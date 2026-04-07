package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteSampleFile writes the exact same data as testdata/sample.cldb
// (2 columns, 3 rows, 1 row group) and verifies byte-for-byte equality.
func TestWriteSampleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	// Build the same data as sample_format.md:
	//   id (Int64): 1, 2, 3          — no nulls
	//   name (String): "alice", NULL, "charlie" — null at row 1
	idCol := NewInt64ColumnFromSlice([]int64{1, 2, 3})
	idChunk := NewColumnChunkNoNulls("id", idCol)

	nameCol := NewStringColumnFromSlice([]string{"alice", "", "charlie"})
	nameNulls := NewNullBitmap(3)
	nameNulls.SetNull(1) // row 1 is NULL
	nameChunk, err := NewColumnChunk("name", nameCol, nameNulls)
	if err != nil {
		t.Fatalf("NewColumnChunk: %v", err)
	}

	rg, err := NewRowGroup(idChunk, nameChunk)
	if err != nil {
		t.Fatalf("NewRowGroup: %v", err)
	}

	if err := w.WriteRowGroup(rg); err != nil {
		t.Fatalf("WriteRowGroup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back and verify.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Verify file size matches the 164-byte spec.
	if len(got) != 164 {
		t.Errorf("file size = %d, want 164", len(got))
	}

	// Verify header magic.
	if string(got[:4]) != MagicBytes {
		t.Errorf("header magic = %q, want %q", got[:4], MagicBytes)
	}

	// Verify version.
	ver := binary.LittleEndian.Uint16(got[4:6])
	if ver != 1 {
		t.Errorf("version = %d, want 1", ver)
	}

	// Verify trailing magic.
	if string(got[len(got)-4:]) != MagicBytes {
		t.Errorf("trailing magic = %q, want %q", got[len(got)-4:], MagicBytes)
	}

	// Verify footer length.
	footerLen := binary.LittleEndian.Uint32(got[len(got)-8 : len(got)-4])
	if footerLen != 92 {
		t.Errorf("footer length = %d, want 92", footerLen)
	}

	// Byte-for-byte comparison with the reference file.
	ref, err := os.ReadFile("testdata/sample.cldb")
	if err != nil {
		t.Logf("skipping byte-for-byte check: %v", err)
	} else {
		if len(got) != len(ref) {
			t.Errorf("file sizes differ: got %d, ref %d", len(got), len(ref))
		}
		minLen := min(len(ref), len(got))
		for i := 0; i < minLen; i++ {
			if got[i] != ref[i] {
				t.Errorf("byte %d differs: got 0x%02X, want 0x%02X", i, got[i], ref[i])
			}
		}
	}
}

// TestWriteMultipleRowGroups verifies that writing two row groups produces
// a valid file with correct offsets and two entries in the footer index.
func TestWriteMultipleRowGroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	// Row group 1: single Int64 column, 2 rows.
	col1 := NewInt64ColumnFromSlice([]int64{10, 20})
	chunk1 := NewColumnChunkNoNulls("x", col1)
	rg1, _ := NewRowGroup(chunk1)

	// Row group 2: same schema, 3 rows.
	col2 := NewInt64ColumnFromSlice([]int64{30, 40, 50})
	chunk2 := NewColumnChunkNoNulls("x", col2)
	rg2, _ := NewRowGroup(chunk2)

	if err := w.WriteRowGroup(rg1); err != nil {
		t.Fatalf("WriteRowGroup 1: %v", err)
	}
	if err := w.WriteRowGroup(rg2); err != nil {
		t.Fatalf("WriteRowGroup 2: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Basic structural checks.
	if string(got[:4]) != MagicBytes {
		t.Errorf("header magic mismatch")
	}
	if string(got[len(got)-4:]) != MagicBytes {
		t.Errorf("trailing magic mismatch")
	}

	// Verify the footer records two row groups.
	footerLen := binary.LittleEndian.Uint32(got[len(got)-8 : len(got)-4])
	footerStart := len(got) - 8 - int(footerLen)

	// Parse schema: num_columns(2) + column entries
	pos := footerStart
	numCols := binary.LittleEndian.Uint16(got[pos:])
	pos += 2
	if numCols != 1 {
		t.Errorf("num_columns = %d, want 1", numCols)
	}

	// Skip schema entries.
	for i := 0; i < int(numCols); i++ {
		nameLen := binary.LittleEndian.Uint16(got[pos:])
		pos += 2 + int(nameLen) + 1 // name_len + name + type
	}

	// Parse row group count.
	numRGs := binary.LittleEndian.Uint32(got[pos:])
	pos += 4
	if numRGs != 2 {
		t.Errorf("num_row_groups = %d, want 2", numRGs)
	}

	// Verify row counts.
	rc1 := binary.LittleEndian.Uint32(got[pos:])
	if rc1 != 2 {
		t.Errorf("row group 0 row_count = %d, want 2", rc1)
	}
}

// TestWriteNullHandling verifies that a column with all nulls and a column
// with mixed nulls are encoded correctly.
func TestWriteNullHandling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nulls.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	// Column with mixed nulls: values [100, 0, 300], nulls at row 1.
	vals := NewInt64ColumnFromSlice([]int64{100, 0, 300})
	nulls := NewNullBitmap(3)
	nulls.SetNull(1)
	mixed, _ := NewColumnChunk("mixed", vals, nulls)

	// Column with all nulls: values [0, 0], all null.
	allNullVals := NewInt64ColumnFromSlice([]int64{0, 0, 0})
	allNulls := NewNullBitmap(3)
	allNulls.SetNull(0)
	allNulls.SetNull(1)
	allNulls.SetNull(2)
	allNull, _ := NewColumnChunk("allnull", allNullVals, allNulls)

	rg, _ := NewRowGroup(mixed, allNull)

	if err := w.WriteRowGroup(rg); err != nil {
		t.Fatalf("WriteRowGroup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Verify trailing magic — file is structurally valid.
	if string(got[len(got)-4:]) != MagicBytes {
		t.Errorf("trailing magic mismatch")
	}

	// Parse the footer to check null counts and stats.
	footerLen := binary.LittleEndian.Uint32(got[len(got)-8 : len(got)-4])
	footerStart := len(got) - 8 - int(footerLen)
	pos := footerStart

	// Skip schema: num_columns(2) + 2 column entries.
	numCols := int(binary.LittleEndian.Uint16(got[pos:]))
	pos += 2
	for range numCols {
		nameLen := int(binary.LittleEndian.Uint16(got[pos:]))
		pos += 2 + nameLen + 1
	}

	// Skip num_row_groups(4) + row_count(4).
	pos += 4 + 4

	// Column 0 ("mixed"): offset(8) + size(4) + null_count(4) + has_min_max(1) + min(8) + max(8)
	pos += 8 + 4 // skip offset and size
	nullCount0 := binary.LittleEndian.Uint32(got[pos:])
	pos += 4
	if nullCount0 != 1 {
		t.Errorf("mixed null_count = %d, want 1", nullCount0)
	}
	hasStats0 := got[pos]
	pos++
	if hasStats0 != 1 {
		t.Errorf("mixed has_min_max = %d, want 1", hasStats0)
	}
	minVal := int64(binary.LittleEndian.Uint64(got[pos:]))
	pos += 8
	maxVal := int64(binary.LittleEndian.Uint64(got[pos:]))
	pos += 8
	if minVal != 100 {
		t.Errorf("mixed min = %d, want 100", minVal)
	}
	if maxVal != 300 {
		t.Errorf("mixed max = %d, want 300", maxVal)
	}

	// Column 1 ("allnull"): should have null_count=3 and has_min_max=0.
	pos += 8 + 4 // skip offset and size
	nullCount1 := binary.LittleEndian.Uint32(got[pos:])
	pos += 4
	if nullCount1 != 3 {
		t.Errorf("allnull null_count = %d, want 3", nullCount1)
	}
	hasStats1 := got[pos]
	if hasStats1 != 0 {
		t.Errorf("allnull has_min_max = %d, want 0", hasStats1)
	}
}

// TestWriteAllTypes verifies that all four column types (Int64, Float64,
// String, Bool) can be written and produce a valid file.
func TestWriteAllTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alltypes.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	intChunk := NewColumnChunkNoNulls("i", NewInt64ColumnFromSlice([]int64{1, 2}))
	floatChunk := NewColumnChunkNoNulls("f", NewFloat64ColumnFromSlice([]float64{1.5, 2.5}))
	strChunk := NewColumnChunkNoNulls("s", NewStringColumnFromSlice([]string{"hi", "bye"}))
	boolChunk := NewColumnChunkNoNulls("b", NewBoolColumnFromSlice([]bool{true, false}))

	rg, _ := NewRowGroup(intChunk, floatChunk, strChunk, boolChunk)
	if err := w.WriteRowGroup(rg); err != nil {
		t.Fatalf("WriteRowGroup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Structural checks.
	if string(got[:4]) != MagicBytes {
		t.Errorf("header magic mismatch")
	}
	if string(got[len(got)-4:]) != MagicBytes {
		t.Errorf("trailing magic mismatch")
	}
	if binary.LittleEndian.Uint16(got[4:6]) != FormatVersion {
		t.Errorf("version mismatch")
	}

	// Spot-check: int64 values at offset 6.
	v0 := int64(binary.LittleEndian.Uint64(got[6:14]))
	v1 := int64(binary.LittleEndian.Uint64(got[14:22]))
	if v0 != 1 || v1 != 2 {
		t.Errorf("int64 values = (%d, %d), want (1, 2)", v0, v1)
	}
}

// TestWriteSchemaMismatch verifies that writing a row group with a
// different schema than the first one returns an error.
func TestWriteSchemaMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mismatch.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}
	defer w.Close()

	rg1, _ := NewRowGroup(NewColumnChunkNoNulls("x", NewInt64ColumnFromSlice([]int64{1})))
	rg2, _ := NewRowGroup(NewColumnChunkNoNulls("y", NewInt64ColumnFromSlice([]int64{2})))

	if err := w.WriteRowGroup(rg1); err != nil {
		t.Fatalf("WriteRowGroup 1: %v", err)
	}

	err = w.WriteRowGroup(rg2)
	if err == nil {
		t.Fatal("expected schema mismatch error, got nil")
	}
}
