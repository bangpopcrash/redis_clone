package server

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/avkunzman/redis_clone/internal/command"
	"github.com/avkunzman/redis_clone/internal/resp"
	"github.com/avkunzman/redis_clone/internal/store"
)

// startTestServer starts a real Server on a loopback port. It returns
// the server's address and a shutdown function. The shutdown function
// stops the server in a safe way.
func startTestServer(t *testing.T, cfg Config) (addr string, shutdown func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	boundAddr := ln.Addr().String()
	if err := ln.Close(); err != nil { // Free the port. ListenAndServe binds it again below.
		t.Fatalf("ln.Close: %v", err)
	}

	st := store.New()
	s := New(func(req resp.Value) resp.Value { return command.Dispatch(st, req) }, cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx, boundAddr) }()

	// Wait until the server starts to accept connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", boundAddr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	shutdown = func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("ListenAndServe() returned error after shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not shut down within 2s")
		}
	}
	return boundAddr, shutdown
}

func TestServerHandlesRealTCPClient(t *testing.T) {
	addr, shutdown := startTestServer(t, DefaultConfig)
	defer shutdown()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("net.Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	w := resp.NewWriter(conn)
	r := resp.NewReader(conn)

	send := func(parts ...string) resp.Value {
		items := make([]resp.Value, len(parts))
		for i, p := range parts {
			items[i] = resp.NewBulkString(p)
		}
		if err := w.Write(resp.NewArray(items)); err != nil {
			t.Fatalf("write request: %v", err)
		}
		reply, err := r.Read()
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		return reply
	}

	if got := send("PING"); !resp.Equal(got, resp.NewSimpleString("PONG")) {
		t.Fatalf("PING = %+v, want PONG", got)
	}

	if got := send("SET", "key", "value"); !resp.Equal(got, resp.NewSimpleString("OK")) {
		t.Fatalf("SET = %+v, want OK", got)
	}

	if got := send("GET", "key"); !resp.Equal(got, resp.NewBulkString("value")) {
		t.Fatalf("GET = %+v, want value", got)
	}

	if got := send("HSET", "h", "f", "v"); !resp.Equal(got, resp.NewInteger(1)) {
		t.Fatalf("HSET = %+v, want 1", got)
	}
}

func TestServerHandlesMultipleConcurrentClients(t *testing.T) {
	addr, shutdown := startTestServer(t, DefaultConfig)
	defer shutdown()

	const clients = 20
	errCh := make(chan error, clients)

	for i := 0; i < clients; i++ {
		go func(id int) {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				errCh <- err
				return
			}
			defer func() { _ = conn.Close() }()

			w := resp.NewWriter(conn)
			r := resp.NewReader(conn)

			key := "key"
			if err := w.Write(resp.NewArray([]resp.Value{
				resp.NewBulkString("SET"), resp.NewBulkString(key), resp.NewBulkString("v"),
			})); err != nil {
				errCh <- err
				return
			}
			reply, err := r.Read()
			if err != nil {
				errCh <- err
				return
			}
			if !resp.Equal(reply, resp.NewSimpleString("OK")) {
				errCh <- errors.New("unexpected reply to SET")
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < clients; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent client error: %v", err)
		}
	}
}

func TestServerRejectsConnectionsOverMaxConnections(t *testing.T) {
	cfg := DefaultConfig
	cfg.MaxConnections = 1
	addr, shutdown := startTestServer(t, cfg)
	defer shutdown()

	// Keep the one allowed connection slot open. A PING round-trip
	// proves the server's Accept loop has already registered this
	// connection's slot before we dial the second connection: dialing
	// on a fixed sleep is not enough, since a busy CI runner can delay
	// the Accept loop past any fixed delay, letting the second dial
	// win the single slot and hang until the idle timeout instead of
	// being rejected.
	holder, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("net.Dial (holder): %v", err)
	}
	defer func() { _ = holder.Close() }()

	holderW := resp.NewWriter(holder)
	holderR := resp.NewReader(holder)
	if err := holderW.Write(resp.NewArray([]resp.Value{resp.NewBulkString("PING")})); err != nil {
		t.Fatalf("write PING (holder): %v", err)
	}
	if _, err := holderR.Read(); err != nil {
		t.Fatalf("read PING reply (holder): %v", err)
	}

	rejected, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("net.Dial (rejected): %v", err)
	}
	defer func() { _ = rejected.Close() }()

	// A deadline turns any unexpected server behavior into a fast,
	// clear failure instead of a hang up to the idle timeout.
	if err := rejected.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	r := resp.NewReader(rejected)
	reply, err := r.Read()
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply.Type != resp.Error {
		t.Fatalf("reply type = %v, want Error (max clients reached)", reply.Type)
	}
}

func TestServerEnforcesIdleTimeout(t *testing.T) {
	cfg := DefaultConfig
	cfg.IdleTimeout = 100 * time.Millisecond
	addr, shutdown := startTestServer(t, cfg)
	defer shutdown()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("net.Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("Read() succeeded, want connection closed by idle timeout")
	}
	if !errors.Is(err, io.EOF) {
		t.Logf("Read() error (acceptable, just not nil): %v", err)
	}
}

func TestServerGracefulShutdownClosesActiveConnections(t *testing.T) {
	addr, shutdown := startTestServer(t, DefaultConfig)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("net.Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	shutdown()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("Read() succeeded after shutdown, want connection closed")
	}
}
