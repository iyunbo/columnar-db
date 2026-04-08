# Phase 5 — SQL frontend: step plan

**Goal**: turn a subset of SQL into calls on the Phase 2–4
execution operators, so that an end user can write

```sql
SELECT city, COUNT(*), AVG(age)
FROM people
WHERE age > 30
GROUP BY city
```

and the engine answers it without anyone hand-wiring `ScanOp →
FilterOp → GroupByOp` in Go.

**Why now**: Phase 4 Step 6 confirmed the vectorized engine is
competitive inside its target regime and worth keeping. Every
operator needed for the canonical GROUP BY query exists, is
tested, and has benchmarks. What's missing is a way to invoke
them without writing Go. Phase 5 closes that gap.

**Decision point**: there isn't one. Phase 5 is plumbing — a
successful Phase 5 is "the integration test at Step 6 passes and
the operators' existing benchmarks are unaffected." No
architectural bets to win or lose.

## SQL subset in scope for Phase 5

```
stmt        := select_stmt ";"?
select_stmt := "SELECT" select_list
               "FROM" identifier
               [ "WHERE" predicate ]
               [ "GROUP" "BY" column_list ]
select_list := select_item ("," select_item)*
select_item := "*"                              -- Step 3
             | column_ref                       -- Step 3
             | agg_func "(" ("*" | column_ref) ")"  -- Step 5
agg_func    := "COUNT" | "SUM" | "MIN" | "MAX" | "AVG"
predicate   := column_ref comparator literal    -- Step 4
comparator  := "=" | "!=" | "<" | "<=" | ">" | ">="
literal     := int_lit | float_lit | string_lit
column_ref  := identifier
column_list := column_ref ("," column_ref)*
```

**Explicitly out of scope** (filed for Phase 6+ unless noted):
- JOIN of any kind → Phase 6
- HAVING → Phase 5 follow-up if trivial, else Phase 6
- ORDER BY, LIMIT → Phase 5.5 if trivial
- Subqueries, CTEs, UNION → later phase
- DDL (CREATE TABLE) and DML (INSERT/UPDATE/DELETE) → a
  later write-path phase; Phase 5 reads from an in-memory
  RowGroup supplied by the caller
- Arithmetic or function expressions in SELECT
  (`SELECT price * 1.1`) → later phase
- Compound predicates with AND/OR — `FilterOp` supports AND only
  by *chaining* two `FilterOp` instances, not by evaluating a
  predicate tree in one op. OR has no current operator-level
  support. Phase 5 Step 4 ships single-predicate WHERE only;
  `a AND b` as two chained `FilterOp`s is filed as Step 4.5
  polish if trivial, OR is deferred entirely
- Quoted identifiers, case-insensitive matching for column names,
  SQL NULL literal — documented limitations, not bugs

## Steps

### Step 1 — Plan + tokenizer skeleton + architecture.html update (this PR)
- This plan doc.
- Package skeleton: `sql/` directory with `token.go` (token
  types), `lexer.go` (stub or first working cut), and a top-level
  `sql.Execute(rg, query) (exec.Operator, error)` entry point
  that currently just errors "not implemented". Returning an
  `exec.Operator` (not a materialized `[]*Batch`) keeps the
  streaming + `Reset()` contract every other layer already
  follows. Lets downstream steps plug in incrementally.
- `docs/architecture.html`: subtitle bump in Step 1; the full
  `sql/` layer box in the SVG layer diagram lands in Step 2 (real
  Lexer ships) — subsequent steps update the diagram in-PR per
  the architecture-doc memory rule.
- **No query execution yet.** This PR is the structural commitment.

### Step 2 — Tokenizer
- Full `lexer.go`: identifiers, int literals, float literals,
  single-quoted string literals, keywords (`SELECT`, `FROM`,
  `WHERE`, `GROUP`, `BY`, `COUNT`, `SUM`, `MIN`, `MAX`, `AVG`,
  `AS`), operators (`=`, `!=`, `<`, `<=`, `>`, `>=`, `*`, `,`,
  `(`, `)`, `;`), whitespace skipping.
- Errors carry a byte offset into the input so Step 3's parser
  can produce useful messages.
- Unit tests covering each token class, edge cases (empty input,
  trailing whitespace, adjacent operators, unterminated string,
  numeric literal followed by identifier).

### Step 3 — AST + parser, `SELECT col_list FROM t` only
- `ast.go`: `SelectStmt`, `SelectItem` (star / column / agg),
  `Predicate` (stubbed, not parsed yet), identifier types.
- `parser.go`: hand-rolled recursive descent — parses
  `SELECT { * | col_list } FROM identifier` and produces a
  `*SelectStmt`. Predicate + GROUP BY are parsed as stubs that
  error "not supported yet" so Step 4/5 can fill them in.
- `planner.go`: translates the AST into an operator tree. For
  this step: just `ScanOp` with the requested projection. Uses
  the RowGroup's schema to validate column names and compute
  column indexes.
- **Table-name handling**: `Execute(rg, query)` takes a single
  RowGroup, and the `FROM` identifier is **ignored** for Phase 5
  — any identifier parses successfully. Multi-table catalog
  support is a Phase 6+ concern. Locking this in now so Step 3
  doesn't stall on the decision mid-implementation.
- Integration test: `sql.Execute(rg, "SELECT name, age FROM t")`
  vs hand-wired `ScanOp` producing byte-identical batches on a
  small fixture.

### Step 4 — WHERE clause → FilterOp
- Parser: extend with `WHERE column_ref comparator literal` (one
  predicate only for Step 4 — AND/OR polish filed for Step 4.5).
- Planner: insert `FilterOp` between `ScanOp` and the top of the
  tree. Literal-to-column coercion rules, decided up front:
    - Int literal + `Int64` column → use literal as-is.
    - Float literal + `Float64` column → use literal as-is.
    - Int literal + `Float64` column → implicit widen to float64.
    - Float literal + `Int64` column → planner error (no silent
      truncation).
    - String literal + `String` column → use as-is.
    - Any other combination → planner error with column name,
      column type, literal type in the message.
  The planner picks the correct typed predicate (`Int64Gt`,
  `StringEq`, …) based on the column type.
- Integration test: `SELECT age FROM t WHERE age > 30` matches
  hand-wired `Scan → Filter`. Include one test for a type
  mismatch and assert the planner's error message.
- Single-predicate support maps to the existing `Int64Gt` /
  `StringEq` family. If a caller writes `age = 30`, the planner
  picks `Int64Eq`; `age != 30` picks `Int64Ne`; etc.

### Step 5 — Scalar aggregation and GROUP BY
- Parser: extend `select_item` to accept
  `COUNT(*)`, `COUNT(col)`, `SUM(col)`, `MIN(col)`, `MAX(col)`,
  `AVG(col)`. Extend the top-level grammar with `GROUP BY
  column_list`.
- Planner:
  - No GROUP BY → `AggregateOp` with one spec per aggregate.
  - Single-key GROUP BY → `GroupByOp` (fast path).
  - Multi-key GROUP BY → `GroupByOpMulti`.
  - Mixing a plain column with an aggregate without GROUP BY is
    a planner error (standard SQL semantics).
  - Each aggregate spec picks the right aggregator based on the
    input column type (`Int64Sum` vs `Float64Sum`, etc.). Invalid
    combinations (`SUM(string_col)`) produce a clear planner
    error.
- Integration tests: the three canonical queries from Phase 4
  benchmarks, run through SQL instead of hand-wired:
  - `SELECT COUNT(*) FROM t WHERE age > 30`
  - `SELECT city, COUNT(*), AVG(age) FROM t GROUP BY city`
  - `SELECT city, age_bucket, COUNT(*), AVG(price) FROM t
    GROUP BY city, age_bucket`
- Each test asserts byte-identical output against the
  hand-wired pipeline.

### Step 6 — End-to-end integration + parity benchmark
- Broader integration suite: at least a dozen parser +
  planner + exec end-to-end tests over a shared fixture,
  covering the SELECT/WHERE/GROUP BY matrix.
- Parity benchmark: rerun the Phase 4 Step 6 low-/high-/
  extra-high-cardinality twin benchmarks, but drive them
  through `sql.Execute(rg, "...")`. Compare against the
  direct-operator numbers from
  `docs/phase4-benchmark.md`. Expected result: SQL adds a
  planner cost at operator construction time which is
  amortized across the drain, so steady-state ns/op should be
  within 1–2 % of direct invocation. If the gap is wider,
  investigate before merging.
- Short write-up: `docs/phase5-summary.md` covering SQL subset,
  known limitations, how to extend in Phase 6.

## Working rules (from short-term memory, still active)

- One PR per step.
- After each PR, run `superpowers:code-reviewer` subagent and
  address the feedback loop until convergence before merging.
- When the architecture changes (Steps 1, 5), update
  `docs/architecture.html` in the same PR.
- No new per-row allocations on the execution hot path from the
  SQL layer — all parsing and planning happens once, before the
  first `Next()` call.
- The lexer and parser return errors, not panics, for any
  user-supplied input; panics are reserved for invariant
  violations the user cannot trigger.

## Explicit non-goals for Phase 5

- No optimizer. The planner is a direct translator: WHERE goes
  to FilterOp, GROUP BY goes to GroupByOp, aggregates go to the
  aggregator list. No predicate pushdown, no projection pushdown
  beyond what ScanOp already does, no join ordering (no joins).
  Query planning is a whole phase of its own (Phase 6 or 7).
- No cost model. Everything is a rule.
- No multi-statement sessions. `sql.Execute` is one query in,
  one result stream out, stateless.
- No schema catalog. The RowGroup supplied by the caller IS the
  table; its name in the `FROM` clause is checked against a
  single caller-supplied name (or ignored, depending on what's
  simplest — to be decided in Step 3).
