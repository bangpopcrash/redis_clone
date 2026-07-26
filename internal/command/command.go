// Package command sends decoded RESP requests to store operations. This
// package builds the RESP reply for each request. This is the only
// package that uses both the resp package and the store package.
package command

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/avkunzman/redis_clone/internal/resp"
	"github.com/avkunzman/redis_clone/internal/store"
)

// Handler runs one command against s. Handler returns the RESP reply.
type Handler func(s *store.Store, args []string) resp.Value

// Table maps each command name, in upper case, to its handler. Table is
// safe to read from more than one goroutine at a time.
var Table = map[string]Handler{
	"PING":    handlePing,
	"ECHO":    handleEcho,
	"SET":     handleSet,
	"GET":     handleGet,
	"DEL":     handleDel,
	"EXISTS":  handleExists,
	"EXPIRE":  handleExpire,
	"TTL":     handleTTL,
	"PERSIST": handlePersist,
	"HSET":    handleHSet,
	"HGET":    handleHGet,
	"HDEL":    handleHDel,
	"HGETALL": handleHGetAll,
}

// WriteCommands lists every command that changes the store. A caller that
// writes to the AOF log (see internal/aof) uses this list to decide what
// to log. That caller does not need to know what each command does.
var WriteCommands = map[string]struct{}{
	"SET":     {},
	"DEL":     {},
	"EXPIRE":  {},
	"PERSIST": {},
	"HSET":    {},
	"HDEL":    {},
}

// IsWrite reports whether name changes the store. IsWrite ignores upper
// case and lower case in name.
func IsWrite(name string) bool {
	_, ok := WriteCommands[strings.ToUpper(name)]
	return ok
}

// Dispatch reads a command name and its arguments from req, then runs the
// command. Dispatch expects req to be a RESP array of bulk strings. This
// is the same request shape that a real Redis client sends.
func Dispatch(s *store.Store, req resp.Value) resp.Value {
	args, err := ParseRequest(req)
	if err != nil {
		return resp.NewError("ERR " + err.Error())
	}
	if len(args) == 0 {
		return resp.NewError("ERR empty command")
	}

	name := strings.ToUpper(args[0])
	handler, ok := Table[name]
	if !ok {
		return resp.NewError("ERR unknown command '" + args[0] + "'")
	}
	return handler(s, args[1:])
}

// ParseRequest reads the command name and the arguments from a RESP
// array-of-bulk-strings request. This is the request shape that every
// real Redis client sends.
func ParseRequest(req resp.Value) ([]string, error) {
	if req.Type != resp.Array {
		return nil, errors.New("expected array request")
	}
	args := make([]string, len(req.Array))
	for i, v := range req.Array {
		if v.Type != resp.BulkString {
			return nil, errors.New("expected bulk string array elements")
		}
		args[i] = v.Str
	}
	return args, nil
}

func wrongArgs(cmd string) resp.Value {
	return resp.NewError("ERR wrong number of arguments for '" + strings.ToLower(cmd) + "' command")
}

func typeErrorOrNil(err error) (resp.Value, bool) {
	if err == nil {
		return resp.Value{}, false
	}
	if errors.Is(err, store.ErrWrongType) {
		return resp.NewError("WRONGTYPE Operation against a key holding the wrong kind of value"), true
	}
	return resp.NewError("ERR " + err.Error()), true
}

func handlePing(_ *store.Store, args []string) resp.Value {
	if len(args) == 0 {
		return resp.NewSimpleString("PONG")
	}
	if len(args) == 1 {
		return resp.NewBulkString(args[0])
	}
	return wrongArgs("PING")
}

func handleEcho(_ *store.Store, args []string) resp.Value {
	if len(args) != 1 {
		return wrongArgs("ECHO")
	}
	return resp.NewBulkString(args[0])
}

func handleSet(s *store.Store, args []string) resp.Value {
	if len(args) != 2 {
		return wrongArgs("SET")
	}
	s.Set(args[0], args[1])
	return resp.NewSimpleString("OK")
}

func handleGet(s *store.Store, args []string) resp.Value {
	if len(args) != 1 {
		return wrongArgs("GET")
	}
	v, ok, err := s.Get(args[0])
	if reply, isErr := typeErrorOrNil(err); isErr {
		return reply
	}
	if !ok {
		return resp.NewNullBulkString()
	}
	return resp.NewBulkString(v)
}

func handleDel(s *store.Store, args []string) resp.Value {
	if len(args) == 0 {
		return wrongArgs("DEL")
	}
	return resp.NewInteger(int64(s.Del(args...)))
}

func handleExists(s *store.Store, args []string) resp.Value {
	if len(args) == 0 {
		return wrongArgs("EXISTS")
	}
	return resp.NewInteger(int64(s.Exists(args...)))
}

func handleExpire(s *store.Store, args []string) resp.Value {
	if len(args) != 2 {
		return wrongArgs("EXPIRE")
	}
	seconds, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return resp.NewError("ERR value is not an integer or out of range")
	}
	if s.Expire(args[0], time.Duration(seconds)*time.Second) {
		return resp.NewInteger(1)
	}
	return resp.NewInteger(0)
}

func handleTTL(s *store.Store, args []string) resp.Value {
	if len(args) != 1 {
		return wrongArgs("TTL")
	}
	ttl, found, hasExpiry := s.TTL(args[0])
	if !found {
		return resp.NewInteger(-2)
	}
	if !hasExpiry {
		return resp.NewInteger(-1)
	}
	// Round to the nearest second, the same way real Redis does. A small
	// gap in time always passes between an EXPIRE command and the next
	// TTL command. Without rounding, this gap could make TTL report one
	// second less than the correct value.
	seconds := int64((ttl + 500*time.Millisecond) / time.Second)
	return resp.NewInteger(seconds)
}

func handlePersist(s *store.Store, args []string) resp.Value {
	if len(args) != 1 {
		return wrongArgs("PERSIST")
	}
	if s.Persist(args[0]) {
		return resp.NewInteger(1)
	}
	return resp.NewInteger(0)
}

func handleHSet(s *store.Store, args []string) resp.Value {
	if len(args) != 3 {
		return wrongArgs("HSET")
	}
	isNew, err := s.HSet(args[0], args[1], args[2])
	if reply, isErr := typeErrorOrNil(err); isErr {
		return reply
	}
	if isNew {
		return resp.NewInteger(1)
	}
	return resp.NewInteger(0)
}

func handleHGet(s *store.Store, args []string) resp.Value {
	if len(args) != 2 {
		return wrongArgs("HGET")
	}
	v, ok, err := s.HGet(args[0], args[1])
	if reply, isErr := typeErrorOrNil(err); isErr {
		return reply
	}
	if !ok {
		return resp.NewNullBulkString()
	}
	return resp.NewBulkString(v)
}

func handleHDel(s *store.Store, args []string) resp.Value {
	if len(args) < 2 {
		return wrongArgs("HDEL")
	}
	n, err := s.HDel(args[0], args[1:]...)
	if reply, isErr := typeErrorOrNil(err); isErr {
		return reply
	}
	return resp.NewInteger(int64(n))
}

func handleHGetAll(s *store.Store, args []string) resp.Value {
	if len(args) != 1 {
		return wrongArgs("HGETALL")
	}
	fields, err := s.HGetAll(args[0])
	if reply, isErr := typeErrorOrNil(err); isErr {
		return reply
	}
	items := make([]resp.Value, 0, len(fields)*2)
	for k, v := range fields {
		items = append(items, resp.NewBulkString(k), resp.NewBulkString(v))
	}
	return resp.NewArray(items)
}
