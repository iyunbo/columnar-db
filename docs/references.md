# References & Learning Materials

## Theory

### Must Read

| Resource | What You Learn | Phase |
|----------|---------------|-------|
| [CMU 15-721: Advanced Database Systems](https://15721.courses.cs.cmu.edu/) | Column stores, vectorized execution, query compilation. Andy Pavlo's course — the gold standard. Watch lectures 03-06 for storage, 07-09 for execution | All |
| [The Design and Implementation of Modern Column-Oriented Database Systems](https://stratos.seas.harvard.edu/files/stratos/files/columnstoresfntdbs.pdf) | Foundational paper on column stores. Covers C-Store, MonetDB, Vertica | 1-3 |
| [MonetDB/X100: Hyper-Pipelining Query Execution](https://www.cidrdb.org/cidr2005/papers/P19.pdf) | Why vectorized execution beats row-at-a-time. The paper that influenced ClickHouse and DuckDB | 3 |

### Recommended

| Resource | What You Learn | Phase |
|----------|---------------|-------|
| [Designing Data-Intensive Applications](https://dataintensive.net/) (Martin Kleppmann) | Chapter 3: Storage & Retrieval. Covers B-Trees, LSM, column stores | 1-2 |
| [C-Store: A Column-oriented DBMS](https://vldb.org/conf/2005/papers/p553-stonebraker.pdf) | Original column store paper by Stonebraker. Read sections on projections and join indexes | 1, 7 |
| [Integrating Compression and Execution in Column-Oriented Database Systems](https://www.cs.duke.edu/courses/cps216/fall12/Papers/compression.pdf) | How encoding/compression integrates with query execution — compress once, decompress lazily | 2-3 |

## Column Store Internals

### Storage Format

| Resource | Notes |
|----------|-------|
| [Apache Parquet Format Spec](https://parquet.apache.org/docs/file-format/) | Industry standard columnar file format. Study its row group + column chunk + page structure — our format will be similar but simplified |
| [Apache Arrow Columnar Format](https://arrow.apache.org/docs/format/Columnar.html) | In-memory columnar format. Focus on the null bitmap design and type system |
| [ClickHouse MergeTree Engine](https://clickhouse.com/docs/en/engines/table-engines/mergetree-family/mergetree) | How ClickHouse organizes data parts, granules, and sparse indexes |

### Encoding & Compression

| Resource | Notes |
|----------|-------|
| [Run-Length Encoding explained](https://en.wikipedia.org/wiki/Run-length_encoding) | Simplest encoding, great for sorted columns |
| [Dictionary Encoding in Parquet](https://parquet.apache.org/docs/file-format/data-pages/encodings/#dictionary-encoding-plain_dictionary--2-and-rle_dictionary--8) | How Parquet does dictionary encoding with RLE for the index array |
| [Delta Encoding](https://en.wikipedia.org/wiki/Delta_encoding) | For monotonically increasing values (timestamps, IDs) |
| [LZ4 spec](https://github.com/lz4/lz4/blob/dev/doc/lz4_Block_format.md) | Block compression. Use Go's `github.com/pierrec/lz4` |

### Vectorized Execution

| Resource | Notes |
|----------|-------|
| [DuckDB Internals (blog series)](https://duckdb.org/2021/05/14/selection-pushdown.html) | How DuckDB implements vectorized execution with selection vectors |
| [Morsel-Driven Parallelism](https://db.in.tum.de/~leis/papers/morsels.pdf) | How to parallelize vectorized execution across cores (advanced, Phase 3+) |
| [Everything You Always Wanted to Know About Compiled and Vectorized Queries](https://www.vldb.org/pvldb/vol11/p2209-kersten.pdf) | Compiled vs vectorized execution — we're doing vectorized |

## Code References

### Study These Source Codes

| Project | Language | What to Study |
|---------|----------|---------------|
| [DuckDB](https://github.com/duckdb/duckdb) | C++ | **Top priority.** Cleanest OLAP codebase. Look at `src/storage/` for column segments, `src/execution/` for vectorized operators |
| [ClickHouse](https://github.com/ClickHouse/ClickHouse) | C++ | Production-grade. Look at `src/Columns/` for column types, `src/Processors/` for query pipeline |
| [Databend](https://github.com/datafuselabs/databend) | Rust | Modern cloud OLAP. Look at `src/query/storages/` and `src/query/expression/` |
| [go-mysql-server](https://github.com/dolthub/go-mysql-server) | Go | SQL parser + execution engine in Go — good reference for Phase 5 |

### Go-Specific

| Resource | Notes |
|----------|-------|
| [Build Your Own DB in Go](https://build-your-own.org/database/) | B+Tree, KV store, disk IO patterns. Not columnar but Go storage fundamentals |
| [Go binary encoding](https://pkg.go.dev/encoding/binary) | How to read/write binary data in Go efficiently |
| [mmap in Go](https://pkg.go.dev/golang.org/x/exp/mmap) | Memory-mapped file IO for fast reads |

## Phase-Specific Reading Order

### Before Phase 1 (start here)
1. DDIA Chapter 3 (Storage & Retrieval) — 2 hours
2. Apache Parquet format spec (skim) — 1 hour
3. CMU 15-721 Lecture 03 (Storage Models) — 1.5 hours
4. DuckDB `src/storage/column_segment.cpp` — 1 hour

### Before Phase 2
1. Parquet encoding spec — 1 hour
2. "Integrating Compression and Execution" paper — 2 hours
3. ClickHouse `src/Compression/` — 1 hour

### Before Phase 3
1. MonetDB/X100 paper — 2 hours
2. CMU 15-721 Lectures 07-08 (Query Execution) — 3 hours
3. DuckDB `src/execution/` — 2 hours

### Before Phase 5
1. [Writing a SQL parser from scratch](https://tomassetti.me/guide-parsing-algorithms-terminology/) — 1 hour
2. go-mysql-server parser source — 2 hours

### Before Phase 6
1. CMU 15-721 Lecture 12 (Query Optimization) — 1.5 hours
2. ClickHouse MergeTree docs (skip indexes / granules) — 1 hour
