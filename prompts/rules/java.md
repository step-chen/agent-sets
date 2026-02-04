### Java (21+)

**Principles**: Modern features (Records, Patterns, Text Blocks). Readability > Brevity. No NPEs.

**Criteria**:

- Modern: `record` for DTOs. `var` for local inference. Text Blocks `"""`.
- Control Flow: Pattern Matching `switch`. Enhanced `instanceof`.
- Streams: Use `Stream` API. Avoid raw loops unless perf critical.
- Null Safety: Use `Optional<T>`. Avoid returning `null`.
- Errors: Specific Exceptions. Never `catch (Exception e)`. Use SLF4J/Log4j.
- Design: Constructor Injection > Field `@Autowired`. Immutability default.
