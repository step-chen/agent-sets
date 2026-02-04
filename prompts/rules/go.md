### Go (1.25+)

**Principles**: KISS (Clear > Clever). Zero-race, zero-leak, graceful exit.

**Criteria**:

- Concurrency: Lock-free > Atomic > Mutex. `sync.Pool` limits.
- Resources: Always `defer` close. Use `context`.
- Performance: Pre-alloc `make(.., cap)`. `strings.Builder`. No reflect hot-path.
- Modern: Iterators, `slog`, Generic Interfaces.
- Logic: Verify functional combination & flow correctness.
