### C++ Rules

#### Core Principles

1. **KISS**: Clear > Clever.
2. **Modern**: C++20. No legacy/back-compat.
3. **100% Safe**: Zero races. Zero leaks. Graceful exit.

#### Critical Criteria

- **Concurrency**: Lock-free > `std::atomic` > Mutex. `std::jthread`. No races.
- **Resource Safety**: Strict RAII. No `new`/`delete`. Smart pointers.
- **Performance**: Zero-copy. `constexpr`. **Vectorization** (SIMD/AVX/NEON/FMA). Cache locality. `[[likely]]`.
- **Modern C++**: `std::filesystem` > Boost. `std::span`. Ranges/Views. Concepts. Coroutines.
- **Logic**: Verify functional combination & flow correctness.
