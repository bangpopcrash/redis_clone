// Package aof stores data with an append-only file. This package logs
// every write command in RESP wire format. At startup, this package
// replays the log in order to rebuild the store's state. This package
// depends only on the resp package. It does not depend on the command
// package or the store package, because "a command is an array of bulk
// strings" is a fact about RESP, not about commands or the store.
package aof

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/avkunzman/redis_clone/internal/resp"
)

// Log is an append-only command log. One file backs each Log.
//
// Fsync policy: Append calls fsync after every write. This choice gives
// up some speed to gain safety. When Append returns, the write is on
// disk. A crash right after that point cannot lose the write. Real
// Redis often uses the faster "everysec" policy by default, but a crash
// under that policy can lose up to one second of writes. This project
// chooses the simpler and safer policy on purpose.
type Log struct {
	mu sync.Mutex
	f  *os.File
	w  *resp.Writer
}

// Open opens the AOF file at path so a caller can append to it. Open
// creates the file when the file does not yet exist.
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("aof: open %s: %w", path, err)
	}
	return &Log{f: f, w: resp.NewWriter(f)}, nil
}

// Append logs one command name and its arguments. Append calls fsync
// before it returns.
func (l *Log) Append(args []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	items := make([]resp.Value, len(args))
	for i, a := range args {
		items[i] = resp.NewBulkString(a)
	}
	if err := l.w.Write(resp.NewArray(items)); err != nil {
		return fmt.Errorf("aof: append: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("aof: fsync: %w", err)
	}
	return nil
}

// Close closes the log's file.
func (l *Log) Close() error {
	return l.f.Close()
}

// Replay reads every command logged at path, in order. Replay calls apply
// for each command. Call Replay once at startup, before the server
// begins to accept connections. A missing file is not an error. A
// missing file means this is the first run of the server.
func Replay(path string, apply func(args []string) error) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("aof: open %s for replay: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	r := resp.NewReader(f)
	for {
		req, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("aof: replay %s: %w", path, err)
		}

		args, err := requestArgs(req)
		if err != nil {
			return fmt.Errorf("aof: replay %s: %w", path, err)
		}
		if err := apply(args); err != nil {
			return fmt.Errorf("aof: replay %s: apply: %w", path, err)
		}
	}
}

func requestArgs(req resp.Value) ([]string, error) {
	if req.Type != resp.Array {
		return nil, errors.New("expected array entry")
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
