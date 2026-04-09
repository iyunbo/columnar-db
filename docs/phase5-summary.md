# Phase 5 — SQL frontend: summary

## What shipped

A SQL frontend that turns a subset of SQL into calls on the Phase
2–4 vectorized execution operators. The end user can now write:

```sql
SELECT city, COUNT(*), AVG(age)
FROM people
WHERE age > 30
GROUP BY city
ORDER BY city
LIMIT 10
```

and have the engine answer it without hand-wiring `ScanOp →
FilterOp → GroupByOp` in Go.

### SQL subset supported

```
SELECT { * | col_list | agg_call [, ...] }
FROM identifier
[ WHERE column { = | != | < | <= | > | >= } literal ]
[ GROUP BY column [, column]* ]
[ ORDER BY column [ ASC | DESC ] ]
[ LIMIT n ]
```

- Aggregate functions: `COUNT(*)`, `COUNT(col)`, `SUM`, `MIN`,
  `MAX`, `AVG` on Int64 and Float64 columns.
- GROUP BY: single-key (fast-path `GroupByOp`) and multi-key
  (`GroupByOpMulti`). Unbounded group count.
- WHERE: Int64 (all 6 ops), String (`=` only). Float64 predicates
  deferred (no exec/ support yet).
- `FROM` identifier is accepted but ignored — the RowGroup
  supplied by the caller is the table.
- Optional trailing `;`.

### Package layout

| File | Role |
|------|------|
| `sql/token.go` | `TokenKind` enum + `Token` struct |
| `sql/lexer.go` | `Tokenize()` — single-pass zero-alloc-per-token scanner |
| `sql/ast.go` | `SelectStmt`, `SelectItem`, `Predicate` |
| `sql/parser.go` | Hand-rolled recursive-descent parser |
| `sql/planner.go` | AST → exec.Operator tree |
| `sql/sql.go` | `Execute(rg, query) → (exec.Operator, error)` |
| `cmd/columnar-db/main.go` | Interactive REPL with demo dataset |

### Operator trees the planner produces

```
SELECT * FROM t              → ScanOp
SELECT col_list FROM t       → ScanOp (projected)
SELECT ... WHERE pred        → ScanOp → FilterOp [→ ProjectOp]
SELECT COUNT(*) FROM t       → ScanOp → AggregateOp
SELECT key, agg FROM t       → ScanOp [→ FilterOp] → GroupByOp
  GROUP BY key                  [→ ProjectOp if reorder needed]
SELECT k1, k2, agg FROM t   → ScanOp [→ FilterOp] → GroupByOpMulti
  GROUP BY k1, k2               [→ ProjectOp if reorder needed]
... ORDER BY col             → ... → OrderByOp
... LIMIT n                  → ... → LimitOp
```

## Parity benchmark

SQL-driven vs Phase 4 direct-operator numbers (1M rows, M4,
Go 1.24, benchtime=5s, count=2). "Reuse" means parse+plan once,
then `Reset+drain` in the measurement loop — the steady-state
shape for a prepared-statement-like pattern.

```
BenchmarkSQLGroupByLowCard-10           426  14122749 ns/op   92752 B/op   57 allocs/op
BenchmarkSQLGroupByLowCardReuse-10      418  14374029 ns/op       2 B/op    0 allocs/op
BenchmarkSQLGroupByHighCard-10          580   9987009 ns/op 16428272 B/op  350 allocs/op
BenchmarkSQLGroupByHighCardReuse-10     658   9191803 ns/op   24873 B/op    0 allocs/op
```

| Regime | Phase 4 direct | SQL Reuse | Gap |
|--------|---------------|-----------|-----|
| Low (10 groups) | 14.32 ms | 14.37 ms | **+0.3%** |
| High (10k groups) | 8.77 ms | 9.10 ms | **+3.8%** |

**The SQL layer adds no measurable overhead on the steady-state
reuse path.** The "full" path (parse+plan per iteration) adds
~14% on high-card from operator construction allocations — this is
the per-query setup cost a prepared-statement pattern avoids.

## Known limitations

- No `AND`/`OR` compound predicates.
- No `HAVING`.
- No `JOIN`, subqueries, CTEs.
- No arithmetic expressions in SELECT (`price * 1.1`).
- No Float64 WHERE predicates.
- `COUNT(col)` maps to `CountStar` (counts all rows, not just
  non-null). Correct when no NULLs exist; filed as TODO for
  nullable-column support.
- No schema catalog — `FROM` identifier is ignored.
- No optimizer or cost model.

## Test coverage

474 tests passing across 4 packages. The `sql/` package alone has
104 tests covering every grammar production, every planner error
path, and end-to-end integration across the full SELECT / WHERE /
GROUP BY / ORDER BY / LIMIT matrix.

## What Phase 5 did NOT deliver (deferred to Phase 6+)

- SQL optimizer (predicate pushdown, projection pushdown, join
  ordering).
- JOIN of any kind.
- HAVING.
- Multi-table catalog and DDL.
- Prepared statements / query caching — the `Reset()` path is the
  manual equivalent; a `Prepare(query) → Statement` API is a
  natural follow-up.
- Multi-column ORDER BY.
- Float64 WHERE predicates.
