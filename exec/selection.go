package exec

import "fmt"

// Selection marks which rows of a Batch are "alive" — i.e. have passed all
// filters applied so far — without copying or rearranging the underlying
// data. It is the core mechanism behind late materialization: instead of
// physically shrinking Vectors after each filter, we carry a compact list
// of surviving indices alongside the unchanged batch, and downstream
// operators iterate only over those indices.
//
// Representation: a dense []uint16 of row positions inside the current
// batch. Row indices ∈ [0, VectorSize), so uint16 (11 bits needed) is
// plenty and half the size of uint32 — denser cache footprint for the
// filter hot path.
//
// Why index array and not a bitmap? Bitmaps are compact when selectivity
// is high (most rows alive) but force branch-heavy iteration for every
// consumer. An index array is optimal when selectivity is low or moderate
// (the common case after a predicate) because consumers can do
//
//	for _, i := range sel.Indices() { ... }
//
// and touch only live rows. We may add a bitmap variant alongside later
// if profiling justifies it.
//
// A Selection is always bound to a single Batch and is reused across
// batches via Reset, same zero-alloc recycling pattern as Vector/Batch.
type Selection struct {
	indices []uint16
}

// NewSelection creates an empty selection with backing capacity for a
// full batch. No rows are marked alive yet — call Add or NewFullSelection
// to populate.
func NewSelection() *Selection {
	return &Selection{indices: make([]uint16, 0, VectorSize)}
}

// NewFullSelection creates a selection covering rows [0, n). Equivalent
// to calling NewSelection and Add(0..n-1) but done as one slice fill.
// Panics if n > VectorSize or n < 0.
func NewFullSelection(n int) *Selection {
	s := NewSelection()
	s.ResetFull(n) // single source of truth for the fill loop
	return s
}

// Len returns the number of live rows in this selection.
func (s *Selection) Len() int { return len(s.indices) }

// Add marks row i as alive. Panics if i is out of range for a batch
// (< 0 or ≥ VectorSize). Does not check for duplicates — the filter
// hot path must not add the same row twice, and guarding against it
// would cost an extra branch per row.
func (s *Selection) Add(i int) {
	if i < 0 || i >= VectorSize {
		panic(fmt.Sprintf("exec: Selection.Add index %d out of range [0,%d)", i, VectorSize))
	}
	s.indices = append(s.indices, uint16(i))
}

// Indices returns the underlying []uint16 of live row positions. Safe
// to iterate with a tight `for _, i := range sel.Indices()` loop —
// this is the hot path shape expected by Filter and downstream operators.
// The returned slice aliases internal state and is valid until the next
// Reset or Add.
func (s *Selection) Indices() []uint16 { return s.indices }

// Reset clears the selection for reuse without allocation. Backing
// capacity (VectorSize) is preserved.
func (s *Selection) Reset() { s.indices = s.indices[:0] }

// ResetFull truncates and refills the selection to cover rows [0, n) in
// place — the allocation-free fast path for Scan → new-batch handoff
// where every row is initially alive before any filter runs. Panics if
// n > VectorSize or n < 0.
func (s *Selection) ResetFull(n int) {
	if n < 0 || n > VectorSize {
		panic(fmt.Sprintf("exec: Selection.ResetFull n=%d out of range [0,%d]", n, VectorSize))
	}
	s.indices = s.indices[:n]
	for i := range n {
		s.indices[i] = uint16(i)
	}
}
