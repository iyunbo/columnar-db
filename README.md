# Columnar DB 🏗️

Building a column-oriented analytical database from scratch — inspired by ClickHouse & SingleStore.

## Why

The best way to understand distributed analytical databases is to build one. No abstractions, no frameworks — just the core mechanics of how modern OLAP engines work under the hood.

## Roadmap

| Phase | Topic | Status |
|-------|-------|--------|
| 1 | **Storage Engine** — Column-oriented storage format, compression, encoding | ⬜ |
| 2 | **Query Engine** — Vectorized execution, expression evaluation | ⬜ |
| 3 | **SQL Parser** — Basic SQL parsing and query planning | ⬜ |
| 4 | **Query Optimizer** — Cost-based optimization, predicate pushdown | ⬜ |
| 5 | **Indexing** — Sparse indexes, skip indexes, zone maps | ⬜ |
| 6 | **Aggregation** — Hash aggregation, streaming aggregation | ⬜ |
| 7 | **Joins** — Hash join, sort-merge join | ⬜ |
| 8 | **Distributed** — Sharding, replication, distributed query execution | ⬜ |

## Architecture

```
┌─────────────────────────────────────┐
│            SQL Interface            │
├─────────────────────────────────────┤
│  Parser → Planner → Optimizer       │
├─────────────────────────────────────┤
│       Vectorized Query Engine       │
├─────────────────────────────────────┤
│  Column Store │ Indexes │ Compression│
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
- **Vectorized execution** — Processing data in batches, not row-by-row
- **Compression** — Run-length encoding, dictionary encoding, delta encoding
- **Sparse indexing** — How ClickHouse skips irrelevant data blocks
- **Distributed execution** — Scatter-gather query patterns across shards

## Reference

- 📖 [ClickHouse Documentation](https://clickhouse.com/docs)
- 📖 [CMU Database Systems Course](https://15445.courses.cs.cmu.edu/)
- 📖 [Designing Data-Intensive Applications](https://dataintensive.net/) by Martin Kleppmann
- 💻 [ClickHouse Source](https://github.com/ClickHouse/ClickHouse)

## Author

**Yunbo WANG** — Staff Data Engineer | AI Agent Engineering | [LinkedIn](https://linkedin.com/in/yunbo-wang) | [Twitter](https://x.com/iyunbo)
