# Columnar DB 🏗️

Building a column-oriented analytical database from scratch in Go — inspired by ClickHouse, DuckDB & SingleStore.

## Why

The best way to understand distributed analytical databases is to build one. No abstractions, no frameworks — just the core mechanics of how modern OLAP engines work under the hood.

## Roadmap

| Phase | Topic | Status |
|-------|-------|--------|
| 1 | **Column Storage** — Column-oriented file format, types, null bitmap, row groups | ⬜ |
| 2 | **Encoding & Compression** — RLE, dictionary, delta encoding, LZ4 | ⬜ |
| 3 | **Vectorized Scan** — Batch processing, filters, projections, selection vectors | ⬜ |
| 4 | **Aggregation** — Hash GROUP BY, COUNT/SUM/AVG/MIN/MAX | ⬜ |
| 5 | **SQL Parser** — Lexer, recursive descent parser, query planner | ⬜ |
| 6 | **Query Optimizer** — Predicate pushdown, projection pushdown, zone maps | ⬜ |
| 7 | **Joins** — Hash join, sort-merge join | ⬜ |
| 8 | **Distributed** — Sharding, scatter-gather, distributed aggregation | ⬜ |

See [docs/plan.md](docs/plan.md) for detailed phase breakdowns and deliverables.

## Architecture

```
┌─────────────────────────────────────┐
│            SQL Interface            │
├─────────────────────────────────────┤
│  Parser → Planner → Optimizer       │
├─────────────────────────────────────┤
│       Vectorized Query Engine       │
│    (vectors, filters, aggregates)   │
├─────────────────────────────────────┤
│  Column Store │ Encoding │ Zone Maps │
├─────────────────────────────────────┤
│         Storage Engine (Disk)       │
└─────────────────────────────────────┘
```

## Tech Stack

- **Language**: Go
- **Storage**: Custom column format on disk
- **No dependencies** on existing DB engines — everything from scratch

## Key Concepts Explored

- **Columnar storage** — Why columns beat rows for analytics
- **Vectorized execution** — Processing data in batches of 1024, not row-by-row
- **Encoding** — RLE, dictionary, delta — column-type-aware compression
- **Zone maps** — Min/max per chunk → skip irrelevant data blocks
- **Distributed execution** — Scatter-gather query patterns across shards

## Documentation

- [Detailed Plan](docs/plan.md) — 8-phase breakdown with deliverables and success criteria
- [References](docs/references.md) — Papers, courses, code references, phase-specific reading order

## Author

**Yunbo WANG** — Staff Data Engineer | AI Agent Engineering | [LinkedIn](https://linkedin.com/in/yunbo-wang) | [Twitter](https://x.com/iyunbo)
