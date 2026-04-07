package exec

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// makeInt64Vector builds a 1-vector populated with values 0..n-1 and
// optional null indices. Returns the vector directly for predicate tests.
func makeInt64Vector(t *testing.T, n int, nullIdx ...int) *Vector {
	t.Helper()
	v := NewVector(storage.TypeInt64)
	for i := range n {
		if err := v.AppendInt64(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, idx := range nullIdx {
		v.Nulls.SetNull(idx)
	}
	return v
}

func TestInt64PredicatesNullFree(t *testing.T) {
	v := makeInt64Vector(t, 100) // values 0..99, no nulls
	in := NewFullSelection(100)
	out := NewSelection()

	cases := []struct {
		name  string
		pred  Predicate
		pass  func(i int) bool
		count int
	}{
		{"Eq=42", Int64Eq{Value: 42}, func(i int) bool { return i == 42 }, 1},
		{"Ne=42", Int64Ne{Value: 42}, func(i int) bool { return i != 42 }, 99},
		{"Lt=10", Int64Lt{Value: 10}, func(i int) bool { return i < 10 }, 10},
		{"Le=10", Int64Le{Value: 10}, func(i int) bool { return i <= 10 }, 11},
		{"Gt=90", Int64Gt{Value: 90}, func(i int) bool { return i > 90 }, 9},
		{"Ge=90", Int64Ge{Value: 90}, func(i int) bool { return i >= 90 }, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.pred.Eval(v, in, out)
			if out.Len() != tc.count {
				t.Errorf("Len = %d, want %d", out.Len(), tc.count)
			}
			// Each surviving index must itself satisfy the predicate.
			for _, i := range out.Indices() {
				if !tc.pass(int(i)) {
					t.Errorf("index %d in out but does not satisfy predicate", i)
				}
			}
		})
	}
}

func TestInt64PredicatesSkipNulls(t *testing.T) {
	// values 0..9, rows 0 and 5 are null
	v := makeInt64Vector(t, 10, 0, 5)
	in := NewFullSelection(10)
	out := NewSelection()

	// Ge=0 would normally match every row. Nulls at 0 and 5 must be dropped.
	Int64Ge{Value: 0}.Eval(v, in, out)
	if out.Len() != 8 {
		t.Errorf("Ge=0 with 2 nulls: Len = %d, want 8", out.Len())
	}
	for _, i := range out.Indices() {
		if i == 0 || i == 5 {
			t.Errorf("null row %d survived filter", i)
		}
	}
}

func TestInt64PredicatesEmptyInput(t *testing.T) {
	v := makeInt64Vector(t, 100)
	in := NewSelection() // empty
	out := NewSelection()
	Int64Gt{Value: 50}.Eval(v, in, out)
	if out.Len() != 0 {
		t.Errorf("empty in, Len = %d, want 0", out.Len())
	}
}

func TestInt64PredicatesResetsOut(t *testing.T) {
	// Eval must Reset() its output before filling — callers shouldn't
	// need to clear it manually.
	v := makeInt64Vector(t, 10)
	in := NewFullSelection(10)
	out := NewSelection()
	out.Add(999) // pre-populate — should be gone after Eval

	Int64Eq{Value: 3}.Eval(v, in, out)
	if out.Len() != 1 || out.Indices()[0] != 3 {
		t.Errorf("after Eval, out = %v", out.Indices())
	}
}

func TestInt64PredicatesNarrowedInputOnly(t *testing.T) {
	// If the input selection is already narrowed (e.g. by a previous
	// filter), Eval must only test those rows — never leak in rows
	// that the upstream filter dropped.
	v := makeInt64Vector(t, 20) // 0..19
	in := NewSelection()
	for _, i := range []int{2, 5, 10, 15} {
		in.Add(i)
	}
	out := NewSelection()
	Int64Ge{Value: 0}.Eval(v, in, out) // matches all
	if out.Len() != 4 {
		t.Errorf("Len = %d, want 4", out.Len())
	}
	got := out.Indices()
	want := []uint16{2, 5, 10, 15}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("out[%d] = %d, want %d", i, got[i], w)
		}
	}
}

func TestInt64PredicateNoMatchOnNonEmptyInput(t *testing.T) {
	// Non-empty input selection, predicate matches nothing. Distinct
	// from TestInt64PredicatesEmptyInput which passes an empty input.
	v := makeInt64Vector(t, 100) // 0..99
	in := NewFullSelection(100)
	out := NewSelection()
	out.Add(777) // pre-populate — must be reset

	Int64Gt{Value: 1000}.Eval(v, in, out)
	if out.Len() != 0 {
		t.Errorf("Len = %d, want 0", out.Len())
	}
}

func TestInt64PredicateAllNulls(t *testing.T) {
	// Every row is null. Any predicate should drop every row.
	v := NewVector(storage.TypeInt64)
	for i := range 20 {
		if err := v.AppendNull(); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	in := NewFullSelection(20)
	out := NewSelection()

	// Ge=MinInt64 would match any non-null value. With all nulls, none
	// should survive.
	Int64Ge{Value: -1 << 62}.Eval(v, in, out)
	if out.Len() != 0 {
		t.Errorf("all-null Ge: Len = %d, want 0", out.Len())
	}
}

func TestInt64PredicateZeroAllocEval(t *testing.T) {
	// The filter hot path must be alloc-free. This is the primary
	// guarantee that Step 6's benchmark target is reachable.
	v := makeInt64Vector(t, VectorSize)
	in := NewFullSelection(VectorSize)
	out := NewSelection()
	// Prime out to its full size once so backing capacity is stable.
	for i := 0; i < VectorSize; i++ {
		out.AppendUint16Unchecked(uint16(i))
	}
	out.Reset()

	allocs := testing.AllocsPerRun(100, func() {
		Int64Gt{Value: 500}.Eval(v, in, out)
	})
	if allocs != 0 {
		t.Errorf("Int64Gt.Eval allocs/op = %v, want 0", allocs)
	}
}

func TestInt64PredicateWithNullsZeroAlloc(t *testing.T) {
	// Same guarantee for the null-aware branch.
	v := makeInt64Vector(t, VectorSize, 0, 100, 500, 1000)
	in := NewFullSelection(VectorSize)
	out := NewSelection()

	allocs := testing.AllocsPerRun(100, func() {
		Int64Gt{Value: 500}.Eval(v, in, out)
	})
	if allocs != 0 {
		t.Errorf("null-aware Eval allocs/op = %v, want 0", allocs)
	}
}
