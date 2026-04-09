# Columnar DB — Detailed Plan

> **Status: Phases 1–5 complete.** The project ships a working SQL
> engine with an interactive REPL, vectorized execution, columnar
> storage, and honest benchmarks. See [phase5-summary.md](phase5-summary.md)
> for what shipped and [phase4-benchmark.md](phase4-benchmark.md)
> for the architecture decision. Phases 6–8 below are the original
> roadmap stretch goals — deferred as out of scope for the current
> project (see the README for rationale).

## Goal

Build a column-oriented analytical database from scratch in Go. Understand how modern OLAP engines (ClickHouse, DuckDB, SingleStore) work under the hood by implementing each layer ourselves.

## Design Principles

1. **From scratch** — No existing DB engines, no Apache Arrow (understand by building)
2. **Incremental** — Each phase produces a working, testable component
3. **Go** — Simple, fast compilation, great concurrency, no magic
4. **Learn by doing** — Every design choice documented with "why"

## References

| Resource | Use For |
|----------|---------|
| [CMU 15-721](https://15721.courses.cs.cmu.edu/) | Column store theory, vectorized execution |
| [DuckDB source](https://github.com/duckdb/duckdb) | Cleanest OLAP implementation reference |
| [ClickHouse docs](https://clickhouse.com/docs) | Production column store design |
| [Build Your Own DB (Go)](https://build-your-own.org/database/) | Storage engine patterns in Go |
| [Designing Data-Intensive Applications](https://dataintensive.net/) | Distributed systems theory |

---

## Phase 1: Column Storage Format (Weeks 1-2)

**Goal:** Read and write columnar data files on disk.

### What to build
- Column chunk: a contiguous array of values of one type
- Supported types: Int64, Float64, String, Bool
- Null bitmap (bit-packed)
- Column metadata: type, count, null count, min/max
- File format: header + column chunks + footer (self-describing)

### Key concepts
- **Why columns?** Sequential scan of one field reads contiguous memory → CPU cache friendly, compression friendly
- **Row group:** Chunk of N rows (e.g., 65536) stored together. Each row group has one chunk per column

### Deliverables
```
storage/
├── types.go          # Column types (Int64, Float64, String, Bool)
├── column.go         # Column chunk: typed array + null bitmap
├── rowgroup.go       # Row group: collection of columns for N rows
├── writer.go         # Write row groups to disk
├── reader.go         # Read row groups from disk
├── format.go         # File format: header, metadata, footer
└── storage_test.go   # Round-trip tests: write → read → verify
```

### Success criteria
- Write 1M rows (5 columns) to disk
- Read back and verify data integrity
- Print file size vs equivalent CSV (should be smaller)

---

## Phase 2: Encoding & Compression (Weeks 3-4)

**Goal:** Reduce storage size through column-aware encoding.

### What to build
- **Plain encoding** — Raw values (baseline)
- **Run-Length Encoding (RLE)** — For columns with consecutive repeated values (e.g., status codes, country)
- **Dictionary encoding** — For low-cardinality strings (e.g., city names, categories)
- **Delta encoding** — For sorted/monotonic columns (e.g., timestamps, IDs)
- **LZ4 compression** — General-purpose block compression on top of encoding

### Key concepts
- Encoding is column-type-aware (RLE makes no sense for random floats)
- Compression happens after encoding
- Metadata stores which encoding was used per column chunk

### Deliverables
```
encoding/
├── plain.go          # No-op encoding (baseline)
├── rle.go            # Run-length encoding
├── dictionary.go     # Dictionary encoding
├── delta.go          # Delta encoding for integers
├── lz4.go            # LZ4 block compression
├── encoder.go        # Interface + auto-selection heuristics
└── encoding_test.go  # Compression ratio benchmarks
```

### Success criteria
- Dictionary encoding: 10× compression on low-cardinality strings
- RLE: 50×+ compression on sorted repeated values
- Benchmark: encoding speed and compression ratio per type

---

## Phase 3: Vectorized Scan Engine (Weeks 5-6)

**Goal:** Execute scans over columnar data in batches, not row-by-row.

### What to build
- **Vector:** A batch of values (e.g., 1024 values) from one column
- **Scan operator:** Read column chunks into vectors
- **Filter:** Apply predicate (e.g., `age > 30`) on a vector, produce selection bitmap
- **Projection:** Select subset of columns

### Key concepts
- **Vectorized execution:** Process data in batches of ~1024 values. Tight loops over arrays → CPU SIMD friendly, branch prediction friendly
- **Selection vector:** A bitmap or index array marking which rows passed the filter. Downstream operators only process selected rows
- **Late materialization:** Don't reconstruct full rows until absolutely necessary

### Deliverables
```
engine/
├── vector.go         # Typed batch (1024 values + null bitmap + selection)
├── scan.go           # Read column chunks into vectors
├── filter.go         # Apply predicates, produce selection vectors
├── project.go        # Column projection
├── expression.go     # Simple expression evaluation (=, >, <, AND, OR)
└── engine_test.go    # Scan + filter + project pipeline tests
```

### Success criteria
- Scan 10M rows, filter on one column, project 3 columns
- Benchmark vs naive row-by-row scan (should be 5-10× faster)

---

## Phase 4: Aggregation (Weeks 7-8)

**Goal:** GROUP BY and aggregate functions.

### What to build
- **Hash aggregation:** GROUP BY using a hash table
- **Aggregate functions:** COUNT, SUM, AVG, MIN, MAX
- **Streaming aggregation:** For pre-sorted data (no hash table needed)

### Key concepts
- Hash table key = group-by column values
- Hash table value = running aggregate states
- Two-phase: accumulate → finalize

### Deliverables
```
engine/
├── aggregate.go      # Hash-based GROUP BY
├── functions.go      # COUNT, SUM, AVG, MIN, MAX
├── streaming_agg.go  # Sorted-data aggregation (optional optimization)
└── aggregate_test.go
```

### Success criteria
- `SELECT city, COUNT(*), AVG(price) FROM orders GROUP BY city`
- Benchmark on 10M rows

---

## Phase 5: SQL Parser & Planner (Weeks 9-10)

**Goal:** Parse SQL strings into query plans.

### What to build
- **Lexer:** SQL → tokens
- **Parser:** Tokens → AST (Abstract Syntax Tree)
- **Planner:** AST → logical plan (tree of operators)
- Supported SQL: SELECT, FROM, WHERE, GROUP BY, ORDER BY, LIMIT

### Key concepts
- Recursive descent parser (simplest approach)
- Logical plan = tree of: Scan → Filter → Project → Aggregate → Sort → Limit

### Deliverables
```
sql/
├── lexer.go          # SQL tokenizer
├── parser.go         # Recursive descent parser → AST
├── ast.go            # AST node types
├── planner.go        # AST → logical plan
├── plan.go           # Logical plan node types
└── sql_test.go       # Parse + plan + execute end-to-end tests
```

### Success criteria
- `SELECT name, SUM(amount) FROM sales WHERE year = 2024 GROUP BY name ORDER BY SUM(amount) DESC LIMIT 10`
- Parse → Plan → Execute → Correct result

---

## Phase 6: Query Optimizer (Weeks 11-12)

**Goal:** Make queries faster with basic optimization rules.

### What to build
- **Predicate pushdown:** Move filters closer to scan
- **Projection pushdown:** Only read needed columns
- **Zone maps / skip indexes:** Min/max per row group → skip entire chunks
- **Cost estimation:** Basic statistics (row count, distinct count)

### Deliverables
```
optimizer/
├── pushdown.go       # Predicate and projection pushdown
├── zonemap.go        # Min/max per column chunk for skip scanning
├── stats.go          # Basic column statistics
├── optimizer.go      # Rule-based optimizer
└── optimizer_test.go
```

### Success criteria
- Query with WHERE on sorted column skips 90% of row groups
- Explain plan shows pushdown applied

---

## Phase 7: Joins (Weeks 13-14)

**Goal:** Join two tables.

### What to build
- **Hash join:** Build hash table on smaller table, probe with larger
- **Sort-merge join:** For pre-sorted data (optional)

### Deliverables
```
engine/
├── join.go           # Hash join implementation
├── sort.go           # External sort for ORDER BY and sort-merge join
└── join_test.go
```

### Success criteria
- `SELECT o.*, c.name FROM orders o JOIN customers c ON o.customer_id = c.id`
- Benchmark on 1M × 100K join

---

## Phase 8: Distributed Query (Weeks 15-16, stretch goal)

**Goal:** Shard data across multiple nodes and execute distributed queries.

### What to build
- **Sharding:** Hash-partition tables across N nodes
- **Scatter-gather:** Send partial queries to shards, merge results
- **Distributed aggregation:** Partial aggregate → final aggregate

### Deliverables
```
distributed/
├── shard.go          # Hash partitioning
├── coordinator.go    # Query coordinator (scatter-gather)
├── node.go           # Worker node (execute partial query)
└── distributed_test.go
```

### Success criteria
- 3-node cluster, distributed GROUP BY, correct results
- Linear speedup on scan-heavy queries

---

## Timeline Summary

| Phase | Topic | Weeks | Dependencies |
|-------|-------|-------|--------------|
| 1 | Column Storage | 1-2 | None |
| 2 | Encoding & Compression | 3-4 | Phase 1 |
| 3 | Vectorized Scan | 5-6 | Phase 1 |
| 4 | Aggregation | 7-8 | Phase 3 |
| 5 | SQL Parser | 9-10 | Phase 3, 4 |
| 6 | Query Optimizer | 11-12 | Phase 5 |
| 7 | Joins | 13-14 | Phase 3, 5 |
| 8 | Distributed (stretch) | 15-16 | Phase 5, 6, 7 |

**Estimated total: 16 weeks at ~5 hours/week**

## Language Choice: Why Go

- Fast compilation → quick iteration
- No garbage collection surprises for this scale
- Great stdlib for networking (Phase 8)
- Simple enough to focus on DB concepts, not language complexity
- Your production language → directly applicable knowledge
