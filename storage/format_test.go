package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateSampleCLDB builds the minimal sample.cldb file described in
// testdata/sample_format.md and writes it to testdata/sample.cldb.
// It also verifies the generated file matches the expected byte layout.
func TestGenerateSampleCLDB(t *testing.T) {
	var buf bytes.Buffer

	// --- Header ---
	buf.WriteString(MagicBytes)                                  // "CLDB"
	binary.Write(&buf, binary.LittleEndian, FormatVersion)       // uint16(1)

	if buf.Len() != HeaderSize {
		t.Fatalf("header size: got %d, want %d", buf.Len(), HeaderSize)
	}

	// --- Row Group 0, Column Chunk 0: "id" (Int64) ---
	chunkIDOffset := int64(buf.Len()) // 6
	// Values: 3 x int64 LE
	for _, v := range []int64{1, 2, 3} {
		binary.Write(&buf, binary.LittleEndian, v)
	}
	// Null bitmap: 1 byte, no nulls
	buf.WriteByte(0x00)
	chunkIDSize := int(int64(buf.Len()) - chunkIDOffset) // 25

	// --- Row Group 0, Column Chunk 1: "name" (String) ---
	chunkNameOffset := int64(buf.Len()) // 31
	// String encoding: num_offsets (u32) + offsets (u32 each) + data + null bitmap
	// Strings: "alice", "" (null), "charlie"
	// Offsets: [0, 5, 5, 12]
	binary.Write(&buf, binary.LittleEndian, uint32(4)) // num_offsets = N+1
	for _, off := range []uint32{0, 5, 5, 12} {
		binary.Write(&buf, binary.LittleEndian, off)
	}
	buf.WriteString("alice")
	buf.WriteString("charlie")
	// Null bitmap: 1 byte, bit 1 set (row 1 is null)
	buf.WriteByte(0x02)
	chunkNameSize := int(int64(buf.Len()) - chunkNameOffset) // 33

	// --- Footer ---
	footerStart := buf.Len() // 64

	// Schema: num_columns + (name_len + name + col_type) per column
	binary.Write(&buf, binary.LittleEndian, uint16(2)) // num_columns
	// Column 0: "id", TypeInt64
	binary.Write(&buf, binary.LittleEndian, uint16(2)) // name_len
	buf.WriteString("id")
	buf.WriteByte(byte(TypeInt64)) // 0
	// Column 1: "name", TypeString
	binary.Write(&buf, binary.LittleEndian, uint16(4)) // name_len
	buf.WriteString("name")
	buf.WriteByte(byte(TypeString)) // 2

	// Row Group Index
	binary.Write(&buf, binary.LittleEndian, uint32(1)) // num_row_groups
	binary.Write(&buf, binary.LittleEndian, uint32(3)) // row_count

	// Column 0 "id" metadata
	binary.Write(&buf, binary.LittleEndian, uint64(chunkIDOffset)) // offset
	binary.Write(&buf, binary.LittleEndian, uint32(chunkIDSize))   // size
	binary.Write(&buf, binary.LittleEndian, uint32(0))             // null_count
	buf.WriteByte(1)                                                // has_min_max
	binary.Write(&buf, binary.LittleEndian, int64(1))              // min
	binary.Write(&buf, binary.LittleEndian, int64(3))              // max

	// Column 1 "name" metadata
	binary.Write(&buf, binary.LittleEndian, uint64(chunkNameOffset)) // offset
	binary.Write(&buf, binary.LittleEndian, uint32(chunkNameSize))   // size
	binary.Write(&buf, binary.LittleEndian, uint32(1))               // null_count
	buf.WriteByte(1)                                                  // has_min_max
	// String min/max: length-prefixed
	binary.Write(&buf, binary.LittleEndian, uint32(5)) // min_len
	buf.WriteString("alice")
	binary.Write(&buf, binary.LittleEndian, uint32(7)) // max_len
	buf.WriteString("charlie")

	footerLen := uint32(buf.Len() - footerStart) // 92

	// Footer length + trailing magic
	binary.Write(&buf, binary.LittleEndian, footerLen)
	buf.WriteString(MagicBytes) // trailing "CLDB"

	data := buf.Bytes()

	// --- Verify key properties ---
	t.Logf("total file size: %d bytes", len(data))

	if len(data) != 164 {
		t.Errorf("file size: got %d, want 164", len(data))
	}

	// Header magic
	if string(data[0:4]) != MagicBytes {
		t.Errorf("header magic: got %q, want %q", data[0:4], MagicBytes)
	}

	// Trailing magic
	if string(data[len(data)-4:]) != MagicBytes {
		t.Errorf("trailing magic: got %q, want %q", data[len(data)-4:], MagicBytes)
	}

	// Version
	ver := binary.LittleEndian.Uint16(data[4:6])
	if ver != 1 {
		t.Errorf("version: got %d, want 1", ver)
	}

	// Footer length
	fl := binary.LittleEndian.Uint32(data[156:160])
	if fl != 92 {
		t.Errorf("footer length: got %d, want 92", fl)
	}

	// Read path simulation: verify we can find footer from the end
	trailing := data[len(data)-TrailerSize:]
	readFooterLen := binary.LittleEndian.Uint32(trailing[0:4])
	readMagic := string(trailing[4:8])
	if readMagic != MagicBytes {
		t.Errorf("read-path trailing magic: %q", readMagic)
	}
	footerOffset := len(data) - TrailerSize - int(readFooterLen)
	if footerOffset != 64 {
		t.Errorf("footer offset: got %d, want 64", footerOffset)
	}

	// Verify id values
	for i, want := range []int64{1, 2, 3} {
		got := int64(binary.LittleEndian.Uint64(data[6+i*8 : 6+(i+1)*8]))
		if got != want {
			t.Errorf("id[%d]: got %d, want %d", i, got, want)
		}
	}

	// Verify null bitmap for "name" column: row 1 is null
	nameBitmapOffset := chunkNameOffset + int64(chunkNameSize) - 1
	if data[nameBitmapOffset] != 0x02 {
		t.Errorf("name null bitmap: got 0x%02x, want 0x02", data[nameBitmapOffset])
	}

	// Write to testdata/sample.cldb
	outPath := filepath.Join("testdata", "sample.cldb")
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		t.Fatalf("write sample.cldb: %v", err)
	}
	t.Logf("wrote %s (%d bytes)", outPath, len(data))
}

// TestFileMetadataTotalRows verifies the TotalRows helper.
func TestFileMetadataTotalRows(t *testing.T) {
	meta := &FileMetadata{
		RowGroups: []RowGroupMeta{
			{RowCount: 100},
			{RowCount: 200},
			{RowCount: 50},
		},
	}
	if got := meta.TotalRows(); got != 350 {
		t.Errorf("TotalRows: got %d, want 350", got)
	}
}

// TestColumnChunkMetaTypes verifies ColumnChunkMeta can hold typed min/max.
func TestColumnChunkMetaTypes(t *testing.T) {
	cases := []struct {
		name string
		meta ColumnChunkMeta
	}{
		{
			name: "int64",
			meta: ColumnChunkMeta{Min: int64(1), Max: int64(100)},
		},
		{
			name: "float64",
			meta: ColumnChunkMeta{Min: 1.5, Max: 99.9},
		},
		{
			name: "string",
			meta: ColumnChunkMeta{Min: "alice", Max: "zebra"},
		},
		{
			name: "bool",
			meta: ColumnChunkMeta{Min: false, Max: true},
		},
		{
			name: "all-nulls",
			meta: ColumnChunkMeta{Min: nil, Max: nil, NullCount: 10},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Verify the struct holds values without type assertion panics.
			_ = tc.meta.Min
			_ = tc.meta.Max
		})
	}
}

// TestConstants verifies format constants are self-consistent.
func TestConstants(t *testing.T) {
	if len(MagicBytes) != MagicSize {
		t.Errorf("MagicBytes length %d != MagicSize %d", len(MagicBytes), MagicSize)
	}
	if HeaderSize != MagicSize+2 {
		t.Errorf("HeaderSize %d != MagicSize+2 (%d)", HeaderSize, MagicSize+2)
	}
	if TrailerSize != FooterLengthSize+MagicSize {
		t.Errorf("TrailerSize %d != FooterLengthSize+MagicSize (%d)", TrailerSize, FooterLengthSize+MagicSize)
	}
	if DefaultRowGroupSize <= 0 {
		t.Error("DefaultRowGroupSize must be positive")
	}

}
