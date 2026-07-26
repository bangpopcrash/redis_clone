// Command redis-server runs the simplified Redis server. This command
// reads the command-line flags. This command then connects the store,
// the AOF persistence (when the user turns it on), and the TCP server.
// This command waits until it gets a SIGINT signal or a SIGTERM signal.
// It then shuts down in a safe way.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/avkunzman/redis_clone/internal/aof"
	"github.com/avkunzman/redis_clone/internal/command"
	"github.com/avkunzman/redis_clone/internal/resp"
	"github.com/avkunzman/redis_clone/internal/server"
	"github.com/avkunzman/redis_clone/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "the TCP address to listen on")
	aofPath := flag.String("aof", "redis_clone.aof", "the path to the append-only file; an empty value turns off persistence")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := run(*addr, *aofPath, log); err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run(addr, aofPath string, log *slog.Logger) error {
	s := store.New()

	var aofLog *aof.Log
	if aofPath != "" {
		if err := aof.Replay(aofPath, func(args []string) error {
			command.Dispatch(s, requestFromArgs(args))
			return nil
		}); err != nil {
			return fmt.Errorf("aof replay: %w", err)
		}

		var err error
		aofLog, err = aof.Open(aofPath)
		if err != nil {
			return fmt.Errorf("aof open: %w", err)
		}
		defer func() { _ = aofLog.Close() }()
	}

	dispatch := func(req resp.Value) resp.Value {
		reply := command.Dispatch(s, req)
		if aofLog != nil && reply.Type != resp.Error {
			if args, err := command.ParseRequest(req); err == nil && len(args) > 0 && command.IsWrite(args[0]) {
				if err := aofLog.Append(args); err != nil {
					log.Error("aof append failed", "err", err)
				}
			}
		}
		return reply
	}

	srv := server.New(dispatch, server.DefaultConfig, log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return srv.ListenAndServe(ctx, addr)
}

func requestFromArgs(args []string) resp.Value {
	items := make([]resp.Value, len(args))
	for i, a := range args {
		items[i] = resp.NewBulkString(a)
	}
	return resp.NewArray(items)
}
