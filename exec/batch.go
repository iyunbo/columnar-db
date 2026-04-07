package exec

// Batch is a set of aligned Vectors — one per output column — that all
// share the same row count, plus a Selection marking which rows are
// still "alive" after the operators applied so far.
//
// Alignment invariant: every Vector in Vectors has the same Len() (the
// raw row count, e.g. 1024). Row i refers to the same tuple across
// columns. Operators that shrink a batch do so by narrowing Sel rather
// than by resizing individual Vectors — this is late materialization
// in action: data stays put, only the index list into it shrinks.
//
// Sel is always non-nil post-construction. Scan initializes it to
// cover every row in the batch; Filter narrows it; consumers iterate
// via `for _, i := range b.Sel.Indices()`.
type Batch struct {
	// Vectors holds one Vector per output column, in the schema order
	// returned by the producing operator.
	Vectors []*Vector

	// Sel marks live row indices inside the batch. Non-nil post-
	// construction; initialized to NewFullSelection by Scan and
	// narrowed by Filter.
	Sel *Selection
}

// Len returns the number of live rows in this batch (after filters).
// Returns 0 for a batch with no columns.
func (b *Batch) Len() int {
	if b.Sel != nil {
		return b.Sel.Len()
	}
	if len(b.Vectors) == 0 {
		return 0
	}
	return b.Vectors[0].Len()
}

// NumColumns returns the number of columns in this batch.
func (b *Batch) NumColumns() int {
	return len(b.Vectors)
}

// Reset clears every vector in the batch and empties the selection,
// preparing it for reuse across a new iteration. Allocation-free: each
// Vector.Reset truncates in place and Selection.Reset is a slice-header
// reset.
func (b *Batch) Reset() {
	for _, v := range b.Vectors {
		v.Reset()
	}
	if b.Sel != nil {
		b.Sel.Reset()
	}
}
