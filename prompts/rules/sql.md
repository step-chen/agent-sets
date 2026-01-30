### SQL Rules

#### Core Principles

1. **Performance**: Index usage. Avoid full text search unless necessary.
2. **Safety**: No SQL Injection (Parameter Input).
3. **Consistency**: ACID compliance awareness.

#### Critical Criteria

- **Query Optimization**: Avoid `SELECT *`. Use specific columns. Check for N+1 problems.
- **Indexing**: Ensure WHERE/JOIN columns are indexed. Avoid functions on indexed columns in predicates.
- **Transactions**: Ensure atomic operations are wrapped in transactions.
- **Modern SQL**: Use CTEs (Common Table Expressions) for readability.
