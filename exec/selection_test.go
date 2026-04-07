package exec

import (
	"testing"
)

func TestNewSelectionEmpty(t *testing.T) {
	s := NewSelection()
	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
	if len(s.Indices()) != 0 {
		t.Errorf("Indices len = %d, want 0", len(s.Indices()))
	}
}

func TestNewFullSelection(t *testing.T) {
	cases := []int{0, 1, 100, 1023, VectorSize}
	for _, n := range cases {
		s := NewFullSelection(n)
		if s.Len() != n {
			t.Errorf("n=%d: Len = %d", n, s.Len())
		}
		for i, idx := range s.Indices() {
			if int(idx) != i {
				t.Fatalf("n=%d index %d: got %d, want %d", n, i, idx, i)
			}
		}
	}
}

func TestNewFullSelectionOutOfRange(t *testing.T) {
	for _, n := range []int{-1, VectorSize + 1, 10000} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("n=%d: expected panic", n)
				}
			}()
			NewFullSelection(n)
		}()
	}
}

func TestSelectionAdd(t *testing.T) {
	s := NewSelection()
	// Simulate "keep every 3rd row" filter over 30 rows.
	for i := 0; i < 30; i++ {
		if i%3 == 0 {
			s.Add(i)
		}
	}
	if s.Len() != 10 {
		t.Errorf("Len = %d, want 10", s.Len())
	}
	for i, idx := range s.Indices() {
		want := uint16(i * 3)
		if idx != want {
			t.Errorf("indices[%d] = %d, want %d", i, idx, want)
		}
	}
}

func TestSelectionAddOutOfRange(t *testing.T) {
	for _, i := range []int{-1, VectorSize, VectorSize + 1, 10000} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("i=%d: expected panic", i)
				}
			}()
			s := NewSelection()
			s.Add(i)
		}()
	}
}

func TestSelectionReset(t *testing.T) {
	s := NewSelection()
	for i := 0; i < 500; i++ {
		s.Add(i)
	}
	s.Reset()
	if s.Len() != 0 {
		t.Errorf("after Reset Len = %d, want 0", s.Len())
	}
	// Still usable and independent of previous fill.
	s.Add(42)
	if s.Len() != 1 || s.Indices()[0] != 42 {
		t.Errorf("after Reset+Add: indices = %v", s.Indices())
	}
}

func TestSelectionResetFull(t *testing.T) {
	s := NewSelection()
	// Pre-populate with arbitrary data to ensure ResetFull overwrites.
	s.Add(999)
	s.Add(1000)
	s.Add(1001)

	s.ResetFull(5)
	if s.Len() != 5 {
		t.Fatalf("Len = %d, want 5", s.Len())
	}
	for i, idx := range s.Indices() {
		if int(idx) != i {
			t.Errorf("indices[%d] = %d, want %d", i, idx, i)
		}
	}

	// Can shrink then grow — should still produce clean output.
	s.ResetFull(2)
	if got := s.Indices(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("after ResetFull(2): %v", got)
	}
	s.ResetFull(10)
	for i, idx := range s.Indices() {
		if int(idx) != i {
			t.Errorf("after ResetFull(10) index %d = %d, want %d", i, idx, i)
		}
	}
}

func TestSelectionResetFullOutOfRange(t *testing.T) {
	for _, n := range []int{-1, VectorSize + 1} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("n=%d: expected panic", n)
				}
			}()
			s := NewSelection()
			s.ResetFull(n)
		}()
	}
}

func TestSelectionZeroAllocReuse(t *testing.T) {
	// Selection is supposed to be allocation-free after construction for
	// the Reset + fill cycle. Guard the invariant — Filter's hot path
	// depends on it.
	s := NewSelection()
	// Prime.
	for i := 0; i < VectorSize; i++ {
		s.Add(i)
	}
	s.Reset()

	allocs := testing.AllocsPerRun(50, func() {
		s.Reset()
		for i := 0; i < VectorSize; i++ {
			if i%2 == 0 {
				s.Add(i)
			}
		}
	})
	if allocs != 0 {
		t.Errorf("Reset+fill allocs/op = %v, want 0", allocs)
	}
}

func TestSelectionResetFullZeroAlloc(t *testing.T) {
	s := NewFullSelection(VectorSize)

	// Exercise ResetFull across varying sizes — each must be alloc-free.
	for _, n := range []int{0, 1, 100, 512, 1023, VectorSize} {
		allocs := testing.AllocsPerRun(50, func() {
			s.ResetFull(n)
		})
		if allocs != 0 {
			t.Errorf("ResetFull(%d) allocs/op = %v, want 0", n, allocs)
		}
	}
}

func TestSelectionFirstFillZeroAlloc(t *testing.T) {
	// A fresh NewSelection is cap=VectorSize, so the first fill up to
	// VectorSize must also be alloc-free (not only post-Reset refills).
	//
	// Note: AllocsPerRun makes multiple calls; use a fresh Selection in
	// each closure invocation to measure the first-fill path specifically,
	// and hold the result to make sure the compiler doesn't elide it.
	var sink *Selection
	allocs := testing.AllocsPerRun(50, func() {
		s := NewSelection()
		for i := 0; i < VectorSize; i++ {
			s.Add(i)
		}
		sink = s
	})
	_ = sink
	// 2 allocs expected: the Selection struct on the heap + the backing
	// []uint16 slice. The fill loop on top must add nothing beyond those.
	if allocs > 2 {
		t.Errorf("NewSelection + full Add loop allocs/op = %v, want ≤ 2", allocs)
	}
}

func TestSelectionIndicesAliasInternal(t *testing.T) {
	// The doc comment promises Indices() aliases internal state and is
	// valid until the next Reset/Add. Verify that mutating through the
	// returned slice actually changes what the selection sees.
	s := NewFullSelection(5)
	idx := s.Indices()
	idx[0] = 42
	if s.Indices()[0] != 42 {
		t.Error("Indices() does not alias internal state")
	}
}
