package storage

// format.go defines the on-disk file format for columnar-db (Format V1).
//
// File layout:
//
//	+----------------------------+
//	| Magic "CLDB" (4 bytes)     |  File identification
//	| Version (2 bytes, LE)      |  Format version (currently 1)
//	+----------------------------+
//	| Row Group 0                |
//	|   Column Chunk 0           |  Typed values + null bitmap
//	|   Column Chunk 1           |
//	|   ...                      |
//	+----------------------------+
//	| Row Group 1                |
//	|   ...                      |
//	+----------------------------+
//	| Footer (variable length)   |
//	|   Schema                   |  Column names + types
//	|   Row Group Index          |  Per-chunk: offset, size, stats
//	|   Footer Length (4B, LE)   |  Size of footer excluding this field
//	+----------------------------+
//	| Magic "CLDB" (4 bytes)     |  Repeated for integrity check
//	+----------------------------+
//
// Read path: read last 8 bytes -> verify trailing magic -> get footer length
// -> seek back and read footer -> get chunk offsets -> seek to needed columns.
//
// Column Chunk binary layout (per chunk):
//
//	Int64:   [v0 int64 LE] [v1 int64 LE] ... [vN int64 LE] [null bitmap bytes]
//	Float64: [v0 float64 LE] [v1 float64 LE] ... [null bitmap bytes]
//	Bool:    [v0 byte] [v1 byte] ... [null bitmap bytes]  (1 byte per bool: 0x00/0x01)
//	String:  [num_offsets uint32 LE] [off0 uint32 LE] ... [offN uint32 LE] [string data bytes] [null bitmap bytes]
//	         num_offsets = num_values + 1; offsets are relative to start of string data.
//	         off[i+1] - off[i] = length of string i. Null strings have length 0.
//
// The null bitmap is always the last ceil(num_values/8) bytes of a chunk.
// Bit layout: byte[i/8] & (1 << (i%8)) != 0 means row i is null.
//
// Footer binary layout:
//
//	[num_columns uint16 LE]
//	For each column:
//	  [name_len uint16 LE] [name bytes] [col_type uint8]
//	[num_row_groups uint32 LE]
//	For each row group:
//	  [row_count uint32 LE]
//	  For each column:
//	    [offset uint64 LE] [size uint32 LE]
//	    [null_count uint32 LE]
//	    [has_min_max uint8]       (0 = no stats, 1 = has stats)
//	    if has_min_max == 1:
//	      For Int64/Float64/Bool: [min 8 bytes LE] [max 8 bytes LE]
//	      For String: [min_len uint32 LE] [min bytes] [max_len uint32 LE] [max bytes]
//	[footer_length uint32 LE]    (bytes from start of footer to here, exclusive)
//
// Design note: the interfaces (FormatWriter/FormatReader) are deliberately
// narrow. They support the current single-file V1 format, but a future V2
// (one file per column) can implement the same interfaces with different
// backing storage. Callers that program to the interface won't need changes.

const (
	// MagicBytes identifies a columnar-db file. Appears at byte 0 and at
	// the very end of the file for integrity verification.
	MagicBytes = "CLDB"

	// FormatVersion is the current on-disk format version.
	// Stored as uint16 LE at byte offset 4.
	FormatVersion uint16 = 1

	// DefaultRowGroupSize is the target number of rows per row group.
	// Writers split input data into groups of this size.
	DefaultRowGroupSize = 65536

	// HeaderSize is the fixed size of the file header: 4 (magic) + 2 (version).
	HeaderSize = 6

	// MagicSize is the size of the magic bytes.
	MagicSize = 4

	// FooterLengthSize is the size of the footer length field (uint32).
	FooterLengthSize = 4

	// TrailerSize is the fixed overhead at the end: footer length (4) + trailing magic (4).
	TrailerSize = FooterLengthSize + MagicSize
)

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// FormatWriter writes columnar data to the on-disk format.
//
// Usage:
//
//	w := NewFileWriter(f, schema)   // (future: writer.go)
//	w.WriteRowGroup(rg1)
//	w.WriteRowGroup(rg2)
//	w.Close()                       // writes footer + trailing magic
//
// Close must be called to finalize the file. A file without its footer
// and trailing magic is invalid.
type FormatWriter interface {
	// WriteRowGroup serializes a row group and appends it to the file.
	// Row groups must all share the same schema.
	WriteRowGroup(rg *RowGroup) error

	// Close writes the footer (schema + row group index + stats),
	// footer length, and trailing magic, then closes the underlying writer.
	Close() error
}

// FormatReader reads columnar data from the on-disk format.
//
// Usage:
//
//	r := OpenFileReader(f)          // (future: reader.go)
//	schema := r.Schema()
//	for i := 0; i < r.NumRowGroups(); i++ {
//	    rg, _ := r.ReadRowGroup(i)
//	    // process rg
//	}
//	r.Close()
type FormatReader interface {
	// Schema returns the file's schema (column names and types).
	Schema() Schema

	// NumRowGroups returns how many row groups the file contains.
	NumRowGroups() int

	// ReadRowGroup reads and deserializes the row group at the given index.
	// Index is 0-based. Returns an error if index is out of range.
	ReadRowGroup(index int) (*RowGroup, error)

	// Close releases resources held by the reader.
	Close() error
}

// ---------------------------------------------------------------------------
// File metadata (in-memory representation of the footer)
// ---------------------------------------------------------------------------

// FileMetadata is the in-memory representation of a file's footer.
// It holds the schema and per-row-group index information needed to
// locate and read individual column chunks without scanning the file.
type FileMetadata struct {
	// Version is the format version read from the file header.
	Version uint16

	// Schema describes column names and types, shared by all row groups.
	Schema Schema

	// RowGroups holds metadata for each row group in file order.
	RowGroups []RowGroupMeta
}

// RowGroupMeta describes one row group's location and contents within the file.
type RowGroupMeta struct {
	// RowCount is the number of rows in this row group.
	RowCount int

	// Columns holds per-column-chunk metadata, one entry per schema column,
	// in the same order as FileMetadata.Schema.
	Columns []ColumnChunkMeta
}

// ColumnChunkMeta locates a single column chunk within the file and
// carries its summary statistics (zone map data for skip scanning).
type ColumnChunkMeta struct {
	// Offset is the byte offset from the start of the file where this
	// column chunk's data begins.
	Offset int64

	// Size is the total byte size of the column chunk on disk, including
	// both the typed values and the trailing null bitmap.
	Size int

	// NullCount is the number of NULL rows in this chunk.
	NullCount int

	// Min is the minimum non-null value, or nil if all values are null.
	// Type matches the column's ColumnType:
	//   TypeInt64   -> int64
	//   TypeFloat64 -> float64
	//   TypeString  -> string
	//   TypeBool    -> bool
	Min any

	// Max is the maximum non-null value, or nil if all values are null.
	Max any
}

// TotalRows returns the total number of rows across all row groups.
func (m *FileMetadata) TotalRows() int {
	total := 0
	for _, rg := range m.RowGroups {
		total += rg.RowCount
	}
	return total
}
