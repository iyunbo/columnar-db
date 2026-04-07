# Phase 3: Vectorized Scan Engine — Step by Step

Phase 3 builds the execution layer: read columnar data in batches, filter it, and project columns — all without materializing rows until the very end.

**Each step produces working code with tests. No step depends on finishing the whole phase.**

The guiding idea is **vectorized execution**: process ~1024 values at a time in tight loops over typed arrays. This is the opposite of the classic row-at-a-time Volcano model — and it's what makes columnar engines (DuckDB, ClickHouse, Vectorwise) fast.

---

## Step 1: Vector — A Batch of Column Values

**Goal:** Define the in-memory batch type that flows through the execution engine.

**What to build:**
- `Vector` struct: a typed batch of up to `VectorSize` (= 1024) values from one column
  - `Type ColumnType`
  - `Values ColumnValues` (reuse Phase 1 typed arrays, capped at 1024)
  - `Nulls NullBitmap`
  - `Length int` (actual count, may be < 1024 for the last batch)
- Constants: `VectorSize = 1024`
- Helpers: `NewVector(type)`, `Reset()`, `Len()`, `IsNull(i)`

**Why 1024?** Big enough to amortize per-call overhead and fit tight inner loops; small enough to stay in L1/L2 cache.

**Files:**
```
exec/vector.go
exec/vector_test.go
```

**Test:** Create vectors for each column type, fill with 1024 values + some nulls, read them back.

**Time:** ~1 hour

---

## Step 2: Scan Operator — Column Chunk → Vectors

**Goal:** Read a column chunk from a row group and produce a stream of vectors.

**What to build:**
- `ScanOp` operator:
  - Inputs: a `RowGroup` + list of column names to read
  - Output: iterator yielding `Batch` (a set of aligned `Vector`s, one per requested column)
- `Batch` struct: `Vectors []*Vector`, `Length int`
- Method: `Next() (*Batch, bool)` — returns next batch of up to 1024 rows, or `false` when exhausted
- Decode encoded chunks (Phase 2 encodings) into plain vectors on the fly

**Why operator-shaped?** Sets the pattern for Filter/Project/Agg later — everything is `Next() (*Batch, bool)`.

**Files:**
```
exec/batch.go
exec/scan.go
exec/scan_test.go
```

**Test:**
1. Build a row group with 5000 Int64 values using Phase 1 APIs
2. Scan it → expect 5 batches (1024, 1024, 1024, 1024, 904)
3. Verify values match input order, nulls preserved

**Time:** ~2 hours

---

## Step 3: Selection Vector

**Goal:** Represent "which rows in this batch are still alive" without copying data.

**What to build:**
- `Selection` type: a compact index array `[]uint16` (positions inside the current 1024-row batch)
- Operations:
  - `NewFull(n)` — selects rows 0..n-1
  - `Add(i)`, `Len()`, `Indices() []uint16`
- Rationale: late materialization — downstream operators loop only over selected indices instead of moving bytes

**Why uint16?** VectorSize = 1024 fits in 11 bits; `uint16` keeps it cache-friendly.

**Files:**
```
exec/selection.go
exec/selection_test.go
```

**Test:** Build full selection over 1024, drop every other row, verify indices. Empty-selection edge case.

**Time:** ~45 minutes

---

## Step 4: Filter Operator — Predicates on Vectors

**Goal:** Apply a predicate to a vector and narrow the selection.

**What to build:**
- `Predicate` interface: `Eval(vec *Vector, in Selection, out *Selection)`
- Concrete predicates (Int64 first, then extend):
  - `Eq(col, value)`, `Lt`, `Gt`, `Le`, `Ge`, `Ne`
- `FilterOp` operator:
  - Wraps a child operator
  - Applies predicate to each incoming batch
  - Emits the batch with an updated selection (or skips fully-filtered batches)
- Null handling: nulls never match predicates (three-valued logic, simple version)

**Key detail:** the filter loop is the hot path. Write it as a tight `for i := 0; i < n; i++` over a typed slice — no interface calls inside the loop body.

**Files:**
```
exec/predicate.go
exec/filter.go
exec/filter_test.go
```

**Test:**
1. Scan a row group with 10,000 Int64 rows (values 0..9999)
2. Apply `value > 5000` → expect 4999 rows
3. Apply `value == 42` → expect 1 row
4. Null values never pass

**Time:** ~2 hours

---

## Step 5: Projection Operator — Column Subset

**Goal:** Drop columns the query doesn't need.

**What to build:**
- `ProjectOp` operator:
  - Wraps a child operator
  - Keeps only specified columns in the output batch
  - Preserves selection unchanged
- Optional push-down: if Project sits directly above Scan, pass the column list down so Scan never decodes unneeded columns (big win)

**Files:**
```
exec/project.go
exec/project_test.go
```

**Test:** Scan 5 columns → project 2 → verify only those 2 vectors present, selection intact.

**Time:** ~45 minutes

---

## Step 6: End-to-End Pipeline + Benchmark

**Goal:** Wire Scan → Filter → Project together and measure.

**What to build:**
- Integration test simulating a simple query:
  `SELECT name, age FROM t WHERE age > 30`
- Benchmark comparing:
  - Naive row-at-a-time scan (write a trivial baseline)
  - Vectorized pipeline
- Measure: rows/sec, ns/row, allocations/op

**Files:**
```
exec/pipeline_test.go
exec/pipeline_bench_test.go
```

**Test + success criteria:**
1. 1M-row dataset, 3 columns
2. Vectorized pipeline ≥ 5× faster than row-at-a-time baseline
3. Zero allocations inside the filter inner loop (`b.ReportAllocs()`)

**Time:** ~2 hours

---

## Summary

| Step | What | Time | Test |
|------|------|------|------|
| 1 | Vector (batch of column values, 1024) | 1h | Round-trip per type |
| 2 | Scan operator (chunk → batches) | 2h | 5000 rows → 5 batches |
| 3 | Selection vector (uint16 indices) | 45min | Full/empty/subset |
| 4 | Filter operator + predicates | 2h | value > N correctness |
| 5 | Projection operator | 45min | Column subset |
| 6 | End-to-end pipeline + benchmark | 2h | 5× faster, 0 allocs |
| **Total** | | **~8.5 hours** | |

## Key Concepts Recap

- **Vectorized execution** — batch of ~1024 values processed in tight loops (SIMD-friendly, branch-predictor-friendly)
- **Selection vector** — narrow the live row set without copying data
- **Late materialization** — don't reconstruct rows until the very end (ideally never, for aggregates)
- **Operator model** — `Next() (*Batch, bool)` is the universal interface; everything composes

## Ready?

Start with Step 1. Each step gets its own PR.
