# redis_clone

A simple Redis server written in Go.

This project has these parts:
- The RESP wire protocol.
- A TCP server.
- An in-memory key-value store with expiry.
- Append-only-file (AOF) persistence.
- Basic protection against bad input.

This project uses only the Go standard library. It has no external
dependencies.

## Features

- **Protocol**: RESP2. This includes simple strings, errors, integers, bulk
  strings, and arrays.
- **Commands**: `PING`, `ECHO`, `SET`, `GET`, `DEL`, `EXISTS`, `EXPIRE`,
  `TTL`, `PERSIST`, `HSET`, `HGET`, `HDEL`, `HGETALL`.
- **Persistence**: The server writes each change command to an append-only
  file (AOF). It replays the file at startup.
- **Concurrency**: Each connection runs in its own goroutine. A mutex
  protects the store. The command `go test -race` confirms this.
- **Protection**: The server limits the size of RESP frames. It rejects a
  frame that is too large before it allocates memory for it. The server
  also limits the number of connections and closes idle connections.
- **Tests**: The project has table-driven unit tests, real-TCP integration
  tests, and a Go fuzz test for the RESP decoder.

## Architecture

```
cmd/redis-server/   Entry point. It reads flags, connects the parts, and handles signals.
internal/resp/      RESP encoder and decoder. It does not know about commands or the store.
internal/store/     In-memory key-value engine. It does not know about the network or RESP.
internal/command/   Dispatch table. It maps each command name to a handler and a reply.
internal/server/    TCP accept loop. It starts one goroutine per connection and can shut down safely.
internal/aof/       Append-only log writer. It also replays the log at startup.
```

You can test each package on its own:
- The `resp` package does not import the `store` package.
- The `store` package does not import the `server` package.
- The `server` package uses only a `Dispatcher` function type. It does not
  import the `command` package or the `aof` package.

The `cmd/redis-server` package connects all the parts. It also decides
which commands are written to the AOF log: after each successful
command, it checks `command.IsWrite` and appends only write commands
(for example `SET`, not `GET`).

## How to Run the Server

```sh
go build -o bin/redis-server ./cmd/redis-server
./bin/redis-server -addr 127.0.0.1:6379 -aof redis_clone.aof
```

Flags:

| Flag    | Default           | Meaning                                    |
|---------|-------------------|---------------------------------------------|
| `-addr` | `127.0.0.1:6379`  | The TCP address to listen on.               |
| `-aof`  | `redis_clone.aof` | The AOF file path. An empty value turns off persistence. |

You can send commands with any RESP client, for example `redis-cli -p
6379`. You can also send raw RESP data:

```sh
printf '*1\r\n$4\r\nPING\r\n' | nc 127.0.0.1 6379
# +PONG
```

## Development Commands

```sh
make build       # Build the server.
make test        # Run all tests.
make test-race   # Run all tests with the race detector.
make fuzz        # Fuzz the RESP decoder for 30 seconds.
make lint        # Run gofmt, go vet, and golangci-lint.
```

The CI workflow (`.github/workflows/ci.yml`) runs gofmt, `go vet`,
golangci-lint, and `go test -race` on each push and each pull request.

## Design Decisions

- **AOF fsync policy**: The server calls fsync after each write, before it
  confirms the write to the client. This gives strong durability: a crash
  right after a write cannot lose that write. This method is slower than
  some alternatives. Real Redis often uses the "everysec" fsync policy by
  default. That policy is faster, but a crash can lose up to one second of
  writes. This project chooses safety over speed. See
  `internal/aof/aof.go`.
- **RESP size limits**: A client declares the length of a bulk string or
  array before it sends the data. The decoder checks this length against a
  maximum value before it allocates memory. This stops a client from
  forcing a large memory allocation with a false length. See the `Limits`
  type in `internal/resp/resp.go`.
- **Connection limits**: The `internal/server` package limits the number of
  active connections. When the server is at this limit, it sends a RESP
  error and closes the new connection. The server also closes a connection
  that stays idle too long. These limits stop one client from using all
  the server's connections.
- **Scope**: This project supports only strings and hashes. It runs as a
  single node. It does not support replication, clustering, scripts, or
  pub-sub.
