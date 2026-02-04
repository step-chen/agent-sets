### SQL

**Principles**: Performance (index usage). Safety (no SQL injection). ACID awareness.

**Criteria**:

- Queries: Avoid `SELECT *`. Check for N+1 problems.
- Indexing: WHERE/JOIN columns indexed. No functions on indexed predicates.
- Transactions: Wrap atomic operations in transactions.
- Modern: Use CTEs for readability.
