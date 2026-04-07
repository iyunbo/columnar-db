package exec

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// makeIntRowGroup builds a 1-column Int64 row group with values 0..n-1,
// optionally setting nulls at indices listed in nullIdx.
func makeIntRowGroup(t *testing.T, n int, nullIdx ...int) *storage.RowGroup {
	t.Helper()
	vals := make([]int64, n)
	for i := range n {
		vals[i] = int64(i)
	}
	col := storage.NewInt64ColumnFromSlice(vals)
	nulls := storage.NewNullBitmap(n)
	for _, idx := range nullIdx {
		nulls.SetNull(idx)
	}
	chunk, err := storage.NewColumnChunk("x", col, nulls)
	if err != nil {
		t.Fatalf("NewColumnChunk: %v", err)
	}
	rg, err := storage.NewRowGroup(chunk)
	if err != nil {
		t.Fatalf("NewRowGroup: %v", err)
	}
	return rg
}

func TestScanSingleBatchSmallerThanVectorSize(t *testing.T) {
	rg := makeIntRowGroup(t, 500)
	scan, err := NewScanOp(rg, []string{"x"})
	if err != nil {
		t.Fatalf("NewScanOp: %v", err)
	}

	b, ok := scan.Next()
	if !ok {
		t.Fatal("expected first batch")
	}
	if b.Len() != 500 {
		t.Errorf("Len = %d, want 500", b.Len())
	}
	got := b.Vectors[0].Int64s()
	for i, v := range got {
		if v != int64(i) {
			t.Fatalf("row %d: got %d, want %d", i, v, i)
		}
	}

	if _, ok := scan.Next(); ok {
		t.Error("expected second Next to return false")
	}
}

func TestScanMultipleBatches(t *testing.T) {
	// Plan Step 2 test: 5000 rows → 5 batches (1024, 1024, 1024, 1024, 904).
	const total = 5000
	rg := makeIntRowGroup(t, total)
	scan, _ := NewScanOp(rg, []string{"x"})

	wantSizes := []int{1024, 1024, 1024, 1024, 904}
	seen := 0
	batchCount := 0
	for {
		b, ok := scan.Next()
		if !ok {
			break
		}
		if batchCount >= len(wantSizes) {
			t.Fatalf("too many batches; got at least %d", batchCount+1)
		}
		if b.Len() != wantSizes[batchCount] {
			t.Errorf("batch %d: Len = %d, want %d", batchCount, b.Len(), wantSizes[batchCount])
		}
		// Verify values are the correct contiguous slice.
		ints := b.Vectors[0].Int64s()
		for i, v := range ints {
			if v != int64(seen+i) {
				t.Fatalf("batch %d row %d: got %d, want %d", batchCount, i, v, seen+i)
			}
		}
		seen += b.Len()
		batchCount++
	}
	if seen != total {
		t.Errorf("total rows seen = %d, want %d", seen, total)
	}
	if batchCount != len(wantSizes) {
		t.Errorf("batch count = %d, want %d", batchCount, len(wantSizes))
	}
	if scan.RowsEmitted() != total {
		t.Errorf("RowsEmitted = %d, want %d", scan.RowsEmitted(), total)
	}
}

func TestScanExactlyVectorSize(t *testing.T) {
	rg := makeIntRowGroup(t, VectorSize)
	scan, _ := NewScanOp(rg, []string{"x"})

	b, ok := scan.Next()
	if !ok || b.Len() != VectorSize {
		t.Fatalf("first batch: ok=%v len=%d", ok, b.Len())
	}
	if _, ok := scan.Next(); ok {
		t.Error("expected second Next to return false after exact-multiple batch")
	}
}

func TestScanPreservesNulls(t *testing.T) {
	rg := makeIntRowGroup(t, 2000, 5, 17, 1023, 1024, 1999)
	scan, _ := NewScanOp(rg, []string{"x"})

	nullAbsPositions := map[int]bool{5: true, 17: true, 1023: true, 1024: true, 1999: true}
	absRow := 0
	for {
		b, ok := scan.Next()
		if !ok {
			break
		}
		v := b.Vectors[0]
		for i := 0; i < v.Len(); i++ {
			wantNull := nullAbsPositions[absRow]
			if got := v.IsNull(i); got != wantNull {
				t.Errorf("row %d (batch-local %d): IsNull = %v, want %v", absRow, i, got, wantNull)
			}
			absRow++
		}
	}
	if absRow != 2000 {
		t.Errorf("scanned %d rows, want 2000", absRow)
	}
}

func TestScanProjection(t *testing.T) {
	// Build a 3-column row group, request only 2 columns in reordered form.
	a := storage.NewColumnChunkNoNulls("a", storage.NewInt64ColumnFromSlice([]int64{1, 2, 3}))
	b := storage.NewColumnChunkNoNulls("b", storage.NewStringColumnFromSlice([]string{"x", "y", "z"}))
	c := storage.NewColumnChunkNoNulls("c", storage.NewFloat64ColumnFromSlice([]float64{1.5, 2.5, 3.5}))
	rg, err := storage.NewRowGroup(a, b, c)
	if err != nil {
		t.Fatal(err)
	}

	scan, err := NewScanOp(rg, []string{"c", "a"}) // reordered, dropping "b"
	if err != nil {
		t.Fatal(err)
	}
	batch, ok := scan.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	if batch.NumColumns() != 2 {
		t.Fatalf("NumColumns = %d, want 2", batch.NumColumns())
	}
	if batch.Vectors[0].Type != storage.TypeFloat64 {
		t.Errorf("col 0 type = %s, want Float64", batch.Vectors[0].Type)
	}
	if batch.Vectors[1].Type != storage.TypeInt64 {
		t.Errorf("col 1 type = %s, want Int64", batch.Vectors[1].Type)
	}
	if got := batch.Vectors[0].Float64s(); got[0] != 1.5 || got[2] != 3.5 {
		t.Errorf("col 0 values = %v", got)
	}
	if got := batch.Vectors[1].Int64s(); got[0] != 1 || got[2] != 3 {
		t.Errorf("col 1 values = %v", got)
	}
}

func TestScanMultiColumnAlignment(t *testing.T) {
	// Two columns across a 3000-row group (3 batches). Per-row alignment
	// must hold: row i in vectors[0] corresponds to row i in vectors[1].
	const n = 3000
	ints := make([]int64, n)
	strs := make([]string, n)
	for i := range n {
		ints[i] = int64(i * 10)
		strs[i] = string(rune('a' + i%26))
	}
	a := storage.NewColumnChunkNoNulls("a", storage.NewInt64ColumnFromSlice(ints))
	b := storage.NewColumnChunkNoNulls("b", storage.NewStringColumnFromSlice(strs))
	rg, _ := storage.NewRowGroup(a, b)

	scan, _ := NewScanOp(rg, []string{"a", "b"})
	absRow := 0
	for {
		bt, ok := scan.Next()
		if !ok {
			break
		}
		is := bt.Vectors[0].Int64s()
		ss := bt.Vectors[1].Strings()
		if len(is) != len(ss) {
			t.Fatalf("batch column length mismatch: %d vs %d", len(is), len(ss))
		}
		for i := range is {
			if is[i] != int64(absRow*10) {
				t.Fatalf("row %d col a = %d, want %d", absRow, is[i], absRow*10)
			}
			if ss[i] != string(rune('a'+absRow%26)) {
				t.Fatalf("row %d col b = %q", absRow, ss[i])
			}
			absRow++
		}
	}
	if absRow != n {
		t.Errorf("total = %d, want %d", absRow, n)
	}
}

func TestScanReset(t *testing.T) {
	rg := makeIntRowGroup(t, 2500)
	scan, _ := NewScanOp(rg, []string{"x"})

	// Drain once.
	for {
		if _, ok := scan.Next(); !ok {
			break
		}
	}
	if scan.RowsEmitted() != 2500 {
		t.Fatalf("first pass: RowsEmitted = %d", scan.RowsEmitted())
	}

	scan.Reset()
	if scan.RowsEmitted() != 0 {
		t.Errorf("after Reset: RowsEmitted = %d, want 0", scan.RowsEmitted())
	}

	// Drain again, verify it produces the same data.
	absRow := 0
	for {
		b, ok := scan.Next()
		if !ok {
			break
		}
		for i, v := range b.Vectors[0].Int64s() {
			if v != int64(absRow+i) {
				t.Fatalf("second pass row %d: got %d, want %d", absRow+i, v, absRow+i)
			}
		}
		absRow += b.Len()
	}
	if absRow != 2500 {
		t.Errorf("second pass total = %d, want 2500", absRow)
	}
}

func TestScanColumnNotFound(t *testing.T) {
	rg := makeIntRowGroup(t, 100)
	if _, err := NewScanOp(rg, []string{"missing"}); err == nil {
		t.Error("expected error for missing column")
	}
}

func TestScanEmptyColumnsList(t *testing.T) {
	rg := makeIntRowGroup(t, 100)
	if _, err := NewScanOp(rg, nil); err == nil {
		t.Error("expected error for empty columns list")
	}
}

func TestScanNilRowGroup(t *testing.T) {
	if _, err := NewScanOp(nil, []string{"x"}); err == nil {
		t.Error("expected error for nil row group")
	}
}

func TestScanZeroAllocPerBatch(t *testing.T) {
	// Scan's per-batch hot path must not allocate. If this regresses,
	// Step 6's benchmark target is dead on arrival.
	rg := makeIntRowGroup(t, VectorSize*4) // 4 full batches
	scan, _ := NewScanOp(rg, []string{"x"})

	// Warm up (first drain may include one-time setup costs we don't measure).
	for {
		if _, ok := scan.Next(); !ok {
			break
		}
	}

	allocs := testing.AllocsPerRun(20, func() {
		scan.Reset()
		for {
			b, ok := scan.Next()
			if !ok {
				break
			}
			_ = b.Len()
		}
	})
	if allocs != 0 {
		t.Errorf("Scan drain allocs/op = %v, want 0", allocs)
	}
}

func TestScanAllNullsAcrossBatchBoundary(t *testing.T) {
	// Every row is null, and we span a batch boundary. Exercises the null
	// copy path for a non-trivial range at non-zero start offset.
	const n = 2000
	nullIdx := make([]int, n)
	for i := range n {
		nullIdx[i] = i
	}
	rg := makeIntRowGroup(t, n, nullIdx...)
	scan, _ := NewScanOp(rg, []string{"x"})

	absRow := 0
	for {
		b, ok := scan.Next()
		if !ok {
			break
		}
		v := b.Vectors[0]
		for i := 0; i < v.Len(); i++ {
			if !v.IsNull(i) {
				t.Fatalf("row %d (batch-local %d) not null", absRow, i)
			}
			absRow++
		}
	}
	if absRow != n {
		t.Errorf("scanned %d rows, want %d", absRow, n)
	}
}

func TestScanResetMidDrain(t *testing.T) {
	// Calling Reset before exhaustion must still rewind cleanly.
	rg := makeIntRowGroup(t, 2500)
	scan, _ := NewScanOp(rg, []string{"x"})

	// Consume one batch, then reset before exhaustion.
	if _, ok := scan.Next(); !ok {
		t.Fatal("expected first batch")
	}
	scan.Reset()
	if scan.RowsEmitted() != 0 {
		t.Fatalf("RowsEmitted after mid-drain Reset = %d", scan.RowsEmitted())
	}

	// Full drain should now see all 2500 rows starting from 0.
	absRow := 0
	for {
		b, ok := scan.Next()
		if !ok {
			break
		}
		for i, v := range b.Vectors[0].Int64s() {
			if v != int64(absRow+i) {
				t.Fatalf("row %d: got %d, want %d", absRow+i, v, absRow+i)
			}
		}
		absRow += b.Len()
	}
	if absRow != 2500 {
		t.Errorf("total after mid-drain Reset = %d, want 2500", absRow)
	}
}

func TestScanAllTypes(t *testing.T) {
	n := 10
	ints := make([]int64, n)
	floats := make([]float64, n)
	strs := make([]string, n)
	bools := make([]bool, n)
	for i := range n {
		ints[i] = int64(i)
		floats[i] = float64(i) + 0.5
		strs[i] = string(rune('A' + i))
		bools[i] = i%2 == 0
	}
	ci := storage.NewColumnChunkNoNulls("i", storage.NewInt64ColumnFromSlice(ints))
	cf := storage.NewColumnChunkNoNulls("f", storage.NewFloat64ColumnFromSlice(floats))
	cs := storage.NewColumnChunkNoNulls("s", storage.NewStringColumnFromSlice(strs))
	cb := storage.NewColumnChunkNoNulls("b", storage.NewBoolColumnFromSlice(bools))
	rg, _ := storage.NewRowGroup(ci, cf, cs, cb)

	scan, _ := NewScanOp(rg, []string{"i", "f", "s", "b"})
	batch, ok := scan.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	if got := batch.Vectors[0].Int64s(); got[3] != 3 {
		t.Errorf("int col: %v", got)
	}
	if got := batch.Vectors[1].Float64s(); got[3] != 3.5 {
		t.Errorf("float col: %v", got)
	}
	if got := batch.Vectors[2].Strings(); got[3] != "D" {
		t.Errorf("string col: %v", got)
	}
	if got := batch.Vectors[3].Bools(); got[3] {
		t.Errorf("bool col: %v (row 3 should be false)", got)
	}
}
