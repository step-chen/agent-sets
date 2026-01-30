### Java Rules

#### Core Principles

1.  **Modern**: Java 21+ features (Records, Patterns, Text Blocks).
2.  **Clean**: Effective Java items. readability > brevity.
3.  **Safe**: No NPEs (`Optional`). No swallowed exceptions.

#### Critical Criteria

- **Modern Java**: Use `record` for DTOs. `var` for local inference. Text Blocks `"""`.
- **Control Flow**: Pattern Matching for `switch`. Enhanced `instanceof`.
- **Streams**: Use `Stream` API for collections processing. Avoid raw loops unless performance critical.
- **Null Safety**: Avoid returning `null`. Use `Optional<T>`.
- **Error Handling**: Specific Exceptions. NEVER `catch (Exception e)`. NO `e.printStackTrace()`. Use SLF4J/Log4j.
- **Design**: Constructor Injection > Field Injection (`@Autowired` on field). Immutability by default.
