// Package server runs the TCP front end. This package accepts
// connections, checks connection limits, and sends each RESP request to
// a Dispatcher function. This package does not know about any single
// command.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/avkunzman/redis_clone/internal/resp"
)

// Dispatcher runs one decoded RESP request and returns the reply. The
// server package knows only this function type. The cmd/redis-server
// package connects command execution, storage, and persistence, and
// gives the result to Server as a Dispatcher value.
type Dispatcher func(req resp.Value) resp.Value

// Config sets limits on server resources, both per connection and in
// total. These limits stop a client, whether hostile or just broken,
// from using up all server memory or all open files.
type Config struct {
	// MaxConnections sets the limit on connections at the same time. A
	// value of 0 means no limit.
	MaxConnections int
	// IdleTimeout closes a connection that sends no data for this much
	// time. A value of 0 means no timeout.
	IdleTimeout time.Duration
	// RESPLimits sets the maximum size of one RESP frame from a client.
	// The server checks a frame against this limit before it allocates
	// memory for the frame's declared length.
	RESPLimits resp.Limits
}

// DefaultConfig gives good starting values for a small learning project.
// Do not use these values for a production Redis deployment.
var DefaultConfig = Config{
	MaxConnections: 10_000,
	IdleTimeout:    5 * time.Minute,
	RESPLimits:     resp.DefaultLimits,
}

// Server accepts TCP connections. Server sends each decoded request to
// its dispatch function.
type Server struct {
	cfg      Config
	dispatch Dispatcher
	log      *slog.Logger

	active atomic.Int64
}

func New(dispatch Dispatcher, cfg Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, dispatch: dispatch, log: log}
}

// ListenAndServe opens addr and serves connections. ListenAndServe runs
// until ctx is canceled. It then stops new connections and closes each
// open connection. ListenAndServe returns nil after every connection
// handler ends.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", addr, err)
	}
	defer func() { _ = ln.Close() }()

	shuttingDown := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(shuttingDown)
		_ = ln.Close()
	}()

	s.log.Info("listening", "addr", ln.Addr().String())

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-shuttingDown:
				wg.Wait()
				return nil
			default:
				return fmt.Errorf("server: accept: %w", err)
			}
		}

		if !s.acquireSlot() {
			s.rejectConn(conn)
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.releaseSlot()
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Server) acquireSlot() bool {
	if s.cfg.MaxConnections <= 0 {
		return true
	}
	for {
		cur := s.active.Load()
		if cur >= int64(s.cfg.MaxConnections) {
			return false
		}
		if s.active.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (s *Server) releaseSlot() {
	s.active.Add(-1)
}

func (s *Server) rejectConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	w := resp.NewWriter(conn)
	_ = w.Write(resp.NewError("ERR max number of clients reached"))
	s.log.Warn("connection rejected: at capacity", "addr", conn.RemoteAddr().String())
}

// handleConn repeats these steps: read one RESP request, run it, write
// one reply. context.AfterFunc closes conn as soon as ctx is canceled.
// This lets a Read call stop right away during shutdown, even when conn
// is idle.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	addr := conn.RemoteAddr().String()
	s.log.Info("connection opened", "addr", addr)
	defer s.log.Info("connection closed", "addr", addr)
	defer func() { _ = conn.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	reader := resp.NewReaderWithLimits(conn, s.cfg.RESPLimits)
	writer := resp.NewWriter(conn)

	for {
		if s.cfg.IdleTimeout > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout)); err != nil {
				return
			}
		}

		req, err := reader.Read()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Debug("connection read error", "addr", addr, "err", err)
			}
			return
		}

		reply := s.dispatch(req)
		if err := writer.Write(reply); err != nil {
			s.log.Debug("connection write error", "addr", addr, "err", err)
			return
		}
	}
}
