package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// SingleFileWriter writes columnar data to a single .cldb file.
//
// It implements FormatWriter: callers write row groups one at a time,
// then call Close to append the footer and trailing magic. A file is
// invalid until Close completes — readers verify the trailing magic
// before trusting any content.
//
// Write order:
//  1. NewSingleFileWriter — creates file, writes header (magic + version)
//  2. WriteRowGroup (0..N times) — appends column chunks, records metadata
//  3. Close — writes footer (schema + row group index + stats + footer length)
//     + trailing magic, then closes the file
type SingleFileWriter struct {
	file     *os.File
	metadata FileMetadata
	offset   uint64 // current byte position in the file
}

// NewSingleFileWriter creates a new .cldb file at path and writes the
// file header (4-byte magic "CLDB" + 2-byte LE version).
//
// The caller must eventually call Close to finalize the file.
func NewSingleFileWriter(path string) (*SingleFileWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	w := &SingleFileWriter{
		file: f,
		metadata: FileMetadata{
			Version: FormatVersion,
		},
	}

	// Write header: magic bytes identify the file type, version allows
	// future format evolution without breaking older readers.
	if err := w.writeBytes([]byte(MagicBytes)); err != nil {
		f.Close()
		return nil, fmt.Errorf("write magic: %w", err)
	}
	if err := w.writeUint16(FormatVersion); err != nil {
		f.Close()
		return nil, fmt.Errorf("write version: %w", err)
	}

	return w, nil
}

// WriteRowGroup serializes a row group (all its column chunks) and appends
// them to the file. Each chunk is written as typed values followed by the
// null bitmap. Metadata (offsets, sizes, stats) is recorded in memory and
// written to the footer when Close is called.
//
// The schema is captured from the first row group. Subsequent row groups
// must have the same schema (same column count, names, and types).
func (w *SingleFileWriter) WriteRowGroup(rg *RowGroup) error {
	schema := rg.Schema()

	// Capture schema from the first row group.
	if len(w.metadata.RowGroups) == 0 {
		w.metadata.Schema = schema
	} else {
		// Validate that this row group matches the established schema.
		if err := w.validateSchema(schema); err != nil {
			return err
		}
	}

	rgMeta := RowGroupMeta{
		RowCount: rg.RowCount,
		Columns:  make([]ColumnChunkMeta, rg.NumColumns()),
	}

	for i, chunk := range rg.Columns {
		chunkStart := w.offset

		// Encode typed values — each type has its own binary layout
		// optimized for sequential reads and cache friendliness.
		if err := w.writeColumnValues(chunk); err != nil {
			return fmt.Errorf("write column %q values: %w", chunk.Name, err)
		}

		// Null bitmap always follows the values. Its position is implicit:
		// readers know it starts at (chunk offset + values size) and has
		// ceil(row_count / 8) bytes.
		if err := w.writeBytes(chunk.Nulls.Bytes()); err != nil {
			return fmt.Errorf("write column %q null bitmap: %w", chunk.Name, err)
		}

		chunkSize := w.offset - chunkStart
		stats := chunk.Stats()

		rgMeta.Columns[i] = ColumnChunkMeta{
			Offset:    int64(chunkStart),
			Size:      int(chunkSize),
			NullCount: stats.NullCount,
			Min:       stats.Min,
			Max:       stats.Max,
		}
	}

	w.metadata.RowGroups = append(w.metadata.RowGroups, rgMeta)
	return nil
}

// Close writes the footer, footer length, and trailing magic, then closes
// the underlying file. The footer makes the file self-describing: a reader
// can open the file, read the last 8 bytes, and navigate to any column
// chunk without scanning the entire file.
func (w *SingleFileWriter) Close() error {
	footerStart := w.offset

	// --- Schema section ---
	// Readers need column names and types to interpret chunk data.
	if err := w.writeSchema(); err != nil {
		return fmt.Errorf("write schema: %w", err)
	}

	// --- Row group index ---
	// Per-chunk offset/size lets readers seek directly to needed columns.
	// Stats (min/max/null_count) enable skip scanning: entire row groups
	// can be skipped if their value range doesn't match a query predicate.
	if err := w.writeRowGroupIndex(); err != nil {
		return fmt.Errorf("write row group index: %w", err)
	}

	// Footer length: bytes from footer start to here (exclusive of this
	// 4-byte field itself). Readers use this to seek backwards from EOF.
	footerLen := uint32(w.offset - footerStart)
	if err := w.writeUint32(footerLen); err != nil {
		return fmt.Errorf("write footer length: %w", err)
	}

	// Trailing magic: duplicated at EOF so readers can verify file
	// integrity with a single 8-byte read from the end.
	if err := w.writeBytes([]byte(MagicBytes)); err != nil {
		return fmt.Errorf("write trailing magic: %w", err)
	}

	return w.file.Close()
}

// ---------------------------------------------------------------------------
// Column value encoding
// ---------------------------------------------------------------------------

// writeColumnValues dispatches to the type-specific encoder.
// Each encoder writes values in a format that can be memory-mapped or
// read sequentially with minimal decoding overhead.
func (w *SingleFileWriter) writeColumnValues(chunk *ColumnChunk) error {
	switch col := chunk.Values.(type) {
	case *Int64Column:
		return w.writeInt64Values(col)
	case *Float64Column:
		return w.writeFloat64Values(col)
	case *BoolColumn:
		return w.writeBoolValues(col)
	case *StringColumn:
		return w.writeStringValues(col)
	default:
		return fmt.Errorf("unsupported column type: %T", chunk.Values)
	}
}

// writeInt64Values writes each int64 as 8 bytes LE.
// Fixed-width encoding: simple, no framing needed, offset = index * 8.
func (w *SingleFileWriter) writeInt64Values(col *Int64Column) error {
	for i := 0; i < col.Len(); i++ {
		if err := w.writeInt64(col.Get(i)); err != nil {
			return err
		}
	}
	return nil
}

// writeFloat64Values writes each float64 as 8 bytes LE (IEEE 754).
func (w *SingleFileWriter) writeFloat64Values(col *Float64Column) error {
	for i := 0; i < col.Len(); i++ {
		if err := w.writeFloat64(col.Get(i)); err != nil {
			return err
		}
	}
	return nil
}

// writeBoolValues writes each bool as 1 byte (0x00 = false, 0x01 = true).
// Not bit-packed here — keeps random access simple at the cost of 8x space.
// The null bitmap IS bit-packed since it's only read in bulk.
func (w *SingleFileWriter) writeBoolValues(col *BoolColumn) error {
	for i := 0; i < col.Len(); i++ {
		var b byte
		if col.Get(i) {
			b = 0x01
		}
		if err := w.writeBytes([]byte{b}); err != nil {
			return err
		}
	}
	return nil
}

// writeStringValues uses the offset-table encoding from the format spec:
//
//	[num_offsets uint32 LE] [off0 .. offN uint32 LE] [concatenated string data]
//
// num_offsets = num_values + 1. Offsets are relative to the start of string
// data. off[i+1] - off[i] = byte length of string i. Null strings have
// length 0 (off[i] == off[i+1]), which is indistinguishable from empty
// strings in the value layer — the null bitmap disambiguates.
func (w *SingleFileWriter) writeStringValues(col *StringColumn) error {
	n := col.Len()
	numOffsets := uint32(n + 1)

	// Build offset table and concatenated data in one pass.
	offsets := make([]uint32, numOffsets)
	var dataLen uint32
	for i := range n {
		offsets[i] = dataLen
		dataLen += uint32(len(col.Get(i)))
	}
	offsets[n] = dataLen

	// Write num_offsets so readers know how many offset entries to expect.
	if err := w.writeUint32(numOffsets); err != nil {
		return err
	}

	// Write offset table.
	for _, off := range offsets {
		if err := w.writeUint32(off); err != nil {
			return err
		}
	}

	// Write concatenated string bytes.
	for i := range n {
		s := col.Get(i)
		if len(s) > 0 {
			if err := w.writeBytes([]byte(s)); err != nil {
				return err
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Footer encoding
// ---------------------------------------------------------------------------

// writeSchema encodes column names and types so readers can reconstruct
// the table structure without external metadata.
func (w *SingleFileWriter) writeSchema() error {
	schema := w.metadata.Schema

	// num_columns: upper bound is 65535 — more than enough for analytics.
	if err := w.writeUint16(uint16(len(schema.Names))); err != nil {
		return err
	}

	for i := range schema.Names {
		nameBytes := []byte(schema.Names[i])
		if err := w.writeUint16(uint16(len(nameBytes))); err != nil {
			return err
		}
		if err := w.writeBytes(nameBytes); err != nil {
			return err
		}
		// ColumnType is stored as a single byte. The iota values in types.go
		// (0=Int64, 1=Float64, 2=String, 3=Bool) are the on-disk encoding.
		if err := w.writeBytes([]byte{byte(schema.Types[i])}); err != nil {
			return err
		}
	}

	return nil
}

// writeRowGroupIndex writes per-row-group metadata: row count, then
// per-column offset/size/stats. This is the "index" that makes seeks fast.
func (w *SingleFileWriter) writeRowGroupIndex() error {
	if err := w.writeUint32(uint32(len(w.metadata.RowGroups))); err != nil {
		return err
	}

	for _, rgMeta := range w.metadata.RowGroups {
		if err := w.writeUint32(uint32(rgMeta.RowCount)); err != nil {
			return err
		}

		for _, colMeta := range rgMeta.Columns {
			if err := w.writeColumnChunkMeta(colMeta); err != nil {
				return err
			}
		}
	}

	return nil
}

// writeColumnChunkMeta encodes one column chunk's location and statistics.
func (w *SingleFileWriter) writeColumnChunkMeta(meta ColumnChunkMeta) error {
	// Offset (uint64): where this chunk starts in the file.
	if err := w.writeUint64(uint64(meta.Offset)); err != nil {
		return err
	}
	// Size (uint32): total bytes of values + null bitmap.
	if err := w.writeUint32(uint32(meta.Size)); err != nil {
		return err
	}
	// Null count (uint32): how many rows are NULL — useful for quick
	// "is this column all-null?" checks without reading the bitmap.
	if err := w.writeUint32(uint32(meta.NullCount)); err != nil {
		return err
	}

	// Min/Max stats for zone-map skip scanning.
	if meta.Min == nil || meta.Max == nil {
		// All values null — no meaningful stats to write.
		if err := w.writeBytes([]byte{0x00}); err != nil {
			return err
		}
	} else {
		if err := w.writeBytes([]byte{0x01}); err != nil {
			return err
		}
		if err := w.writeStatValue(meta.Min); err != nil {
			return fmt.Errorf("write min stat: %w", err)
		}
		if err := w.writeStatValue(meta.Max); err != nil {
			return fmt.Errorf("write max stat: %w", err)
		}
	}

	return nil
}

// writeStatValue encodes a min or max value according to its type.
// The encoding must match what the reader expects for each ColumnType.
func (w *SingleFileWriter) writeStatValue(v any) error {
	switch val := v.(type) {
	case int64:
		return w.writeInt64(val)
	case float64:
		return w.writeFloat64(val)
	case bool:
		// Bool stats use 8 bytes LE to match the "8 bytes LE" rule for
		// Int64/Float64/Bool in the footer spec. Encoding as uint64
		// keeps the footer parser simple (fixed-size for numeric types).
		var n uint64
		if val {
			n = 1
		}
		return w.writeUint64(n)
	case string:
		// String stats are length-prefixed: [len uint32 LE] [bytes].
		b := []byte(val)
		if err := w.writeUint32(uint32(len(b))); err != nil {
			return err
		}
		return w.writeBytes(b)
	default:
		return fmt.Errorf("unsupported stat type: %T", v)
	}
}

// ---------------------------------------------------------------------------
// Schema validation
// ---------------------------------------------------------------------------

func (w *SingleFileWriter) validateSchema(s Schema) error {
	ref := w.metadata.Schema
	if len(s.Names) != len(ref.Names) {
		return fmt.Errorf("schema mismatch: got %d columns, expected %d",
			len(s.Names), len(ref.Names))
	}
	for i := range ref.Names {
		if s.Names[i] != ref.Names[i] {
			return fmt.Errorf("schema mismatch: column %d name %q != %q",
				i, s.Names[i], ref.Names[i])
		}
		if s.Types[i] != ref.Types[i] {
			return fmt.Errorf("schema mismatch: column %d type %v != %v",
				i, s.Types[i], ref.Types[i])
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Low-level write helpers
// ---------------------------------------------------------------------------

// writeBytes writes raw bytes and advances the offset tracker.
func (w *SingleFileWriter) writeBytes(data []byte) error {
	n, err := w.file.Write(data)
	w.offset += uint64(n)
	return err
}

func (w *SingleFileWriter) writeUint16(v uint16) error {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], v)
	return w.writeBytes(buf[:])
}

func (w *SingleFileWriter) writeUint32(v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	return w.writeBytes(buf[:])
}

func (w *SingleFileWriter) writeUint64(v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	return w.writeBytes(buf[:])
}

func (w *SingleFileWriter) writeInt64(v int64) error {
	return w.writeUint64(uint64(v))
}

func (w *SingleFileWriter) writeFloat64(v float64) error {
	return w.writeUint64(math.Float64bits(v))
}
