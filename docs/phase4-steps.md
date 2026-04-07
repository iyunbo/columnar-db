# Phase 4 — Aggregation: step plan

**Goal**: Implement hash-based GROUP BY and aggregate functions
(`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`). Phase 4 is also the **first
benchmark battleground inside the regime the vectorized engine was
designed for**: aggregation is one of the canonical wins for
vector-at-a-time execution per the MonetDB X100 paper, and the three
Phase 3 negative results have left the architectural bet entirely on
Phase 4+. Phase 3's benchmarks weren't unfair — they were *outside
the target regime*. Phase 4's are the first ones inside it.

**Decision point**: Step 6's benchmark is the go/no-go signal. If
vectorized aggregation does not at least *match* the naive baseline,
Phase 3's "keep the architecture, the upside is in the target regime"
recommendation has to be revisited.

## Steps

### Step 1 — Plan & architecture (this PR)
- Write this step plan doc.
- Define `Aggregator` interface + `AggregateOp` operator stub.
- Update `docs/architecture.html` with the AggregateOp box and a note
  that Phase 4 is in progress.
- **No behavior change.** This PR is the architectural commitment, not
  the implementation.

The interface uses **columnar per-group state** (the MonetDB/DuckDB
shape): `Init(numGroups) / Grow(numGroups) / Update(vec, sel,
groupIDs) / Finalize(group, out, row)`. Scalar aggregation is just
`numGroups == 1` with `groupIDs == nil`. This is the same shape
both `AggregateOp` (Step 3) and `GroupByOp` (Step 4) use, so Step 4
does **not** force a breaking refactor of Step 2 — caught and fixed
during Step 1 review.

### Step 2 — Aggregate functions (no GROUP BY)
- Implement `Count`, `Sum`, `Min`, `Max`, `Avg` aggregators for `int64`
  and `float64`.
- Each aggregator:
  - `Init()` — reset accumulator state
  - `Update(vec *Vector, sel *Selection)` — tight loop over selection
  - `Finalize() any` — return the result
- Unit tests + correctness twin against a naive row loop.

### Step 3 — `AggregateOp` operator (scalar aggregation, no grouping)
- Implements `Operator`. Pulls batches from its child, calls
  `Update` on each aggregator per batch, returns a single-row batch
  on the first `Next()` and `(nil, false)` after.
- Canonical query: `SELECT COUNT(*), SUM(price) FROM t WHERE age > 30`
- Pipeline: `Scan → Filter → AggregateOp`
- Integration test + zero-alloc contract on the steady state.

### Step 4 — Hash GROUP BY (single key)
- New `GroupByOp` operator. Maintains a hash table from group key →
  per-aggregator state.
- Initial implementation: Go's built-in `map[int64]…` and
  `map[string]…` (open-addressing follow-up filed for after Step 6).
- Canonical query: `SELECT city, COUNT(*) FROM t GROUP BY city`
- Output shape: one row per group, key column(s) + aggregate columns.

### Step 5 — Multi-key + multi-aggregate
- Extend `GroupByOp` to support multiple group keys and multiple
  aggregators in one pass.
- Canonical query:
  `SELECT city, age_bucket, COUNT(*), AVG(price) FROM t GROUP BY city, age_bucket`
- Initial key strategy: string concat (binary-key follow-up filed).

### Step 6 — Benchmark + decision point
- Twin benchmarks: naive row-at-a-time GROUP BY (Go map + per-row
  hash + per-row aggregator updates) vs vectorized `GroupByOp`.
- 1 M-row fixture, canonical query
  `SELECT city, COUNT(*), AVG(age) FROM t GROUP BY city`.
- **Run at TWO cardinalities**, not one, so a win at one and loss
  at the other can't be cherry-picked post hoc:
  - **Low cardinality**: ~10 distinct groups (e.g. 10 cities). Hash
    table fits in L1, naive's `map[string]…` is also L1-resident,
    branch predictor sees stable group IDs. Both should be fast.
  - **High cardinality**: ~10 000 distinct groups (e.g.
    `user_id % 10000`). Hash table spills out of L1, hashing
    dominates, branch predictor sees uniform group ID distribution.
    This is where vectorized's per-batch hash-probe amortization is
    supposed to win.
- Write up in `docs/phase4-benchmark.md` with the same intellectual-
  honesty discipline as Phase 3.
- **Success criterion**: vectorized at least matches naive (≥1.0×)
  on **both** cardinalities. Stretch: ≥2× faster on the high-
  cardinality case.
- **Failure handling**: if vectorized loses on both, revisit the
  architecture honestly. Don't spin a fourth negative result as a
  "narrowing gap"; the model has been given a benchmark inside its
  target regime. If it loses on one and wins on the other, document
  honestly which workload each shape favours and decide whether the
  architecture's value bracket is wide enough to justify the
  complexity.

## Working rules (from short-term memory)

- One PR per step.
- After each PR, run `superpowers:code-reviewer` subagent and address
  the feedback loop until convergence before merging.
- When the architecture changes (Steps 1, 3, 4), update
  `docs/architecture.html` in the same PR.
- Keep zero-alloc steady state on operator hot paths where it already
  exists. New aggregator state is allowed to allocate at `Init()`
  time but not inside the per-batch `Update()` call.

## Null semantics for aggregates (in scope, defined here)

Standard SQL semantics, defined upfront so Step 2 doesn't have to
re-litigate them per aggregator:

- `COUNT(*)` counts rows, ignoring nulls in any column (it just
  counts selected rows).
- `COUNT(col)` counts non-null values in `col`. Nulls are skipped.
- `SUM`, `AVG`, `MIN`, `MAX` skip null inputs. If every input row
  for a group is null (or the group is empty), the result is null
  for `SUM`/`AVG`/`MIN`/`MAX`. `COUNT` of an all-null group is `0`.
- `AVG` over an all-null group is null, not a divide-by-zero.

Each aggregator's `Update` consults `vec.Nulls` and skips null rows.
The output `Vector`'s `Nulls` bitmap is set by `Finalize` for groups
where the result is null.

## Out of scope for Phase 4

- HAVING (post-aggregation filter) — defer to Phase 5 (SQL parser).
- DISTINCT inside aggregates (`COUNT(DISTINCT x)`) — defer.
- Spilling to disk when the hash table doesn't fit — defer; Phase 4
  assumes hash tables fit in memory.
- Custom open-addressing hash table — file as a follow-up after
  Step 6 measures whether Go's built-in map is the bottleneck.
- Two-phase parallel aggregation — defer to a parallelism phase.
