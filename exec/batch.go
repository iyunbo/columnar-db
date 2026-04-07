package exec

// Batch is a set of aligned Vectors — one per output column — that all
// share the same row count. It is the unit of data that flows between
// operators in the vectorized execution engine.
//
// Alignment invariant: every Vector in Vectors has the same Len(). Row i
// refers to the same tuple across columns. Operators that shrink a batch
// do so via Selection (Step 3), not by resizing individual Vectors —
// that keeps column slices independent and cache-friendly.
type Batch struct {
	// Vectors holds one Vector per output column, in the schema order
	// returned by the producing operator.
	Vectors []*Vector
}

// Len returns the number of live rows in this batch. Returns 0 for a
// batch with no columns.
func (b *Batch) Len() int {
	if len(b.Vectors) == 0 {
		return 0
	}
	return b.Vectors[0].Len()
}

// NumColumns returns the number of columns in this batch.
func (b *Batch) NumColumns() int {
	return len(b.Vectors)
}

// Reset clears every vector in the batch, preparing it for reuse across
// a new iteration. Allocation-free: each Vector.Reset truncates in place.
func (b *Batch) Reset() {
	for _, v := range b.Vectors {
		v.Reset()
	}
}
