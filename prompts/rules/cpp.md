### C++ (C++20)

**Principles**: KISS (Clear > Clever). Zero-race, zero-leak, graceful exit.

**Criteria**:

- Concurrency: Lock-free > `std::atomic` > Mutex. `std::jthread`.
- Resources: Strict RAII. No `new`/`delete`. Smart pointers.
- Performance: Zero-copy. `constexpr`. Vectorization (SIMD/AVX/NEON). `[[likely]]`.
- Modern: `std::filesystem`, `std::span`, Ranges/Views, Concepts, Coroutines.
- Logic: Verify functional combination & flow correctness.
