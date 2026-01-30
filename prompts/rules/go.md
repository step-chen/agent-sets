### Go Rules

#### Core Principles

1. **KISS**: Clear > Clever.
2. **Modern**: Go 1.25+. No legacy/back-compat.
3. **100% Safe**: Zero races. Zero leaks. Graceful exit.

#### Critical Criteria

- **Concurrency**: Lock-free > Atomic > Mutex. No races. `sync.Pool` limits.
- **Resource Safety**: Conn/File/Goroutine. Always `defer` close. Use `context`.
- **Performance**: Pre-alloc `make(..,cap)`. `strings.Builder`. No reflect hot-path.
- **Modern Go**: Iterators. `slog`. Generic Interfaces.
- **Logic**: Verify functional combination & flow correctness.
