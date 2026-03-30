# Phase 1: Column Storage — Step by Step

Phase 1 is the foundation. We break it into 5 small, testable steps.

**Each step produces working code with tests. No step depends on finishing the whole phase.**

---

## Step 1: Type System + Column Values

**Goal:** Define column types and store values in typed arrays.

**What to build:**
- `ColumnType` enum: Int64, Float64, String, Bool
- `ColumnValues` interface: a typed slice of values
- Concrete types: `Int64Column`, `Float64Column`, `StringColumn`, `BoolColumn`
- Each has: `Append(value)`, `Get(i) value`, `Len() int`

**Files:**
```
storage/types.go     # ColumnType enum + ColumnValues interface
storage/int64.go     # Int64Column
storage/float64.go   # Float64Column
storage/string.go    # StringColumn
storage/bool.go      # BoolColumn
storage/types_test.go
```

**Test:** Create columns, append 1000 values, get them back, verify correctness.

**Time:** ~1 hour

---

## Step 2: Null Bitmap

**Goal:** Track which values are NULL using a bit-packed bitmap.

**What to build:**
- `NullBitmap`: a `[]byte` where each bit represents one row
- `SetNull(i)`, `IsNull(i) bool`, `NullCount() int`
- Bit-packing: 8 rows per byte, bit 0 = row 0, bit 7 = row 7

**Why bit-packed?** 1M rows = 125 KB bitmap. Using `[]bool` would be 1 MB (8× more).

**Files:**
```
storage/bitmap.go       # NullBitmap implementation
storage/bitmap_test.go
```

**Test:** Set random nulls in 10,000 positions, verify IsNull is correct for all.

**Time:** ~1 hour

---

## Step 3: Column Chunk

**Goal:** Combine values + null bitmap + metadata into a single unit.

**What to build:**
- `ColumnChunk` struct:
  - `Name string`
  - `Type ColumnType`
  - `Values ColumnValues`
  - `Nulls NullBitmap`
  - `Count int`
  - `NullCount int`
  - `Min, Max any` (for zone maps later)

**Files:**
```
storage/chunk.go       # ColumnChunk struct
storage/chunk_test.go
```

**Test:** Create a chunk with 1000 Int64 values + 50 nulls. Verify count, null count, min, max.

**Time:** ~30 minutes

---

## Step 4: Row Group

**Goal:** Group multiple column chunks together (one chunk per column, all with same row count).

**What to build:**
- `RowGroup` struct:
  - `Columns []ColumnChunk`
  - `RowCount int`
- `NewRowGroup(columns ...ColumnChunk) RowGroup` — validates all columns have same length
- Helper: `CreateFromRows(schema, rows)` — convert row-oriented data into columnar row group

**Why row groups?** Real data is billions of rows. We process them in chunks (e.g., 65536 rows). Each row group is independently readable — enables parallel reads and skip scanning later.

**Files:**
```
storage/rowgroup.go       # RowGroup struct
storage/rowgroup_test.go
```

**Test:** Create a row group with 5 columns × 10,000 rows. Verify structure. Try creating with mismatched lengths → should error.

**Time:** ~30 minutes

---

## Step 5: Binary File Format (Read + Write)

**Goal:** Serialize row groups to disk and read them back.

**What to build:**

File format layout:
```
┌──────────────────────┐
│ Magic: "COLDB\x01"   │  6 bytes
├──────────────────────┤
│ Row Group 1          │
│  ├─ Column 1 data    │  raw bytes
│  ├─ Column 1 nulls   │  bitmap bytes
│  ├─ Column 2 data    │
│  ├─ Column 2 nulls   │
│  └─ ...              │
├──────────────────────┤
│ Row Group 2          │
│  └─ ...              │
├──────────────────────┤
│ Footer               │
│  ├─ Schema           │  column names, types
│  ├─ Row group index  │  offsets, row counts
│  └─ Column stats     │  min, max, null count
├──────────────────────┤
│ Footer offset        │  8 bytes (int64, points to footer start)
├──────────────────────┤
│ Magic: "COLDB\x01"   │  6 bytes
└──────────────────────┘
```

Key design decisions:
- **Footer at the end** — Write data first, then metadata. Reader seeks to end, reads footer offset, then reads footer to know where everything is (same as Parquet)
- **Self-describing** — Schema is in the footer. No external schema file needed
- **Footer contains offsets** — Reader can jump to any row group or column without scanning

**Files:**
```
storage/writer.go         # Write row groups to file
storage/reader.go         # Read row groups from file (seek-based)
storage/format.go         # Constants, magic bytes, footer encoding
storage/writer_test.go
storage/reader_test.go
```

**Test:**
1. Create 3 row groups × 5 columns × 65536 rows = ~1M rows
2. Write to file
3. Read back, verify all values match
4. Print: file size vs equivalent CSV size
5. Read only column 2 of row group 1 (selective read)

**Time:** ~3 hours

---

## Summary

| Step | What | Time | Test |
|------|------|------|------|
| 1 | Type system + typed arrays | 1h | Append/get round-trip |
| 2 | Null bitmap (bit-packed) | 1h | Random null pattern |
| 3 | Column chunk (values + nulls + meta) | 30min | Count, min, max |
| 4 | Row group (multi-column container) | 30min | Validation |
| 5 | Binary file format (write + read) | 3h | 1M row round-trip |
| **Total** | | **~6 hours** | |

## Go Project Setup

Before Step 1:
```bash
cd /Users/verrerie/git/columnar-db
go mod init github.com/iyunbo/columnar-db
mkdir -p storage
```

## Ready?

Start with Step 1. Each step gets its own PR.
