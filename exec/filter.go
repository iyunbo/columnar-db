package exec

import "fmt"

// FilterOp wraps a child Operator and narrows each batch's Selection
// using a Predicate evaluated over one of its columns. Vectors are
// never copied, mutated, or resized — only the batch's Sel changes.
// This is the canonical late-materialization step: row data stays put,
// the index list into it shrinks.
//
// Batches that are fully filtered out are silently skipped: FilterOp
// pulls the next child batch and tries again, so Next() never returns
// a live batch with zero rows. When the child is exhausted FilterOp
// returns (nil, false), matching the Operator contract.
//
// Target column is specified by its index into the child's output
// batch, not by name. The caller knows the schema produced by the
// operator below (a ScanOp's columns list, a ProjectOp's projection
// list) and resolves names to indices at pipeline-construction time.
// This matches how real engines hand filter operators pre-resolved
// column positions during query planning.
type FilterOp struct {
	child Operator
	col   int
	pred  Predicate

	// tempSel is a scratch selection used as the output buffer for
	// pred.Eval. Swapped with the child batch's Sel on every Next()
	// call — no copying, no allocation in steady state.
	tempSel *Selection
}

// NewFilterOp builds a filter over the given child. `col` is the index
// of the target column in the batches the child produces; `pred` is
// the boolean test to apply.
func NewFilterOp(child Operator, col int, pred Predicate) (*FilterOp, error) {
	if child == nil {
		return nil, fmt.Errorf("exec: NewFilterOp: nil child")
	}
	if col < 0 {
		return nil, fmt.Errorf("exec: NewFilterOp: negative column index %d", col)
	}
	if pred == nil {
		return nil, fmt.Errorf("exec: NewFilterOp: nil predicate")
	}
	return &FilterOp{
		child:   child,
		col:     col,
		pred:    pred,
		tempSel: NewSelection(),
	}, nil
}

// Next returns the next batch whose selection is non-empty after the
// predicate has been applied. Fully-filtered batches are skipped. The
// returned *Batch pointer aliases the child's reused batch — consume
// or copy before the next Next().
func (f *FilterOp) Next() (*Batch, bool) {
	for {
		b, ok := f.child.Next()
		if !ok {
			return nil, false
		}
		if f.col >= len(b.Vectors) {
			panic(fmt.Sprintf("exec: FilterOp.Next: col %d ≥ batch columns %d", f.col, len(b.Vectors)))
		}

		// Evaluate into the scratch selection; swap to make it the
		// batch's live Sel. This is strictly faster than CopyFrom
		// because it's O(1) pointer swap vs O(n) memmove.
		f.pred.Eval(b.Vectors[f.col], b.Sel, f.tempSel)
		b.Sel, f.tempSel = f.tempSel, b.Sel

		if b.Sel.Len() == 0 {
			continue // fully filtered — ask the child for another batch
		}
		return b, true
	}
}

// Reset rewinds the child and clears the scratch selection.
func (f *FilterOp) Reset() {
	f.child.Reset()
	f.tempSel.Reset()
}
