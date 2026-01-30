### Python Rules

#### Core Principles

1. **KISS**: Clear > Clever. Explicit > Implicit.
2. **Modern**: Python 3.18+. No legacy/back-compat.
3. **100% Safe**: Zero races. Zero leaks. Graceful exit.

#### Critical Criteria

- **Concurrency**: `asyncio` patterns. `Semaphore` limits. No races.
- **Resource Safety**: Always `with` context managers. Proper cleanup.
- **Performance**: `set`/`dict` O(1). Generators. `lru_cache`. Omit `getattr`.
- **Modern Python**: Type hints. Match. Walrus (`:=`).
- **Logic**: Verify functional combination & flow correctness.
