package exec

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// makeIntRowGroupVals builds a 1-column Int64 row group with the given
// exact values. Lets tests craft specific predicate scenarios without
// being tied to the 0..n-1 pattern of makeIntRowGroup.
func makeIntRowGroupVals(t *testing.T, vals []int64, nullIdx ...int) *storage.RowGroup {
	t.Helper()
	col := storage.NewInt64ColumnFromSlice(vals)
	nulls := storage.NewNullBitmap(len(vals))
	for _, i := range nullIdx {
		nulls.SetNull(i)
	}
	chunk, err := storage.NewColumnChunk("x", col, nulls)
	if err != nil {
		t.Fatal(err)
	}
	rg, err := storage.NewRowGroup(chunk)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

func TestFilterSingleBatchGt(t *testing.T) {
	// Plan Step 4 test: 10000 rows, value > 5000 → 4999 survivors.
	const n = 10000
	rg := makeIntRowGroup(t, n) // values 0..n-1
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 5000})
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for {
		b, ok := filter.Next()
		if !ok {
			break
		}
		vals := b.Vectors[0].Int64s()
		for _, i := range b.Sel.Indices() {
			v := vals[i]
			if v <= 5000 {
				t.Fatalf("value %d ≤ 5000 survived filter", v)
			}
			seen++
		}
	}
	if seen != 4999 {
		t.Errorf("survivors = %d, want 4999", seen)
	}
}

func TestFilterSingleMatchEq(t *testing.T) {
	// Plan test: value == 42 → exactly 1 row.
	rg := makeIntRowGroup(t, 10000)
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Eq{Value: 42})

	total := 0
	var matchedValue int64 = -1
	for {
		b, ok := filter.Next()
		if !ok {
			break
		}
		vals := b.Vectors[0].Int64s()
		for _, i := range b.Sel.Indices() {
			matchedValue = vals[i]
			total++
		}
	}
	if total != 1 {
		t.Errorf("match count = %d, want 1", total)
	}
	if matchedValue != 42 {
		t.Errorf("matched value = %d, want 42", matchedValue)
	}
}

func TestFilterNullsNeverMatch(t *testing.T) {
	// Rows 5, 10, 15 are null. Ge=0 would normally match all rows.
	rg := makeIntRowGroup(t, 20, 5, 10, 15)
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Ge{Value: 0})

	seen := 0
	for {
		b, ok := filter.Next()
		if !ok {
			break
		}
		for _, i := range b.Sel.Indices() {
			if i == 5 || i == 10 || i == 15 {
				t.Errorf("null row %d survived filter", i)
			}
			seen++
		}
	}
	if seen != 17 {
		t.Errorf("survivors = %d, want 17", seen)
	}
}

func TestFilterFullyFilteredBatchesSkipped(t *testing.T) {
	// Spread 3 matching rows across a 3000-row group (3 scan batches).
	// Batches that match nothing must be silently skipped: filter.Next()
	// should never return a batch with Len()==0, and total survivors = 3.
	vals := make([]int64, 3000)
	for i := range vals {
		vals[i] = -1 // no row will satisfy > 1000 except the three we plant
	}
	vals[10] = 9999   // in batch 0
	vals[1500] = 9999 // in batch 1
	vals[2800] = 9999 // in batch 2
	rg := makeIntRowGroupVals(t, vals)
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Gt{Value: 1000})

	batches := 0
	total := 0
	for {
		b, ok := filter.Next()
		if !ok {
			break
		}
		if b.Len() == 0 {
			t.Fatal("filter returned an empty batch")
		}
		batches++
		total += b.Len()
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if batches != 3 {
		t.Errorf("batches = %d, want 3", batches)
	}
}

func TestFilterEntirelyEmptyResult(t *testing.T) {
	// Nothing matches — filter should just drain the child and return
	// (nil, false) on the first call, no panic.
	rg := makeIntRowGroup(t, 1000)
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Gt{Value: 10000})

	if b, ok := filter.Next(); ok {
		t.Errorf("expected (nil,false), got batch len %d", b.Len())
	}
}

func TestFilterCrossBatch(t *testing.T) {
	// Values 0..4999 over 5 batches. Filter > 2000 → survivors 2001..4999
	// (2999 rows) distributed across batches 2, 3, 4 (and partially 1).
	const n = 5000
	rg := makeIntRowGroup(t, n)
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Gt{Value: 2000})

	// Collect all surviving values in order.
	got := []int64{}
	for {
		b, ok := filter.Next()
		if !ok {
			break
		}
		vals := b.Vectors[0].Int64s()
		for _, i := range b.Sel.Indices() {
			got = append(got, vals[i])
		}
	}
	if len(got) != 2999 {
		t.Errorf("len = %d, want 2999", len(got))
	}
	for i, v := range got {
		want := int64(2001 + i)
		if v != want {
			t.Fatalf("got[%d] = %d, want %d", i, v, want)
		}
	}
}

func TestFilterReset(t *testing.T) {
	rg := makeIntRowGroup(t, 3000)
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Ge{Value: 1000})

	drain := func() int {
		total := 0
		for {
			b, ok := filter.Next()
			if !ok {
				break
			}
			total += b.Len()
		}
		return total
	}

	first := drain()
	if first != 2000 {
		t.Fatalf("first pass: %d, want 2000", first)
	}

	filter.Reset()
	second := drain()
	if second != 2000 {
		t.Errorf("after Reset: %d, want 2000", second)
	}
}

func TestFilterNilChild(t *testing.T) {
	if _, err := NewFilterOp(nil, 0, Int64Eq{Value: 0}); err == nil {
		t.Error("expected error for nil child")
	}
}

func TestFilterNilPredicate(t *testing.T) {
	rg := makeIntRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"x"})
	if _, err := NewFilterOp(scan, 0, nil); err == nil {
		t.Error("expected error for nil predicate")
	}
}

func TestFilterNegativeColumnIndex(t *testing.T) {
	rg := makeIntRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"x"})
	if _, err := NewFilterOp(scan, -1, Int64Eq{Value: 0}); err == nil {
		t.Error("expected error for negative column index")
	}
}

func TestFilterBatchShapePreserved(t *testing.T) {
	// FilterOp must leave the batch's vector data untouched — only Sel
	// changes. A row dropped by the filter should still be physically
	// present in the Vector, just not indexed by Sel.
	rg := makeIntRowGroup(t, 100) // 0..99
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Gt{Value: 50})

	b, ok := filter.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	if b.Vectors[0].Len() != 100 {
		t.Errorf("vector shrunk to %d, want 100 (data must stay put)", b.Vectors[0].Len())
	}
	// Row 0 is still physically in the vector but must not be in Sel.
	if got := b.Vectors[0].Int64s()[0]; got != 0 {
		t.Errorf("vector row 0 = %d, want 0 (data unchanged)", got)
	}
	for _, i := range b.Sel.Indices() {
		if i <= 50 {
			t.Errorf("row %d ≤ 50 in Sel after filter > 50", i)
		}
	}
}

func TestFilterZeroAllocSteadyState(t *testing.T) {
	rg := makeIntRowGroup(t, VectorSize*4) // 4 full batches
	scan, _ := NewScanOp(rg, []string{"x"})
	filter, _ := NewFilterOp(scan, 0, Int64Gt{Value: 1000})

	// Warm up: one full drain primes all backing slices.
	for {
		if _, ok := filter.Next(); !ok {
			break
		}
	}

	allocs := testing.AllocsPerRun(20, func() {
		filter.Reset()
		for {
			b, ok := filter.Next()
			if !ok {
				break
			}
			_ = b.Len()
		}
	})
	if allocs != 0 {
		t.Errorf("filter drain allocs/op = %v, want 0", allocs)
	}
}

func TestFilterMultiColumnProjection(t *testing.T) {
	// Filter on column 0, but Scan also carries a 2nd column for free.
	// After filter, the 2nd column's data must still be addressable via
	// the shared Sel (alignment invariant).
	nums := make([]int64, 100)
	names := make([]string, 100)
	for i := range nums {
		nums[i] = int64(i)
		names[i] = "row-" + string(rune('A'+i%26))
	}
	numCol := storage.NewColumnChunkNoNulls("num", storage.NewInt64ColumnFromSlice(nums))
	nameCol := storage.NewColumnChunkNoNulls("name", storage.NewStringColumnFromSlice(names))
	rg, _ := storage.NewRowGroup(numCol, nameCol)

	scan, _ := NewScanOp(rg, []string{"num", "name"})
	filter, _ := NewFilterOp(scan, 0, Int64Eq{Value: 42})

	b, ok := filter.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	if b.Sel.Len() != 1 {
		t.Fatalf("Sel.Len = %d, want 1", b.Sel.Len())
	}
	row := b.Sel.Indices()[0]
	if got := b.Vectors[0].Int64s()[row]; got != 42 {
		t.Errorf("num col row = %d, want 42", got)
	}
	if got := b.Vectors[1].Strings()[row]; got != "row-"+string(rune('A'+42%26)) {
		t.Errorf("name col row = %q", got)
	}
}
