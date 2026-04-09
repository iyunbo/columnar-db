# Columnar DB

A column-oriented analytical database built from scratch in Go — inspired by DuckDB & ClickHouse.

## Quick Start

```bash
go run ./cmd/columnar-db
```

```
columnar-db> SELECT city, COUNT(*), AVG(age) FROM people GROUP BY city ORDER BY city
city     | COUNT(*) | AVG(age)
---------+----------+---------
Beijing  | 166      | 47.82
Kunming  | 184      | 45.51
Lyon     | 156      | 44.48
Paris    | 165      | 44.72
Shanghai | 174      | 44.94
Tokyo    | 155      | 45.02
(6 rows)

columnar-db> SELECT name, age FROM people WHERE age > 60 ORDER BY age DESC LIMIT 5
name   | age
-------+----
Alice  | 69
Karen  | 69
Tina   | 69
Pete   | 69
Grace  | 69
(5 rows)
```

The REPL ships with a 1000-row demo dataset. Type `.schema` to see columns, `.help` for the SQL subset, `.quit` to exit.

## SQL Subset

```sql
SELECT { * | col [, ...] | agg(col) [, ...] }
FROM table
[WHERE col { = | != | < | <= | > | >= } literal]
[GROUP BY col [, col ...]]
[ORDER BY col [ASC | DESC]]
[LIMIT n]
```

Aggregates: `COUNT(*)`, `COUNT(col)`, `SUM`, `MIN`, `MAX`, `AVG` on Int64 and Float64 columns.

## Architecture

```
┌─────────────────────────────────────────┐
│  cmd/columnar-db  — Interactive REPL    │
├─────────────────────────────────────────┤
│  sql/  — Lexer → Parser → Planner      │
│    Tokenize · Parse · Plan · Execute    │
├─────────────────────────────────────────┤
│  exec/ — Vectorized Execution Engine    │
│    ScanOp · FilterOp · ProjectOp        │
│    AggregateOp · GroupByOp              │
│    OrderByOp · LimitOp                  │
│    9 Aggregators · 7 Predicates         │
├─────────────────────────────────────────┤
│  storage/ — In-Memory Columnar Types    │
│    RowGroup · ColumnChunk · NullBitmap  │
│  encoding/ — RLE · Dictionary · Delta   │
└─────────────────────────────────────────┘
```

## What Was Built (5 Phases)

| Phase | Topic | Outcome |
|-------|-------|---------|
| 1 | **Column Storage** — types, null bitmap, row groups, file format | Done |
| 2 | **Encoding** — RLE, dictionary, delta, LZ4 compression | Done |
| 3 | **Vectorized Scan** — batch processing, filters, selection vectors | Done (negative benchmark: ScanOp memmove tax) |
| 4 | **Aggregation** — hash GROUP BY, COUNT/SUM/AVG/MIN/MAX | Done (high-card 1.67x win, zero allocs) |
| 5 | **SQL Frontend** — lexer, parser, planner, REPL | Done (SQL overhead +0.3% on reuse path) |

### Key Benchmark Results

Phase 4 GROUP BY (1M rows, Apple M4):

| Cardinality | Naive | Vectorized | Speedup | Allocs |
|-------------|-------|------------|---------|--------|
| 10 groups (string) | 11.4 ms | 14.3 ms | 0.79x | 13 vs 0 |
| 10k groups (int64) | 14.6 ms | 8.8 ms | **1.67x** | 10033 vs 0 |
| 100k groups (int64) | 19.9 ms | 13.9 ms | **1.44x** | 100252 vs 6 |

Phase 5 SQL parity (Reset+drain path): **+0.3%** (low card) / **+3.8%** (high card) overhead vs direct operator calls.

## Key Concepts

- **Columnar storage** — contiguous typed arrays + null bitmaps, not row tuples
- **Vectorized execution** — process ~1024 values per batch in tight typed loops, not row-by-row
- **Late materialization** — selection vectors narrow live rows without copying data
- **Zero-alloc hot path** — per-batch operator Update/Finalize allocates nothing at steady state
- **Honest benchmarking** — negative results documented, not explained away ([Phase 3](docs/phase3-benchmark-results.md), [Phase 4](docs/phase4-benchmark.md))

## Documentation

| Doc | Content |
|-----|---------|
| [docs/plan.md](docs/plan.md) | Original 8-phase roadmap |
| [docs/phase4-benchmark.md](docs/phase4-benchmark.md) | GROUP BY benchmark + architecture decision |
| [docs/phase5-summary.md](docs/phase5-summary.md) | SQL frontend summary, parity benchmark, known limitations |
| [docs/architecture.html](docs/architecture.html) | Interactive SVG architecture diagram |

## Stats

- **~19K lines of Go** across 5 packages + CLI
- **474 tests** (storage, encoding, exec, sql)
- **Go 1.24+**, zero external dependencies for the core engine

## Author

**Yunbo WANG** — Staff Data Engineer | AI Agent Engineering | [LinkedIn](https://linkedin.com/in/yunbo-wang) | [Twitter](https://x.com/iyunbo)
