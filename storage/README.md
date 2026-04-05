# Phase 1: Storage Engine

## Goals
- Implement column-oriented storage format on disk
- Support types: Int64, Float64, String, Bool
- Null tracking via bit-packed bitmap
- Row groups for chunked storage
- Self-describing file format with footer

## Key Design Decisions

### Single File vs One-File-Per-Column

We chose **single file with all columns** (like Parquet/DuckDB), not one-file-per-column (like ClickHouse's MergeTree).

| | Single file (ours) | One file per column |
|---|---|---|
| Read single column | seek to offset, skip others | open file, sequential read |
| Read multiple columns | one open, multiple seeks | multiple opens, each sequential |
| Atomic writes | one file = atomic | multi-file consistency needed |
| File management | fewer files | 100 columns = 100 files |
| Concurrent writes | lock entire file | each column appends independently |
| Compression | per-column within file | per-column per file (same) |

**Why single file:**
1. **Simplicity** — learning project, minimize operational complexity
2. **Atomicity** — a file is either complete or not, no partial state
3. **Self-describing** — footer contains all metadata, no sidecar files needed
4. **Industry-proven** — Parquet, the most widely used columnar format, uses this model

**When one-file-per-column wins:**
- Real-time append workloads (each column can be appended independently)
- Very wide tables (hundreds of columns, queries touch few)
- Independent per-column compression tuning

**Key insight:** With footer-based offset+size per column chunk, single-file reads can skip unneeded columns via seek — effective read performance is comparable to one-file-per-column.

### Null Handling

Bit-packed null bitmap. One bit per row. 0 = not null, 1 = null. Stored alongside each column chunk. This matches Parquet and Arrow conventions.

### Row Groups

Data is split into row groups (target: 65536 rows). Each row group contains one chunk per column. Benefits:
- Parallel reads (different goroutines read different row groups)
- Skip scanning (check row group stats → skip groups that don't match)
- Memory management (process one row group at a time)

## File Format

```
┌──────────────────────────┐
│  Magic "CLDB" (4B)       │
│  Version (2B)            │
├──────────────────────────┤
│  Row Group 0             │
│  ├─ Column Chunk 0       │  values + null bitmap
│  ├─ Column Chunk 1       │
│  └─ ...                  │
├──────────────────────────┤
│  Row Group 1             │
│  └─ ...                  │
├──────────────────────────┤
│  Footer                  │
│  ├─ Schema               │  column names + types
│  ├─ Row Group Index      │  offset + size per chunk
│  ├─ Column Stats         │  min/max/null count
│  └─ Footer Length (4B)   │
├──────────────────────────┤
│  Magic "CLDB" (4B)       │  repeated for integrity check
└──────────────────────────┘
```

Read path: read last 8 bytes → get footer length + verify magic → read footer → get chunk offsets → seek to needed columns only.

## Completed

- [x] types.go — Column types (Int64, Float64, String, Bool)
- [x] bitmap.go — Bit-packed null bitmap
- [x] chunk.go — Column chunk with stats
- [x] rowgroup.go — Row group (collection of chunks)
- [x] Tests for all above

## Remaining

- [ ] format.go — File format structs and constants
- [ ] writer.go — Write row groups to disk
- [ ] reader.go — Read row groups from disk
- [ ] Round-trip test: write 1M rows → read back → verify
