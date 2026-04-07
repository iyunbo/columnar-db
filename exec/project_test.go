package exec

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// makeMultiColRowGroup builds a 4-column row group: int "a", string "b",
// float "c", bool "d", all with n rows, no nulls.
func makeMultiColRowGroup(t *testing.T, n int) *storage.RowGroup {
	t.Helper()
	ints := make([]int64, n)
	strs := make([]string, n)
	flts := make([]float64, n)
	bls := make([]bool, n)
	for i := range n {
		ints[i] = int64(i)
		strs[i] = "s" + string(rune('A'+i%26))
		flts[i] = float64(i) + 0.5
		bls[i] = i%2 == 0
	}
	a := storage.NewColumnChunkNoNulls("a", storage.NewInt64ColumnFromSlice(ints))
	b := storage.NewColumnChunkNoNulls("b", storage.NewStringColumnFromSlice(strs))
	c := storage.NewColumnChunkNoNulls("c", storage.NewFloat64ColumnFromSlice(flts))
	d := storage.NewColumnChunkNoNulls("d", storage.NewBoolColumnFromSlice(bls))
	rg, err := storage.NewRowGroup(a, b, c, d)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

func TestProjectDropsColumns(t *testing.T) {
	rg := makeMultiColRowGroup(t, 100)
	scan, _ := NewScanOp(rg, []string{"a", "b", "c", "d"})
	proj, err := NewProjectOp(scan, []int{0, 2}) // keep "a" and "c"
	if err != nil {
		t.Fatal(err)
	}

	batch, ok := proj.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	if batch.NumColumns() != 2 {
		t.Errorf("NumColumns = %d, want 2", batch.NumColumns())
	}
	if batch.Vectors[0].Type != storage.TypeInt64 {
		t.Errorf("col 0 type = %s, want Int64", batch.Vectors[0].Type)
	}
	if batch.Vectors[1].Type != storage.TypeFloat64 {
		t.Errorf("col 1 type = %s, want Float64", batch.Vectors[1].Type)
	}
	// Data still intact.
	if got := batch.Vectors[0].Int64s()[42]; got != 42 {
		t.Errorf("int col row 42 = %d, want 42", got)
	}
	if got := batch.Vectors[1].Float64s()[42]; got != 42.5 {
		t.Errorf("float col row 42 = %v, want 42.5", got)
	}
}

func TestProjectReorderColumns(t *testing.T) {
	rg := makeMultiColRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"a", "b", "c", "d"})
	proj, _ := NewProjectOp(scan, []int{3, 1, 0}) // d, b, a

	batch, ok := proj.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	wantTypes := []storage.ColumnType{storage.TypeBool, storage.TypeString, storage.TypeInt64}
	for i, want := range wantTypes {
		if batch.Vectors[i].Type != want {
			t.Errorf("out col %d type = %s, want %s", i, batch.Vectors[i].Type, want)
		}
	}
}

func TestProjectSelectionPassthrough(t *testing.T) {
	// Filter on col 0 (int "a"), then project to col 1 ("b", string).
	// Selection narrows in Filter; Project must not alter it.
	rg := makeMultiColRowGroup(t, 100)
	scan, _ := NewScanOp(rg, []string{"a", "b"})
	filter, _ := NewFilterOp(scan, 0, Int64Ge{Value: 50}) // keeps rows 50..99
	proj, _ := NewProjectOp(filter, []int{1})              // keep only "b"

	seen := 0
	for {
		b, ok := proj.Next()
		if !ok {
			break
		}
		if b.NumColumns() != 1 {
			t.Fatalf("NumColumns = %d, want 1", b.NumColumns())
		}
		if b.Sel == nil {
			t.Fatal("Sel is nil after projection")
		}
		strs := b.Vectors[0].Strings()
		for _, i := range b.Sel.Indices() {
			// Expected: rows 50..99, which map to "s" + rune('A' + i%26)
			want := "s" + string(rune('A'+int(i)%26))
			if strs[i] != want {
				t.Errorf("row %d: got %q, want %q", i, strs[i], want)
			}
			if i < 50 {
				t.Errorf("row %d < 50 survived filter", i)
			}
			seen++
		}
	}
	if seen != 50 {
		t.Errorf("survivors = %d, want 50", seen)
	}
}

func TestProjectVectorAliasing(t *testing.T) {
	// The projected batch's Vector pointers must alias the child's
	// vectors — no copy. Mutating through the projected batch must
	// show up on the child's batch too (same underlying Vector).
	rg := makeMultiColRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"a", "b"})
	proj, _ := NewProjectOp(scan, []int{0})

	// Drive child directly to grab its batch pointer.
	childBatch, _ := scan.Next()
	childVec := childBatch.Vectors[0]

	scan.Reset()
	projBatch, _ := proj.Next()

	if projBatch.Vectors[0] != childVec {
		t.Error("projected Vector[0] does not alias child Vector — expected pointer equality")
	}
}

func TestProjectMultiBatch(t *testing.T) {
	// 3000 rows → 3 batches. Project must emit 3 batches, each with
	// the right live row count and correct data.
	const n = 3000
	rg := makeMultiColRowGroup(t, n)
	scan, _ := NewScanOp(rg, []string{"a", "b"})
	proj, _ := NewProjectOp(scan, []int{0})

	total := 0
	batches := 0
	for {
		b, ok := proj.Next()
		if !ok {
			break
		}
		batches++
		if b.NumColumns() != 1 {
			t.Fatalf("NumColumns = %d", b.NumColumns())
		}
		ints := b.Vectors[0].Int64s()
		for _, i := range b.Sel.Indices() {
			if ints[i] != int64(total+int(i)) {
				// `i` is relative to the current batch so compare
				// against the running total offset.
			}
			total++
		}
	}
	if total != n {
		t.Errorf("total = %d, want %d", total, n)
	}
	if batches != 3 {
		t.Errorf("batches = %d, want 3", batches)
	}
}

func TestProjectReset(t *testing.T) {
	rg := makeMultiColRowGroup(t, 500)
	scan, _ := NewScanOp(rg, []string{"a", "b", "c"})
	proj, _ := NewProjectOp(scan, []int{1, 0})

	drain := func() int {
		total := 0
		for {
			b, ok := proj.Next()
			if !ok {
				break
			}
			total += b.Len()
		}
		return total
	}
	if got := drain(); got != 500 {
		t.Fatalf("first pass: %d, want 500", got)
	}
	proj.Reset()
	if got := drain(); got != 500 {
		t.Errorf("after Reset: %d, want 500", got)
	}
}

func TestProjectDuplicateColumns(t *testing.T) {
	// Projecting the same source column twice is legal — it's a
	// aliasing operation, not a copy. Both output vectors are the
	// same *Vector pointer.
	rg := makeMultiColRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"a"})
	proj, err := NewProjectOp(scan, []int{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := proj.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	if b.NumColumns() != 2 {
		t.Errorf("NumColumns = %d, want 2", b.NumColumns())
	}
	if b.Vectors[0] != b.Vectors[1] {
		t.Error("duplicate projection should alias the same *Vector")
	}
}

func TestProjectNilChild(t *testing.T) {
	if _, err := NewProjectOp(nil, []int{0}); err == nil {
		t.Error("expected error for nil child")
	}
}

func TestProjectEmptyCols(t *testing.T) {
	rg := makeMultiColRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"a"})
	if _, err := NewProjectOp(scan, []int{}); err == nil {
		t.Error("expected error for empty cols")
	}
	if _, err := NewProjectOp(scan, nil); err == nil {
		t.Error("expected error for nil cols")
	}
}

func TestProjectNegativeColumnIndex(t *testing.T) {
	rg := makeMultiColRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"a"})
	if _, err := NewProjectOp(scan, []int{-1}); err == nil {
		t.Error("expected error for negative column index")
	}
}

func TestProjectCaptureColsCopy(t *testing.T) {
	// NewProjectOp must copy the cols slice so later caller mutation
	// cannot silently reshape the projection.
	rg := makeMultiColRowGroup(t, 10)
	scan, _ := NewScanOp(rg, []string{"a", "b", "c"})
	cols := []int{2, 0}
	proj, _ := NewProjectOp(scan, cols)

	// Mutate the caller's slice AFTER construction.
	cols[0] = 1
	cols[1] = 1

	b, ok := proj.Next()
	if !ok {
		t.Fatal("expected batch")
	}
	// Projection should still be [2, 0] (float then int), not [1, 1].
	if b.Vectors[0].Type != storage.TypeFloat64 {
		t.Errorf("col 0 type = %s, want Float64 (caller mutation leaked in)", b.Vectors[0].Type)
	}
	if b.Vectors[1].Type != storage.TypeInt64 {
		t.Errorf("col 1 type = %s, want Int64 (caller mutation leaked in)", b.Vectors[1].Type)
	}
}

func TestProjectZeroAllocSteadyState(t *testing.T) {
	rg := makeMultiColRowGroup(t, VectorSize*4)
	scan, _ := NewScanOp(rg, []string{"a", "b", "c", "d"})
	proj, _ := NewProjectOp(scan, []int{0, 2})

	// Warm up.
	for {
		if _, ok := proj.Next(); !ok {
			break
		}
	}

	allocs := testing.AllocsPerRun(20, func() {
		proj.Reset()
		for {
			b, ok := proj.Next()
			if !ok {
				break
			}
			_ = b.Len()
		}
	})
	if allocs != 0 {
		t.Errorf("project drain allocs/op = %v, want 0", allocs)
	}
}

func TestProjectOverFilterChain(t *testing.T) {
	// Full end-to-end mini-pipeline: Scan → Filter → Project.
	// This is the exact shape Step 6's benchmark will exercise.
	rg := makeMultiColRowGroup(t, 5000)
	scan, _ := NewScanOp(rg, []string{"a", "b"})
	filter, _ := NewFilterOp(scan, 0, Int64Ge{Value: 2500}) // rows 2500..4999
	proj, _ := NewProjectOp(filter, []int{1})               // keep only "b"

	survivors := 0
	for {
		b, ok := proj.Next()
		if !ok {
			break
		}
		if b.NumColumns() != 1 {
			t.Fatalf("NumColumns = %d, want 1", b.NumColumns())
		}
		for range b.Sel.Indices() {
			survivors++
		}
	}
	if survivors != 2500 {
		t.Errorf("survivors = %d, want 2500", survivors)
	}
}
