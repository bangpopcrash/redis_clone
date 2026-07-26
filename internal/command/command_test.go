package command

import (
	"testing"

	"github.com/avkunzman/redis_clone/internal/resp"
	"github.com/avkunzman/redis_clone/internal/store"
)

// request builds the RESP array-of-bulk-strings shape that a real client
// sends. For example, request("SET", "key", "value").
func request(parts ...string) resp.Value {
	items := make([]resp.Value, len(parts))
	for i, p := range parts {
		items[i] = resp.NewBulkString(p)
	}
	return resp.NewArray(items)
}

func TestDispatchUnknownCommand(t *testing.T) {
	s := store.New()
	got := Dispatch(s, request("NOSUCHCOMMAND"))
	if got.Type != resp.Error {
		t.Fatalf("Dispatch() type = %v, want Error", got.Type)
	}
}

func TestDispatchMalformedRequest(t *testing.T) {
	s := store.New()

	// This value is not an array.
	got := Dispatch(s, resp.NewBulkString("PING"))
	if got.Type != resp.Error {
		t.Fatalf("Dispatch() on non-array type = %v, want Error", got.Type)
	}

	// This array holds an element that is not a bulk string.
	got = Dispatch(s, resp.NewArray([]resp.Value{resp.NewInteger(1)}))
	if got.Type != resp.Error {
		t.Fatalf("Dispatch() on non-bulk-string element type = %v, want Error", got.Type)
	}
}

func TestPingEcho(t *testing.T) {
	s := store.New()

	got := Dispatch(s, request("PING"))
	want := resp.NewSimpleString("PONG")
	if !resp.Equal(got, want) {
		t.Fatalf("PING = %+v, want %+v", got, want)
	}

	got = Dispatch(s, request("PING", "hello"))
	want = resp.NewBulkString("hello")
	if !resp.Equal(got, want) {
		t.Fatalf("PING hello = %+v, want %+v", got, want)
	}

	got = Dispatch(s, request("ECHO", "hi"))
	want = resp.NewBulkString("hi")
	if !resp.Equal(got, want) {
		t.Fatalf("ECHO = %+v, want %+v", got, want)
	}

	got = Dispatch(s, request("ECHO"))
	if got.Type != resp.Error {
		t.Fatalf("ECHO with no args = %+v, want Error", got)
	}
}

func TestSetGet(t *testing.T) {
	s := store.New()

	got := Dispatch(s, request("SET", "key", "value"))
	if !resp.Equal(got, resp.NewSimpleString("OK")) {
		t.Fatalf("SET = %+v, want +OK", got)
	}

	got = Dispatch(s, request("GET", "key"))
	if !resp.Equal(got, resp.NewBulkString("value")) {
		t.Fatalf("GET = %+v, want value", got)
	}

	got = Dispatch(s, request("GET", "missing"))
	if !got.Null {
		t.Fatalf("GET missing = %+v, want null bulk string", got)
	}

	got = Dispatch(s, request("SET", "key"))
	if got.Type != resp.Error {
		t.Fatalf("SET with wrong arity = %+v, want Error", got)
	}
}

func TestDelExists(t *testing.T) {
	s := store.New()
	Dispatch(s, request("SET", "a", "1"))
	Dispatch(s, request("SET", "b", "2"))

	got := Dispatch(s, request("EXISTS", "a", "b", "missing"))
	if !resp.Equal(got, resp.NewInteger(2)) {
		t.Fatalf("EXISTS = %+v, want 2", got)
	}

	got = Dispatch(s, request("DEL", "a", "missing"))
	if !resp.Equal(got, resp.NewInteger(1)) {
		t.Fatalf("DEL = %+v, want 1", got)
	}

	got = Dispatch(s, request("EXISTS", "a"))
	if !resp.Equal(got, resp.NewInteger(0)) {
		t.Fatalf("EXISTS after DEL = %+v, want 0", got)
	}
}

func TestExpireTTLPersist(t *testing.T) {
	s := store.New()
	Dispatch(s, request("SET", "key", "value"))

	got := Dispatch(s, request("TTL", "missing"))
	if !resp.Equal(got, resp.NewInteger(-2)) {
		t.Fatalf("TTL on missing key = %+v, want -2", got)
	}

	got = Dispatch(s, request("TTL", "key"))
	if !resp.Equal(got, resp.NewInteger(-1)) {
		t.Fatalf("TTL on key with no expiry = %+v, want -1", got)
	}

	got = Dispatch(s, request("EXPIRE", "key", "100"))
	if !resp.Equal(got, resp.NewInteger(1)) {
		t.Fatalf("EXPIRE = %+v, want 1", got)
	}

	got = Dispatch(s, request("TTL", "key"))
	if !resp.Equal(got, resp.NewInteger(100)) {
		t.Fatalf("TTL after EXPIRE = %+v, want 100", got)
	}

	got = Dispatch(s, request("EXPIRE", "key", "notanumber"))
	if got.Type != resp.Error {
		t.Fatalf("EXPIRE with bad seconds = %+v, want Error", got)
	}

	got = Dispatch(s, request("PERSIST", "key"))
	if !resp.Equal(got, resp.NewInteger(1)) {
		t.Fatalf("PERSIST = %+v, want 1", got)
	}

	got = Dispatch(s, request("TTL", "key"))
	if !resp.Equal(got, resp.NewInteger(-1)) {
		t.Fatalf("TTL after PERSIST = %+v, want -1", got)
	}
}

func TestHashCommands(t *testing.T) {
	s := store.New()

	got := Dispatch(s, request("HSET", "h", "f1", "v1"))
	if !resp.Equal(got, resp.NewInteger(1)) {
		t.Fatalf("HSET new field = %+v, want 1", got)
	}

	got = Dispatch(s, request("HSET", "h", "f1", "v2"))
	if !resp.Equal(got, resp.NewInteger(0)) {
		t.Fatalf("HSET existing field = %+v, want 0", got)
	}

	got = Dispatch(s, request("HGET", "h", "f1"))
	if !resp.Equal(got, resp.NewBulkString("v2")) {
		t.Fatalf("HGET = %+v, want v2", got)
	}

	got = Dispatch(s, request("HGET", "h", "missing"))
	if !got.Null {
		t.Fatalf("HGET missing field = %+v, want null", got)
	}

	Dispatch(s, request("HSET", "h", "f2", "v3"))
	got = Dispatch(s, request("HGETALL", "h"))
	if got.Type != resp.Array || len(got.Array) != 4 {
		t.Fatalf("HGETALL = %+v, want array of 4 elements", got)
	}

	got = Dispatch(s, request("HDEL", "h", "f1", "missing"))
	if !resp.Equal(got, resp.NewInteger(1)) {
		t.Fatalf("HDEL = %+v, want 1", got)
	}
}

func TestWrongTypeErrors(t *testing.T) {
	s := store.New()
	Dispatch(s, request("SET", "str", "value"))

	got := Dispatch(s, request("HGET", "str", "field"))
	if got.Type != resp.Error {
		t.Fatalf("HGET on string key = %+v, want Error", got)
	}

	Dispatch(s, request("HSET", "hash", "f", "v"))
	got = Dispatch(s, request("GET", "hash"))
	if got.Type != resp.Error {
		t.Fatalf("GET on hash key = %+v, want Error", got)
	}
}
