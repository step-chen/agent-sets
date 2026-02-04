### Python (3.12+)

**Principles**: KISS (Clear > Clever, Explicit > Implicit). Zero-race, zero-leak.

**Criteria**:

- Concurrency: `asyncio` patterns. `Semaphore` limits.
- Resources: Always `with` context managers. Proper cleanup.
- Performance: `set`/`dict` O(1). Generators. `lru_cache`. Avoid `getattr`.
- Modern: Type hints, `match`, Walrus (`:=`).
- Logic: Verify functional combination & flow correctness.
