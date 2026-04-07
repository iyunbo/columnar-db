package exec

import "fmt"

// ProjectOp wraps a child Operator and drops columns the query does
// not need. The output batch carries only the requested columns, in
// the order the caller specified. Vectors are not copied — the output
// Batch's Vectors slice holds pointers to the same underlying
// *Vector objects the child emits. Selection is shared by reference
// so filters below (or above, in unusual stacks) see a coherent view.
//
// Canonical use: Filter needs a column for its predicate that the
// final consumer doesn't want. ScanOp reads both; Filter narrows on
// the predicate column; Project drops it from the downstream view.
// Example:
//
//	SELECT name FROM t WHERE age > 30
//	  ScanOp:   reads "age" and "name"
//	  FilterOp: predicate on age → narrows Sel
//	  ProjectOp: drops "age", keeps only "name"
//
// Push-down note: when Project sits directly above Scan, the caller
// should push the projection into NewScanOp's column list instead of
// stacking a ProjectOp — Scan then never decodes the dropped columns
// at all. That's pure push-down and costs nothing. ProjectOp exists
// for the cases where something else (a filter, a join) sits between
// Scan and the projection.
type ProjectOp struct {
	child Operator
	cols  []int // column indices into the child's output batch

	// out is the reused projected batch. Its Vectors slice is filled
	// on every Next() by pointer-aliasing the child's vectors; Sel
	// is replaced with the child's Sel pointer directly.
	out *Batch
}

// NewProjectOp builds a projection over `child`. `cols` is the list of
// column indices (into the child's output batch) to pass through, in
// the desired output order. Returns an error on nil child, empty cols,
// or negative indices. Out-of-range column indices (too large for the
// child's schema) are not caught here — the current Operator contract
// does not expose a schema accessor — and will surface as a panic on
// the first Next() call.
func NewProjectOp(child Operator, cols []int) (*ProjectOp, error) {
	if child == nil {
		return nil, fmt.Errorf("exec: NewProjectOp: nil child")
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("exec: NewProjectOp: no columns requested")
	}
	for _, c := range cols {
		if c < 0 {
			return nil, fmt.Errorf("exec: NewProjectOp: negative column index %d", c)
		}
	}
	// Copy cols so later caller mutation can't silently reshape the
	// projection.
	colsCopy := make([]int, len(cols))
	copy(colsCopy, cols)

	return &ProjectOp{
		child: child,
		cols:  colsCopy,
		out:   &Batch{Vectors: make([]*Vector, len(cols))},
	}, nil
}

// Next pulls the next batch from the child and returns a projected
// view: only the requested columns, in the requested order, sharing
// the same Sel. The returned *Batch pointer is stable across calls
// (same one every time); its Vectors slice and Sel field are rebound
// each call to point at the current child batch's state. Consume or
// copy before calling Next() again.
func (p *ProjectOp) Next() (*Batch, bool) {
	b, ok := p.child.Next()
	if !ok {
		return nil, false
	}
	for i, col := range p.cols {
		if col >= len(b.Vectors) {
			// TODO(step6): validate cols in NewProjectOp once the
			// Operator interface exposes a Schema() accessor. Same
			// deferred check as FilterOp.
			panic(fmt.Sprintf("exec: ProjectOp.Next: col %d ≥ batch columns %d", col, len(b.Vectors)))
		}
		p.out.Vectors[i] = b.Vectors[col]
	}
	// Share the child's Sel by pointer. Selection is row-level and
	// survives projection unchanged — dropping columns never changes
	// which rows are alive.
	p.out.Sel = b.Sel
	return p.out, true
}

// Reset rewinds the child operator.
func (p *ProjectOp) Reset() {
	p.child.Reset()
}
