# Sample CLDB File: Binary Layout

Minimal test case for the columnar-db V1 file format.

## Test Data

| id (Int64) | name (String) |
|------------|---------------|
| 1          | "alice"       |
| 2          | NULL          |
| 3          | "charlie"     |

2 columns, 3 rows, 1 row group. Column "name" has 1 null at row 1.

## Complete Binary Layout

Total file size: **164 bytes**

### Header (6 bytes)

| Offset | Size | Field          | Hex                 | Value       |
|--------|------|----------------|---------------------|-------------|
| 0      | 4    | Magic          | `43 4C 44 42`       | "CLDB"      |
| 4      | 2    | Version (u16)  | `01 00`             | 1           |

### Row Group 0, Column Chunk 0: "id" (Int64)

Offset 6, Size 25 bytes. Layout: 3 x int64 LE + 1 byte null bitmap.

| Offset | Size | Field             | Hex                         | Value |
|--------|------|-------------------|-----------------------------|-------|
| 6      | 8    | id[0] (int64 LE)  | `01 00 00 00 00 00 00 00`   | 1     |
| 14     | 8    | id[1] (int64 LE)  | `02 00 00 00 00 00 00 00`   | 2     |
| 22     | 8    | id[2] (int64 LE)  | `03 00 00 00 00 00 00 00`   | 3     |
| 30     | 1    | null bitmap       | `00`                        | no nulls (bits: 000) |

### Row Group 0, Column Chunk 1: "name" (String)

Offset 31, Size 33 bytes. Layout: offset table + string data + null bitmap.

String encoding: `num_offsets (u32)` + `offsets[0..N] (u32 each)` + `string data` + `null bitmap`.
Offsets are relative to the start of string data. `offsets[i+1] - offsets[i]` = length of string i.
Null strings have zero length (offset[i] == offset[i+1]).

Strings: "alice" (5 bytes), "" (null placeholder, 0 bytes), "charlie" (7 bytes).
Offsets: [0, 5, 5, 12]. String data: "alicecharlie" (12 bytes).

| Offset | Size | Field               | Hex                         | Value            |
|--------|------|---------------------|-----------------------------|------------------|
| 31     | 4    | num_offsets (u32)   | `04 00 00 00`               | 4                |
| 35     | 4    | offsets[0] (u32)    | `00 00 00 00`               | 0                |
| 39     | 4    | offsets[1] (u32)    | `05 00 00 00`               | 5                |
| 43     | 4    | offsets[2] (u32)    | `05 00 00 00`               | 5                |
| 47     | 4    | offsets[3] (u32)    | `0C 00 00 00`               | 12               |
| 51     | 5    | "alice"             | `61 6C 69 63 65`            | "alice"          |
| 56     | 7    | "charlie"           | `63 68 61 72 6C 69 65`      | "charlie"        |
| 63     | 1    | null bitmap         | `02`                        | row 1 null (bits: 010) |

### Footer (92 bytes, offset 64-155)

#### Schema

| Offset | Size | Field              | Hex                    | Value     |
|--------|------|--------------------|------------------------|-----------|
| 64     | 2    | num_columns (u16)  | `02 00`                | 2         |
| 66     | 2    | name_len (u16)     | `02 00`                | 2         |
| 68     | 2    | column name        | `69 64`                | "id"      |
| 70     | 1    | col_type (u8)      | `00`                   | TypeInt64 |
| 71     | 2    | name_len (u16)     | `04 00`                | 4         |
| 73     | 4    | column name        | `6E 61 6D 65`          | "name"    |
| 77     | 1    | col_type (u8)      | `02`                   | TypeString|

#### Row Group Index

| Offset | Size | Field                | Hex                              | Value     |
|--------|------|----------------------|----------------------------------|-----------|
| 78     | 4    | num_row_groups (u32) | `01 00 00 00`                    | 1         |
| 82     | 4    | row_count (u32)      | `03 00 00 00`                    | 3         |

#### Row Group 0, Column 0 ("id") Metadata

| Offset | Size | Field              | Hex                              | Value     |
|--------|------|--------------------|----------------------------------|-----------|
| 86     | 8    | offset (u64)       | `06 00 00 00 00 00 00 00`        | 6         |
| 94     | 4    | size (u32)         | `19 00 00 00`                    | 25        |
| 98     | 4    | null_count (u32)   | `00 00 00 00`                    | 0         |
| 102    | 1    | has_min_max (u8)   | `01`                             | true      |
| 103    | 8    | min (int64 LE)     | `01 00 00 00 00 00 00 00`        | 1         |
| 111    | 8    | max (int64 LE)     | `03 00 00 00 00 00 00 00`        | 3         |

#### Row Group 0, Column 1 ("name") Metadata

| Offset | Size | Field              | Hex                              | Value     |
|--------|------|--------------------|----------------------------------|-----------|
| 119    | 8    | offset (u64)       | `1F 00 00 00 00 00 00 00`        | 31        |
| 127    | 4    | size (u32)         | `21 00 00 00`                    | 33        |
| 131    | 4    | null_count (u32)   | `01 00 00 00`                    | 1         |
| 135    | 1    | has_min_max (u8)   | `01`                             | true      |
| 136    | 4    | min_len (u32)      | `05 00 00 00`                    | 5         |
| 140    | 5    | min value          | `61 6C 69 63 65`                 | "alice"   |
| 145    | 4    | max_len (u32)      | `07 00 00 00`                    | 7         |
| 149    | 7    | max value          | `63 68 61 72 6C 69 65`           | "charlie" |

#### Footer Length + Trailing Magic

| Offset | Size | Field              | Hex                              | Value     |
|--------|------|--------------------|----------------------------------|-----------|
| 156    | 4    | footer_length (u32)| `5C 00 00 00`                    | 92        |
| 160    | 4    | Magic              | `43 4C 44 42`                    | "CLDB"    |

## Verification Checksums

- Footer starts at offset 64, ends at offset 155 (inclusive). Length = 92 bytes.
- footer_length (92 = 0x5C) counts bytes from footer start (64) to end of footer (155), exclusive of the footer_length field itself.
- Read path: read bytes [160..163] = "CLDB" (verify magic), read bytes [156..159] = 92 (footer length), seek to offset 64 (= 156 - 92), read 92 bytes of footer.
- Total file size: 164 bytes.
