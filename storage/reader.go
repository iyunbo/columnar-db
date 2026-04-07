package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// SingleFileReader reads columnar data from a single .cldb file.
//
// It implements FormatReader: callers open a file, inspect the schema and
// row group count, then read individual row groups on demand. The footer
// is parsed eagerly on open so that ReadRowGroup can seek directly to any
// column chunk without scanning the file.
//
// Open path:
//  1. OpenFile — opens the file, verifies header/trailing magic, parses footer
//  2. Schema / NumRowGroups — inspect metadata
//  3. ReadRowGroup(i) — seek to row group i, decode column chunks
//  4. Close — release the file handle
type SingleFileReader struct {
	file     *os.File
	metadata FileMetadata
}

// OpenFile opens a .cldb file and parses its header and footer.
//
// It verifies the header magic + version and the trailing magic, then
// reads the footer to populate the schema and row group index. The
// returned reader is ready for ReadRowGroup calls.
func OpenFile(path string) (*SingleFileReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	r := &SingleFileReader{file: f}

	// Verify header: magic (4 bytes) + version (2 bytes).
	if err := r.readHeader(); err != nil {
		f.Close()
		return nil, err
	}

	// Verify trailing magic and read footer length from the last 8 bytes.
	footerLen, err := r.readTrailer()
	if err != nil {
		f.Close()
		return nil, err
	}

	// Read and parse the footer (schema + row group index).
	if err := r.readFooter(footerLen); err != nil {
		f.Close()
		return nil, err
	}

	return r, nil
}

// Schema returns the file's schema (column names and types).
func (r *SingleFileReader) Schema() Schema {
	return r.metadata.Schema
}

// NumRowGroups returns how many row groups the file contains.
func (r *SingleFileReader) NumRowGroups() int {
	return len(r.metadata.RowGroups)
}

// ReadRowGroup reads and deserializes the row group at the given index.
// Index is 0-based. Returns an error if index is out of range.
func (r *SingleFileReader) ReadRowGroup(index int) (*RowGroup, error) {
	if index < 0 || index >= len(r.metadata.RowGroups) {
		return nil, fmt.Errorf("row group index %d out of range [0, %d)",
			index, len(r.metadata.RowGroups))
	}

	rgMeta := r.metadata.RowGroups[index]
	schema := r.metadata.Schema
	columns := make([]*ColumnChunk, len(schema.Names))

	for i := range schema.Names {
		colMeta := rgMeta.Columns[i]

		// Read the raw chunk bytes from disk.
		buf := make([]byte, colMeta.Size)
		if _, err := r.file.ReadAt(buf, colMeta.Offset); err != nil {
			return nil, fmt.Errorf("read column %q chunk: %w", schema.Names[i], err)
		}

		// The null bitmap is always the last ceil(rowCount/8) bytes.
		bitmapSize := (rgMeta.RowCount + 7) / 8
		if int(colMeta.Size) < bitmapSize {
			return nil, fmt.Errorf("column %q chunk size %d too small for bitmap (%d bytes)", schema.Names[i], colMeta.Size, bitmapSize)
		}
		valueBytes := buf[:len(buf)-bitmapSize]
		bitmapBytes := buf[len(buf)-bitmapSize:]

		// Reconstruct null bitmap.
		nulls := newNullBitmapFromBytes(bitmapBytes, rgMeta.RowCount)

		// Decode typed values.
		col, err := r.decodeColumn(schema.Types[i], valueBytes, rgMeta.RowCount)
		if err != nil {
			return nil, fmt.Errorf("decode column %q: %w", schema.Names[i], err)
		}

		chunk, err := NewColumnChunk(schema.Names[i], col, nulls)
		if err != nil {
			return nil, fmt.Errorf("create column chunk %q: %w", schema.Names[i], err)
		}
		columns[i] = chunk
	}

	return NewRowGroup(columns...)
}

// Close releases resources held by the reader.
func (r *SingleFileReader) Close() error {
	return r.file.Close()
}

// ---------------------------------------------------------------------------
// Header / trailer / footer parsing
// ---------------------------------------------------------------------------

// readHeader verifies the file header: 4-byte magic "CLDB" + 2-byte LE version.
func (r *SingleFileReader) readHeader() error {
	var header [HeaderSize]byte
	if _, err := io.ReadFull(r.file, header[:]); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	if string(header[:MagicSize]) != MagicBytes {
		return fmt.Errorf("invalid magic: got %q, want %q", header[:MagicSize], MagicBytes)
	}

	version := binary.LittleEndian.Uint16(header[MagicSize:])
	if version != FormatVersion {
		return fmt.Errorf("unsupported version: got %d, want %d", version, FormatVersion)
	}

	r.metadata.Version = version
	return nil
}

// readTrailer reads the last 8 bytes: footer length (uint32 LE) + trailing
// magic (4 bytes). Returns the footer length.
func (r *SingleFileReader) readTrailer() (uint32, error) {
	// Seek to the last 8 bytes of the file.
	var trailer [TrailerSize]byte
	fi, err := r.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat file: %w", err)
	}
	fileSize := fi.Size()

	if fileSize < int64(HeaderSize+TrailerSize) {
		return 0, fmt.Errorf("file too small: %d bytes", fileSize)
	}

	if _, err := r.file.ReadAt(trailer[:], fileSize-int64(TrailerSize)); err != nil {
		return 0, fmt.Errorf("read trailer: %w", err)
	}

	// Verify trailing magic.
	if string(trailer[FooterLengthSize:]) != MagicBytes {
		return 0, fmt.Errorf("invalid trailing magic: got %q, want %q",
			trailer[FooterLengthSize:], MagicBytes)
	}

	footerLen := binary.LittleEndian.Uint32(trailer[:FooterLengthSize])
	return footerLen, nil
}

// readFooter reads and parses the footer section: schema + row group index.
// footerLen is the number of bytes in the footer (excluding the footer length
// field and trailing magic).
func (r *SingleFileReader) readFooter(footerLen uint32) error {
	fi, err := r.file.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	fileSize := fi.Size()

	// Sanity-check footer length before allocating.
	maxFooterLen := fileSize - int64(HeaderSize) - int64(TrailerSize)
	if int64(footerLen) > maxFooterLen || int64(footerLen) < 0 {
		return fmt.Errorf("invalid footer length %d (file size %d)", footerLen, fileSize)
	}

	// Footer starts at: fileSize - TrailerSize - footerLen
	footerStart := fileSize - int64(TrailerSize) - int64(footerLen)
	buf := make([]byte, footerLen)
	if _, err := r.file.ReadAt(buf, footerStart); err != nil {
		return fmt.Errorf("read footer: %w", err)
	}

	pos := 0

	// --- Parse schema ---
	if pos+2 > len(buf) {
		return fmt.Errorf("footer too short for num_columns")
	}
	numCols := int(binary.LittleEndian.Uint16(buf[pos:]))
	pos += 2

	names := make([]string, numCols)
	types := make([]ColumnType, numCols)

	for i := range numCols {
		if pos+2 > len(buf) {
			return fmt.Errorf("footer too short for column %d name_len", i)
		}
		nameLen := int(binary.LittleEndian.Uint16(buf[pos:]))
		pos += 2

		if pos+nameLen+1 > len(buf) {
			return fmt.Errorf("footer too short for column %d name+type", i)
		}
		names[i] = string(buf[pos : pos+nameLen])
		pos += nameLen
		types[i] = ColumnType(buf[pos])
		pos++
	}

	r.metadata.Schema = Schema{Names: names, Types: types}

	// --- Parse row group index ---
	if pos+4 > len(buf) {
		return fmt.Errorf("footer too short for num_row_groups")
	}
	numRGs := int(binary.LittleEndian.Uint32(buf[pos:]))
	pos += 4

	r.metadata.RowGroups = make([]RowGroupMeta, numRGs)

	for rg := range numRGs {
		if pos+4 > len(buf) {
			return fmt.Errorf("footer too short for row group %d row_count", rg)
		}
		rowCount := int(binary.LittleEndian.Uint32(buf[pos:]))
		pos += 4

		rgMeta := RowGroupMeta{
			RowCount: rowCount,
			Columns:  make([]ColumnChunkMeta, numCols),
		}

		for col := range numCols {
			colMeta, n, err := r.parseColumnChunkMeta(buf[pos:], types[col])
			if err != nil {
				return fmt.Errorf("parse column %d meta in row group %d: %w", col, rg, err)
			}
			rgMeta.Columns[col] = colMeta
			pos += n
		}

		r.metadata.RowGroups[rg] = rgMeta
	}

	return nil
}

// parseColumnChunkMeta parses one ColumnChunkMeta from buf and returns it
// along with the number of bytes consumed.
func (r *SingleFileReader) parseColumnChunkMeta(buf []byte, colType ColumnType) (ColumnChunkMeta, int, error) {
	var meta ColumnChunkMeta
	pos := 0

	// offset (uint64)
	if pos+8 > len(buf) {
		return meta, 0, fmt.Errorf("too short for offset")
	}
	meta.Offset = int64(binary.LittleEndian.Uint64(buf[pos:]))
	pos += 8

	// size (uint32)
	if pos+4 > len(buf) {
		return meta, 0, fmt.Errorf("too short for size")
	}
	meta.Size = int(binary.LittleEndian.Uint32(buf[pos:]))
	pos += 4

	// null_count (uint32)
	if pos+4 > len(buf) {
		return meta, 0, fmt.Errorf("too short for null_count")
	}
	meta.NullCount = int(binary.LittleEndian.Uint32(buf[pos:]))
	pos += 4

	// has_min_max (uint8)
	if pos+1 > len(buf) {
		return meta, 0, fmt.Errorf("too short for has_min_max")
	}
	hasStats := buf[pos]
	pos++

	// Only 0 and 1 are defined today; treat any other value as "no stats"
	// so that files written by a future encoder (which might use 2+ for
	// new stat kinds) can still be opened by this reader.
	if hasStats == 1 {
		minVal, n, err := r.parseStatValue(buf[pos:], colType)
		if err != nil {
			return meta, 0, fmt.Errorf("parse min: %w", err)
		}
		meta.Min = minVal
		pos += n

		maxVal, n, err := r.parseStatValue(buf[pos:], colType)
		if err != nil {
			return meta, 0, fmt.Errorf("parse max: %w", err)
		}
		meta.Max = maxVal
		pos += n
	}

	return meta, pos, nil
}

// parseStatValue reads a min/max stat value from buf based on the column type.
// Returns the value and bytes consumed.
func (r *SingleFileReader) parseStatValue(buf []byte, colType ColumnType) (any, int, error) {
	switch colType {
	case TypeInt64:
		if len(buf) < 8 {
			return nil, 0, fmt.Errorf("too short for int64 stat")
		}
		return int64(binary.LittleEndian.Uint64(buf)), 8, nil

	case TypeFloat64:
		if len(buf) < 8 {
			return nil, 0, fmt.Errorf("too short for float64 stat")
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(buf)), 8, nil

	case TypeBool:
		// Bool stats are stored as uint64 LE (8 bytes) to match the
		// footer spec's fixed-size rule for numeric types.
		if len(buf) < 8 {
			return nil, 0, fmt.Errorf("too short for bool stat")
		}
		v := binary.LittleEndian.Uint64(buf)
		return v != 0, 8, nil

	case TypeString:
		// String stats: [len uint32 LE] [bytes].
		if len(buf) < 4 {
			return nil, 0, fmt.Errorf("too short for string stat length")
		}
		strLen := int(binary.LittleEndian.Uint32(buf))
		if len(buf) < 4+strLen {
			return nil, 0, fmt.Errorf("too short for string stat value")
		}
		return string(buf[4 : 4+strLen]), 4 + strLen, nil

	default:
		return nil, 0, fmt.Errorf("unsupported column type: %v", colType)
	}
}

// ---------------------------------------------------------------------------
// Column value decoding
// ---------------------------------------------------------------------------

// decodeColumn dispatches to the type-specific decoder.
func (r *SingleFileReader) decodeColumn(colType ColumnType, data []byte, rowCount int) (Column, error) {
	switch colType {
	case TypeInt64:
		return decodeInt64Values(data, rowCount)
	case TypeFloat64:
		return decodeFloat64Values(data, rowCount)
	case TypeBool:
		return decodeBoolValues(data, rowCount)
	case TypeString:
		return decodeStringValues(data, rowCount)
	default:
		return nil, fmt.Errorf("unsupported column type: %v", colType)
	}
}

// decodeInt64Values reads rowCount int64 values from data (8 bytes LE each).
func decodeInt64Values(data []byte, rowCount int) (*Int64Column, error) {
	expected := rowCount * 8
	if len(data) != expected {
		return nil, fmt.Errorf("int64 data size %d, expected %d", len(data), expected)
	}

	col := NewInt64Column()
	for i := range rowCount {
		v := int64(binary.LittleEndian.Uint64(data[i*8:]))
		col.Append(v)
	}
	return col, nil
}

// decodeFloat64Values reads rowCount float64 values from data (8 bytes LE each).
func decodeFloat64Values(data []byte, rowCount int) (*Float64Column, error) {
	expected := rowCount * 8
	if len(data) != expected {
		return nil, fmt.Errorf("float64 data size %d, expected %d", len(data), expected)
	}

	col := NewFloat64Column()
	for i := range rowCount {
		bits := binary.LittleEndian.Uint64(data[i*8:])
		col.Append(math.Float64frombits(bits))
	}
	return col, nil
}

// decodeBoolValues reads rowCount bool values from data (1 byte each).
func decodeBoolValues(data []byte, rowCount int) (*BoolColumn, error) {
	if len(data) != rowCount {
		return nil, fmt.Errorf("bool data size %d, expected %d", len(data), rowCount)
	}

	col := NewBoolColumn()
	for i := range rowCount {
		col.Append(data[i] != 0)
	}
	return col, nil
}

// decodeStringValues reads the offset-table encoded string data.
// Layout: [num_offsets uint32 LE] [off0..offN uint32 LE] [string data bytes]
func decodeStringValues(data []byte, rowCount int) (*StringColumn, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("string data too short for num_offsets")
	}

	numOffsets := int(binary.LittleEndian.Uint32(data[:4]))
	expectedOffsets := rowCount + 1
	if numOffsets != expectedOffsets {
		return nil, fmt.Errorf("num_offsets = %d, expected %d", numOffsets, expectedOffsets)
	}

	// Read offset table.
	offsetTableSize := 4 + numOffsets*4 // num_offsets field + offsets
	if len(data) < offsetTableSize {
		return nil, fmt.Errorf("string data too short for offset table")
	}

	offsets := make([]uint32, numOffsets)
	for i := range numOffsets {
		offsets[i] = binary.LittleEndian.Uint32(data[4+i*4:])
	}

	// String data starts right after the offset table.
	stringData := data[offsetTableSize:]

	col := NewStringColumn()
	for i := range rowCount {
		start := offsets[i]
		end := offsets[i+1]
		if start > end || int(end) > len(stringData) {
			return nil, fmt.Errorf("invalid string offset range [%d, %d) for data length %d", start, end, len(stringData))
		}
		col.Append(string(stringData[start:end]))
	}

	return col, nil
}

// ---------------------------------------------------------------------------
// Null bitmap reconstruction
// ---------------------------------------------------------------------------

// newNullBitmapFromBytes creates a NullBitmap from raw bytes and the total
// row count. This is the inverse of NullBitmap.Bytes().
func newNullBitmapFromBytes(data []byte, n int) *NullBitmap {
	// Copy the data to avoid aliasing the read buffer.
	cp := make([]byte, len(data))
	copy(cp, data)
	return &NullBitmap{
		data: cp,
		len:  n,
	}
}
