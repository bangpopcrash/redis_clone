# redis_clone

This is a simple Redis server. It is written in Go. This is a resume
project. It shows good Go and systems engineering skill for a move from
cybersecurity work to software engineering work.

This project loosely follows the guide at
https://www.build-redis-from-scratch.dev/en/first-steps. It then adds more
features: AOF persistence, protection against bad input, and fuzz testing.

## Scope

- Protocol: RESP (REdis Serialization Protocol) over TCP.
- Data types: strings and hashes only. Do not add lists, sets, sorted
  sets, pub/sub, or transactions unless the user asks for them. Extra
  features can weaken a resume project instead of making it stronger.
- Persistence: append-only file (AOF), replayed at startup. Do not add
  RDB snapshots.
- No clustering, no replication, no Lua scripts. This server runs as one
  process on one node.

## Architecture

Do not restructure the architecture without a discussion first.

```
cmd/redis-server/   Entry point only. It reads flags, connects the parts, and handles signals.
internal/resp/      RESP encoder and decoder. It does not know about commands or the store.
internal/store/     In-memory key-value engine. It does not know about the network or RESP.
internal/command/   Dispatch table. It maps each command name to a handler(store, args) function and a reply.
internal/server/    TCP accept loop, one goroutine per connection, safe shutdown.
internal/aof/       Append-only log writer. It also replays the log at startup.
```

Each package must work correctly on its own, with its own tests.
- The `resp` package must never import the `store` package.
- The `store` package must never import the `server` package.

If a change seems to need one of these imports, stop. Talk about the
design again. Do not add a workaround to force the import.

## Engineering Standards

- **Go style**: Use the standard project layout (`cmd/`, `internal/`).
  Keep the code clean with `gofmt`. The code must pass `go vet` and
  `golangci-lint`. Do not add a suppression comment just to silence a
  real problem.
- **Errors**: Wrap each error with `%w` and add context. Do not ignore an
  error. Do not use a bare `panic` in a request-handling path. A bad
  command from a client must never crash the server.
- **Concurrency**: The store must be safe under concurrent access. Use a
  mutex or sharded locks. Test each concurrency-sensitive package with
  the `-race` flag.
- **Logging**: Use structured logging (`log/slog`). Do not use
  `fmt.Println`. Log the connection lifecycle and errors. Do not log
  every command at the info level.
- **No early abstraction**: Do not add interfaces, config options, or
  generic layers for data types that do not exist yet. Three similar
  command handlers are fine. Do not build a framework to replace them.

## Testing Requirements

- Every package under `internal/` needs table-driven unit tests. Follow
  standard Go test patterns: use `_test.go` files and `t.Run` for
  subtests.
- The `internal/resp` package also needs a native Go fuzz test (`go test
  -fuzz`). This parser reads data from a client we do not trust. Fuzzing
  this parser gives the most value.
- The `internal/server` package needs integration tests. These tests
  must open real TCP connections to a running server. Do not use mocks
  here. A mock can hide a real bug in socket handling.
- Run `go test -race ./...` before you call any concurrency-related work
  done.
- Do not mark a task complete because the code looks correct. Run the
  tests first.

## Security Posture

This project uses basic protection. It is not a full security product.

- The RESP parser must reject a bulk string or an array that is too
  large. It must do this before it allocates memory for the data. Read
  the declared length first. Check the length against the limit. Only
  then allocate memory.
- Limit the number of active connections. Close a connection that stays
  idle too long. These rules stop a client from using up all the
  server's resources.
- Add a short comment for each security-related decision: a limit, a
  timeout, or a validation rule. The comment must explain the risk that
  the rule addresses. This project must show that its protections are
  planned, not accidental.
- Do not add authentication, TLS, or access control lists unless the
  user asks for them. Basic protection is the agreed scope. A full
  security feature set is not.

## Workflow Expectations

- Discuss a change to the architecture before you make it. This file
  exists so we do not need to repeat old decisions about scope, layout,
  and persistence in every session.
- Prefer small commits. Each commit should match one phase of work. See
  the task list for the phases.
- Update this file when a settled decision changes. This file, not the
  chat history, is the source of truth.
