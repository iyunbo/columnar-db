package exec

import (
	"fmt"

	"github.com/iyunbo/columnar-db/storage"
)

// Aggregator is the contract every aggregate function (COUNT, SUM,
// AVG, MIN, MAX, ...) implements. The shape is **columnar per-group
// state** (the MonetDB/DuckDB layout), not single-state.
//
// Why columnar state:
//
//	Both AggregateOp (scalar, one group) and GroupByOp (N groups)
//	use the same interface. The aggregator owns a typed slice of
//	per-group state — e.g. Int64Sum owns []int64 sums and []int64
//	counts, indexed by group ordinal. Scalar aggregation is just
//	the special case numGroups == 1. This avoids the Step 4
//	"break the Step 2 interface" trap the reviewer flagged on
//	PR #36.
//
// Lifecycle:
//
//	a := newSomeAggregator()
//	a.Init(numGroups)              // allocate per-group state slices
//	for each batch from upstream:
//	    a.Update(vec, sel, gids)   // accumulate into state[gids[i]]
//	for g := 0; g < numGroups; g++ {
//	    a.Finalize(g, outVec)      // append group g's result to outVec
//	}
//
// **Output sizing**: callers must ensure outVec has capacity for at
// least numGroups appends. AggregateOp/GroupByOp will size their
// result Vector accordingly. The current Vector implementation has
// VectorSize=1024 capacity, so a single result Vector cannot hold
// more than 1024 groups; Step 4's GroupByOp will need to either
// emit multiple result batches or grow Vector capacity. Filed as a
// Step 4 design point.
//
// Shape rules:
//
//   - Init(numGroups) allocates. May be called once at construction
//     for scalar AggregateOp (numGroups=1), or as the hash table is
//     sized in GroupByOp.
//
//   - Grow(numGroups) extends per-group state when GroupByOp's hash
//     table grows. May allocate. Existing state must be preserved.
//
//   - Update is the steady-state hot path and **must not allocate**.
//     It iterates sel.Indices() and updates state[groupIDs[i]] for
//     each selected row. For scalar aggregation, GroupByOp passes
//     groupIDs == nil and the aggregator uses group 0 for every row.
//
//   - OutputType returns the column type of the final result, so
//     AggregateOp/GroupByOp can pre-allocate the result Vector at
//     construction (no per-batch type-switch dance).
//
//   - Finalize APPENDS the result for the given group to out. Callers
//     must call Finalize in group order (0, 1, ..., numGroups-1) so
//     out's length grows linearly. (GroupByOp may need random-access
//     Set semantics in Step 4 — filed as a TODO. For Step 2/3 the
//     append contract is enough.) Finalize sets a null in out's
//     bitmap when the group has no non-null inputs.
//
//   - Aggregators that operate on a specific input type (e.g.
//     Int64Sum) check vec.Type once in Update and panic on mismatch.
//     Type checking happens at planning time when AggregateOp/
//     GroupByOp wires up its aggregators, not per row.
//
// Phase 4 Step 1: this is the interface only. Concrete
// implementations (Count, Sum, Min, Max, Avg over int64/float64)
// land in Step 2.
type Aggregator interface {
	Init(numGroups int)
	Grow(numGroups int)
	// Reset zeroes per-group state in place WITHOUT reallocating.
	// Used by AggregateOp.Reset between query iterations to avoid
	// allocating per-iteration on the b.Loop hot path of Step 6's
	// benchmark. Implementations must keep numGroups capacity intact
	// (do not nil out the state slices).
	Reset()
	Update(vec *Vector, sel *Selection, groupIDs []int32)
	OutputType() storage.ColumnType
	Finalize(group int, out *Vector)
}

// AggregateSpec describes one aggregator instance: which input column
// it consumes, the aggregator implementation, and the output column
// name (informational — Batch does not yet carry names; the name lives
// here for self-documentation and for future use by SQL planning in
// Phase 5).
//
// CountStar ignores the input column entirely (it counts selected
// rows from sel.Len()), so for CountStar specs the ColIndex must
// still be a valid index in the child's batch but can be any column.
// Convention: pass 0.
type AggregateSpec struct {
	Name     string // result column name
	ColIndex int    // input column index in child batches
	Agg      Aggregator
}

// AggregateOp is the scalar (non-grouping) aggregation operator. It
// pulls batches from a child operator, calls Update on each
// configured Aggregator with groupIDs == nil (so every selected row
// lands in the single group 0), and emits a one-row Batch containing
// the finalized values on the first Next(); subsequent Next() calls
// return (nil, false) until Reset.
//
// Pipeline shape:
//
//	Scan → Filter → AggregateOp → consumer
//
// Lifecycle:
//
//	op, _ := NewAggregateOp(child, []AggregateSpec{...})
//	for {
//	    b, ok := op.Next()
//	    if !ok { break }
//	    // b is a 1-row batch with one Vector per spec.
//	    // For multiple iterations, op.Reset() between drains.
//	}
//
// Reset semantics: each aggregator is re-Init'd (which allocates a
// fresh per-group state slice). This is acceptable per-query overhead;
// the steady-state hot path (Update inside Next's drain loop) is
// where the zero-alloc contract matters.
type AggregateOp struct {
	child Operator
	specs []AggregateSpec
	// out is the single-row result Batch, allocated once at
	// construction so the result emission step does not allocate.
	out *Batch
	// done is true after the first Next() returns the result;
	// subsequent calls return (nil, false) until Reset.
	done bool
}

// NewAggregateOp builds a scalar aggregation operator over child.
// At least one spec is required. Each Aggregator instance may be
// referenced by at most one spec — duplicate pointers are rejected
// because each spec writes to its own output Vector and Init/Reset
// would clobber each other's state.
//
// TODO: the CountStar-takes-ColIndex-it-ignores API smell. Cleanest
// fix is a sentinel ColIndex of -1 plus an `agg.NeedsInput() bool`
// optional interface, but that requires flipping the negative-index
// validation. Filed as a Step 5 / planning cleanup.
func NewAggregateOp(child Operator, specs []AggregateSpec) (*AggregateOp, error) {
	if err := validateAggregateSpecs("AggregateOp", specs); err != nil {
		return nil, err
	}
	out := &Batch{
		Vectors: make([]*Vector, len(specs)),
		Sel:     NewSelection(),
	}
	for i, s := range specs {
		out.Vectors[i] = NewVector(s.Agg.OutputType())
		s.Agg.Init(1)
	}
	return &AggregateOp{
		child: child,
		specs: specs,
		out:   out,
	}, nil
}

// validateAggregateSpecs enforces the shared invariants for any
// operator that accepts a []AggregateSpec (currently AggregateOp and
// GroupByOp/GroupByOpMulti): non-empty, no nil Agg, non-negative
// ColIndex, no duplicate Aggregator pointer across specs. `opName`
// is interpolated into the error prefix so the caller knows which
// operator rejected the spec.
func validateAggregateSpecs(opName string, specs []AggregateSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("exec: %s requires at least one aggregator spec", opName)
	}
	seen := make(map[Aggregator]struct{}, len(specs))
	for i, s := range specs {
		if s.Agg == nil {
			return fmt.Errorf("exec: %s AggregateSpec[%d] (%q) has nil Aggregator", opName, i, s.Name)
		}
		if s.ColIndex < 0 {
			return fmt.Errorf("exec: %s AggregateSpec[%d] (%q) has negative ColIndex %d", opName, i, s.Name, s.ColIndex)
		}
		if _, dup := seen[s.Agg]; dup {
			return fmt.Errorf("exec: %s AggregateSpec[%d] (%q) reuses an Aggregator instance already used by an earlier spec; each spec needs its own Aggregator", opName, i, s.Name)
		}
		seen[s.Agg] = struct{}{}
	}
	return nil
}

// Next drains all batches from the child, updating every aggregator
// with each batch's selected rows, then emits a single one-row Batch
// of finalized results. Subsequent calls return (nil, false) until
// Reset.
func (a *AggregateOp) Next() (*Batch, bool) {
	if a.done {
		return nil, false
	}
	for {
		b, ok := a.child.Next()
		if !ok {
			break
		}
		for _, s := range a.specs {
			vec := b.Vectors[s.ColIndex]
			s.Agg.Update(vec, b.Sel, nil)
		}
	}
	// Finalize each aggregator into its result vector. The output
	// vectors were either freshly allocated by NewAggregateOp or
	// freshly Reset; both leave them empty, so a single AppendInt64/
	// AppendFloat64/AppendNull from Finalize lands at row 0.
	for i, s := range a.specs {
		s.Agg.Finalize(0, a.out.Vectors[i])
	}
	a.out.Sel.Reset()
	a.out.Sel.Add(0)
	a.done = true
	return a.out, true
}

// Reset rewinds the operator. Zero allocation: aggregators zero
// their per-group state in place via Aggregator.Reset (not Init,
// which would reallocate the state slices). The output Batch is
// also Reset in place. This matters for Step 6's b.Loop benchmark
// where AggregateOp.Reset runs once per iteration.
func (a *AggregateOp) Reset() {
	a.child.Reset()
	a.out.Reset()
	for _, s := range a.specs {
		s.Agg.Reset()
	}
	a.done = false
}
