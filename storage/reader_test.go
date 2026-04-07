package storage

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Round-trip tests: write with writer, read back with reader, verify equality
// ---------------------------------------------------------------------------

// TestRoundTripAllTypes writes all four column types (Int64, Float64, String,
// Bool) in a single row group, reads them back, and verifies every value.
func TestRoundTripAllTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alltypes.cldb")

	// --- Write ---
	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	intCol := NewInt64ColumnFromSlice([]int64{10, -20, 300, 0})
	floatCol := NewFloat64ColumnFromSlice([]float64{1.1, -2.2, 3.3, 0.0})
	strCol := NewStringColumnFromSlice([]string{"hello", "world", "", "foo"})
	boolCol := NewBoolColumnFromSlice([]bool{true, false, true, false})

	rg, _ := NewRowGroup(
		NewColumnChunkNoNulls("i", intCol),
		NewColumnChunkNoNulls("f", floatCol),
		NewColumnChunkNoNulls("s", strCol),
		NewColumnChunkNoNulls("b", boolCol),
	)

	if err := w.WriteRowGroup(rg); err != nil {
		t.Fatalf("WriteRowGroup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// --- Read ---
	r, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	// Verify schema.
	schema := r.Schema()
	wantNames := []string{"i", "f", "s", "b"}
	wantTypes := []ColumnType{TypeInt64, TypeFloat64, TypeString, TypeBool}
	if len(schema.Names) != 4 {
		t.Fatalf("schema has %d columns, want 4", len(schema.Names))
	}
	for i := range wantNames {
		if schema.Names[i] != wantNames[i] {
			t.Errorf("column %d name = %q, want %q", i, schema.Names[i], wantNames[i])
		}
		if schema.Types[i] != wantTypes[i] {
			t.Errorf("column %d type = %v, want %v", i, schema.Types[i], wantTypes[i])
		}
	}

	if r.NumRowGroups() != 1 {
		t.Fatalf("NumRowGroups = %d, want 1", r.NumRowGroups())
	}

	got, err := r.ReadRowGroup(0)
	if err != nil {
		t.Fatalf("ReadRowGroup(0): %v", err)
	}

	if got.RowCount != 4 {
		t.Errorf("RowCount = %d, want 4", got.RowCount)
	}

	// Verify Int64 values.
	gotInt := got.Column(0).Values.(*Int64Column)
	for i, want := range []int64{10, -20, 300, 0} {
		if gotInt.Get(i) != want {
			t.Errorf("int64[%d] = %d, want %d", i, gotInt.Get(i), want)
		}
	}

	// Verify Float64 values.
	gotFloat := got.Column(1).Values.(*Float64Column)
	for i, want := range []float64{1.1, -2.2, 3.3, 0.0} {
		if gotFloat.Get(i) != want {
			t.Errorf("float64[%d] = %f, want %f", i, gotFloat.Get(i), want)
		}
	}

	// Verify String values.
	gotStr := got.Column(2).Values.(*StringColumn)
	for i, want := range []string{"hello", "world", "", "foo"} {
		if gotStr.Get(i) != want {
			t.Errorf("string[%d] = %q, want %q", i, gotStr.Get(i), want)
		}
	}

	// Verify Bool values.
	gotBool := got.Column(3).Values.(*BoolColumn)
	for i, want := range []bool{true, false, true, false} {
		if gotBool.Get(i) != want {
			t.Errorf("bool[%d] = %v, want %v", i, gotBool.Get(i), want)
		}
	}
}

// TestRoundTripMultipleRowGroups writes two row groups, reads both back,
// and verifies data integrity.
func TestRoundTripMultipleRowGroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi_rg.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	// Row group 0: 3 rows.
	rg0, _ := NewRowGroup(
		NewColumnChunkNoNulls("x", NewInt64ColumnFromSlice([]int64{1, 2, 3})),
		NewColumnChunkNoNulls("y", NewFloat64ColumnFromSlice([]float64{1.0, 2.0, 3.0})),
	)
	// Row group 1: 2 rows.
	rg1, _ := NewRowGroup(
		NewColumnChunkNoNulls("x", NewInt64ColumnFromSlice([]int64{4, 5})),
		NewColumnChunkNoNulls("y", NewFloat64ColumnFromSlice([]float64{4.0, 5.0})),
	)

	if err := w.WriteRowGroup(rg0); err != nil {
		t.Fatalf("WriteRowGroup 0: %v", err)
	}
	if err := w.WriteRowGroup(rg1); err != nil {
		t.Fatalf("WriteRowGroup 1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// --- Read ---
	r, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	if r.NumRowGroups() != 2 {
		t.Fatalf("NumRowGroups = %d, want 2", r.NumRowGroups())
	}

	// Row group 0.
	got0, err := r.ReadRowGroup(0)
	if err != nil {
		t.Fatalf("ReadRowGroup(0): %v", err)
	}
	if got0.RowCount != 3 {
		t.Errorf("rg0 RowCount = %d, want 3", got0.RowCount)
	}
	xCol0 := got0.Column(0).Values.(*Int64Column)
	for i, want := range []int64{1, 2, 3} {
		if xCol0.Get(i) != want {
			t.Errorf("rg0 x[%d] = %d, want %d", i, xCol0.Get(i), want)
		}
	}

	// Row group 1.
	got1, err := r.ReadRowGroup(1)
	if err != nil {
		t.Fatalf("ReadRowGroup(1): %v", err)
	}
	if got1.RowCount != 2 {
		t.Errorf("rg1 RowCount = %d, want 2", got1.RowCount)
	}
	xCol1 := got1.Column(0).Values.(*Int64Column)
	for i, want := range []int64{4, 5} {
		if xCol1.Get(i) != want {
			t.Errorf("rg1 x[%d] = %d, want %d", i, xCol1.Get(i), want)
		}
	}
}

// TestRoundTripWithNulls writes data with null values, reads back, and
// verifies both values and null bitmap.
func TestRoundTripWithNulls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nulls.cldb")

	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	// Int64 column: [100, NULL, 300]
	intVals := NewInt64ColumnFromSlice([]int64{100, 0, 300})
	intNulls := NewNullBitmap(3)
	intNulls.SetNull(1)
	intChunk, _ := NewColumnChunk("val", intVals, intNulls)

	// String column: ["alpha", NULL, "gamma"]
	strVals := NewStringColumnFromSlice([]string{"alpha", "", "gamma"})
	strNulls := NewNullBitmap(3)
	strNulls.SetNull(1)
	strChunk, _ := NewColumnChunk("label", strVals, strNulls)

	rg, _ := NewRowGroup(intChunk, strChunk)
	if err := w.WriteRowGroup(rg); err != nil {
		t.Fatalf("WriteRowGroup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// --- Read ---
	r, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	got, err := r.ReadRowGroup(0)
	if err != nil {
		t.Fatalf("ReadRowGroup(0): %v", err)
	}

	// Verify int column nulls.
	intCol := got.Column(0)
	if intCol.NullCount() != 1 {
		t.Errorf("int NullCount = %d, want 1", intCol.NullCount())
	}
	if !intCol.Nulls.IsNull(1) {
		t.Error("int row 1 should be null")
	}
	if intCol.Nulls.IsNull(0) || intCol.Nulls.IsNull(2) {
		t.Error("int rows 0 and 2 should not be null")
	}
	gotIntCol := intCol.Values.(*Int64Column)
	if gotIntCol.Get(0) != 100 || gotIntCol.Get(2) != 300 {
		t.Errorf("int values: got (%d, %d), want (100, 300)",
			gotIntCol.Get(0), gotIntCol.Get(2))
	}

	// Verify string column nulls.
	strCol := got.Column(1)
	if strCol.NullCount() != 1 {
		t.Errorf("str NullCount = %d, want 1", strCol.NullCount())
	}
	if !strCol.Nulls.IsNull(1) {
		t.Error("str row 1 should be null")
	}
	gotStrCol := strCol.Values.(*StringColumn)
	if gotStrCol.Get(0) != "alpha" || gotStrCol.Get(2) != "gamma" {
		t.Errorf("str values: got (%q, %q), want (\"alpha\", \"gamma\")",
			gotStrCol.Get(0), gotStrCol.Get(2))
	}
}

// TestReadSampleFile reads the reference testdata/sample.cldb and verifies
// the expected data: 2 columns (id Int64, name String), 3 rows, 1 row group.
func TestReadSampleFile(t *testing.T) {
	r, err := OpenFile("testdata/sample.cldb")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	// Verify schema.
	schema := r.Schema()
	if len(schema.Names) != 2 {
		t.Fatalf("schema has %d columns, want 2", len(schema.Names))
	}
	if schema.Names[0] != "id" || schema.Types[0] != TypeInt64 {
		t.Errorf("column 0: name=%q type=%v, want id/Int64", schema.Names[0], schema.Types[0])
	}
	if schema.Names[1] != "name" || schema.Types[1] != TypeString {
		t.Errorf("column 1: name=%q type=%v, want name/String", schema.Names[1], schema.Types[1])
	}

	if r.NumRowGroups() != 1 {
		t.Fatalf("NumRowGroups = %d, want 1", r.NumRowGroups())
	}

	rg, err := r.ReadRowGroup(0)
	if err != nil {
		t.Fatalf("ReadRowGroup(0): %v", err)
	}

	if rg.RowCount != 3 {
		t.Errorf("RowCount = %d, want 3", rg.RowCount)
	}

	// Verify id column: [1, 2, 3], no nulls.
	idCol := rg.Column(0).Values.(*Int64Column)
	for i, want := range []int64{1, 2, 3} {
		if idCol.Get(i) != want {
			t.Errorf("id[%d] = %d, want %d", i, idCol.Get(i), want)
		}
	}
	if rg.Column(0).Nulls.HasNulls() {
		t.Error("id column should have no nulls")
	}

	// Verify name column: ["alice", NULL, "charlie"].
	nameCol := rg.Column(1).Values.(*StringColumn)
	if nameCol.Get(0) != "alice" {
		t.Errorf("name[0] = %q, want \"alice\"", nameCol.Get(0))
	}
	if !rg.Column(1).Nulls.IsNull(1) {
		t.Error("name[1] should be null")
	}
	if nameCol.Get(2) != "charlie" {
		t.Errorf("name[2] = %q, want \"charlie\"", nameCol.Get(2))
	}
	if rg.Column(1).NullCount() != 1 {
		t.Errorf("name NullCount = %d, want 1", rg.Column(1).NullCount())
	}
}

// TestReadMetadata verifies the metadata fields (version, schema, row group
// index) parsed from the footer.
func TestReadMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.cldb")

	// Write a file with known data.
	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	intChunk := NewColumnChunkNoNulls("age", NewInt64ColumnFromSlice([]int64{25, 30, 35}))
	strChunk := NewColumnChunkNoNulls("city", NewStringColumnFromSlice([]string{"Paris", "Tokyo", "NYC"}))
	rg, _ := NewRowGroup(intChunk, strChunk)
	if err := w.WriteRowGroup(rg); err != nil {
		t.Fatalf("WriteRowGroup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read and check metadata.
	r, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	if r.metadata.Version != FormatVersion {
		t.Errorf("version = %d, want %d", r.metadata.Version, FormatVersion)
	}

	schema := r.Schema()
	if len(schema.Names) != 2 {
		t.Fatalf("num columns = %d, want 2", len(schema.Names))
	}
	if schema.Names[0] != "age" || schema.Names[1] != "city" {
		t.Errorf("column names = %v, want [age, city]", schema.Names)
	}

	if r.NumRowGroups() != 1 {
		t.Fatalf("NumRowGroups = %d, want 1", r.NumRowGroups())
	}

	// The metadata should record the total rows.
	if r.metadata.TotalRows() != 3 {
		t.Errorf("TotalRows = %d, want 3", r.metadata.TotalRows())
	}
}

// ---------------------------------------------------------------------------
// Error handling tests
// ---------------------------------------------------------------------------

func TestOpenFileNotFound(t *testing.T) {
	_, err := OpenFile("/nonexistent/path/file.cldb")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestOpenFileBadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "badmagic.cldb")
	if err := os.WriteFile(path, []byte("XXXX\x01\x00"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := OpenFile(path)
	if err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestOpenFileBadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "badver.cldb")
	// Valid magic, wrong version (99). Include trailer so the header check
	// is the first failure.
	data := []byte("CLDB")
	data = append(data, 0x63, 0x00)             // version 99
	data = append(data, 0x00, 0x00, 0x00, 0x00) // footer length 0
	data = append(data, []byte("CLDB")...)      // trailing magic
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := OpenFile(path)
	if err == nil {
		t.Fatal("expected error for bad version")
	}
}

func TestOpenFileBadTrailingMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "badtrail.cldb")
	// Valid header but bad trailing magic.
	data := []byte("CLDB\x01\x00")
	data = append(data, 0x00, 0x00, 0x00, 0x00) // footer length 0
	data = append(data, []byte("XXXX")...)      // bad trailing magic
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := OpenFile(path)
	if err == nil {
		t.Fatal("expected error for bad trailing magic")
	}
}

func TestReadRowGroupOutOfRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oob.cldb")
	w, _ := NewSingleFileWriter(path)
	rg, _ := NewRowGroup(NewColumnChunkNoNulls("x", NewInt64ColumnFromSlice([]int64{1})))
	w.WriteRowGroup(rg)
	w.Close()

	r, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	if _, err := r.ReadRowGroup(-1); err == nil {
		t.Error("expected error for index -1")
	}
	if _, err := r.ReadRowGroup(1); err == nil {
		t.Error("expected error for index 1 (only 1 row group)")
	}
}

// ---------------------------------------------------------------------------
// Phase 1 completion: large file round-trip
// ---------------------------------------------------------------------------

// TestLargeFile writes 1M rows (5 columns) to disk, reads back, verifies
// data integrity, and reports file size vs equivalent CSV estimate.
//
// This validates the Phase 1 success criteria:
//   - Write 1M rows (5 columns) to disk
//   - Read back and verify data integrity
//   - Print file size vs equivalent CSV (should be smaller)
func TestLargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	const totalRows = 1_000_000
	path := filepath.Join(t.TempDir(), "large.cldb")

	// --- Generate test data ---
	rng := rand.New(rand.NewSource(42))

	ids := make([]int64, totalRows)
	prices := make([]float64, totalRows)
	names := make([]string, totalRows)
	actives := make([]bool, totalRows)
	scores := make([]int64, totalRows)

	namePool := []string{"alice", "bob", "charlie", "diana", "eve", "frank", "grace", "heidi"}
	csvEstimate := 0

	for i := range totalRows {
		ids[i] = int64(i)
		prices[i] = float64(rng.Intn(100000)) / 100.0
		names[i] = namePool[rng.Intn(len(namePool))]
		actives[i] = rng.Intn(2) == 1
		scores[i] = int64(rng.Intn(1000))

		// Estimate CSV row: "id,price,name,active,score\n"
		csvEstimate += len(fmt.Sprintf("%d,%.2f,%s,%v,%d\n",
			ids[i], prices[i], names[i], actives[i], scores[i]))
	}

	// --- Write ---
	w, err := NewSingleFileWriter(path)
	if err != nil {
		t.Fatalf("NewSingleFileWriter: %v", err)
	}

	// Split into row groups of DefaultRowGroupSize.
	for start := 0; start < totalRows; start += DefaultRowGroupSize {
		end := min(start+DefaultRowGroupSize, totalRows)

		rg, err := NewRowGroup(
			NewColumnChunkNoNulls("id", NewInt64ColumnFromSlice(ids[start:end])),
			NewColumnChunkNoNulls("price", NewFloat64ColumnFromSlice(prices[start:end])),
			NewColumnChunkNoNulls("name", NewStringColumnFromSlice(names[start:end])),
			NewColumnChunkNoNulls("active", NewBoolColumnFromSlice(actives[start:end])),
			NewColumnChunkNoNulls("score", NewInt64ColumnFromSlice(scores[start:end])),
		)
		if err != nil {
			t.Fatalf("NewRowGroup at row %d: %v", start, err)
		}
		if err := w.WriteRowGroup(rg); err != nil {
			t.Fatalf("WriteRowGroup at row %d: %v", start, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// --- Report file size ---
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	fileSize := fi.Size()

	t.Logf("=== Phase 1 Success Criteria ===")
	t.Logf("Rows:          %d", totalRows)
	t.Logf("Columns:       5 (Int64, Float64, String, Bool, Int64)")
	t.Logf("Row groups:    %d", (totalRows+DefaultRowGroupSize-1)/DefaultRowGroupSize)
	t.Logf("CLDB file:     %.2f MB", float64(fileSize)/1024/1024)
	t.Logf("CSV estimate:  %.2f MB", float64(csvEstimate)/1024/1024)
	t.Logf("Ratio:         %.1f%% of CSV", float64(fileSize)/float64(csvEstimate)*100)
	if fileSize > int64(csvEstimate) {
		t.Logf("Note: CLDB > CSV because fixed-width encoding (8B/int64, 8B/float64)")
		t.Logf("      uses more space than ASCII for small numbers. Compression (Phase 2)")
		t.Logf("      will reverse this — columnar layout enables much better compression.")
	}

	// --- Read back and verify ---
	r, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer r.Close()

	schema := r.Schema()
	if len(schema.Names) != 5 {
		t.Fatalf("schema has %d columns, want 5", len(schema.Names))
	}

	expectedRGs := (totalRows + DefaultRowGroupSize - 1) / DefaultRowGroupSize
	if r.NumRowGroups() != expectedRGs {
		t.Fatalf("NumRowGroups = %d, want %d", r.NumRowGroups(), expectedRGs)
	}

	// Read all row groups and verify data.
	rowOffset := 0
	for rgi := 0; rgi < r.NumRowGroups(); rgi++ {
		rg, err := r.ReadRowGroup(rgi)
		if err != nil {
			t.Fatalf("ReadRowGroup(%d): %v", rgi, err)
		}

		idCol := rg.Column(0).Values.(*Int64Column)
		priceCol := rg.Column(1).Values.(*Float64Column)
		nameCol := rg.Column(2).Values.(*StringColumn)
		activeCol := rg.Column(3).Values.(*BoolColumn)
		scoreCol := rg.Column(4).Values.(*Int64Column)

		for i := 0; i < rg.RowCount; i++ {
			row := rowOffset + i
			if idCol.Get(i) != ids[row] {
				t.Fatalf("row %d: id = %d, want %d", row, idCol.Get(i), ids[row])
			}
			if priceCol.Get(i) != prices[row] {
				t.Fatalf("row %d: price = %f, want %f", row, priceCol.Get(i), prices[row])
			}
			if nameCol.Get(i) != names[row] {
				t.Fatalf("row %d: name = %q, want %q", row, nameCol.Get(i), names[row])
			}
			if activeCol.Get(i) != actives[row] {
				t.Fatalf("row %d: active = %v, want %v", row, activeCol.Get(i), actives[row])
			}
			if scoreCol.Get(i) != scores[row] {
				t.Fatalf("row %d: score = %d, want %d", row, scoreCol.Get(i), scores[row])
			}
		}

		rowOffset += rg.RowCount
	}

	if rowOffset != totalRows {
		t.Errorf("total rows read = %d, want %d", rowOffset, totalRows)
	}

	t.Logf("All %d rows verified successfully.", totalRows)
}
