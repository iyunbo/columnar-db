package exec

import "github.com/iyunbo/columnar-db/storage"

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
//	    a.Finalize(g, outVec, g)   // write group g's result into row g of outVec
//	}
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
//   - Finalize writes the result for the given group into out at the
//     given row. Zero allocation. Caller pre-sized out to numGroups.
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
	Update(vec *Vector, sel *Selection, groupIDs []int32)
	OutputType() storage.ColumnType
	Finalize(group int, out *Vector, row int)
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
// Phase 4 Step 1: stub only — fields and constructor signature exist
// so dependent code (the architecture diagram, the Step 2 PRs) can
// reference the type, but Next/Reset and constructor are not
// implemented yet.
//
// The implementation lands in Step 3.
//
// TODO(Step 3): the output schema (column names + Vector types)
// must be known at construction so `out *Batch` can be allocated
// once. Likely shape: NewAggregateOp(child, []namedAggregator) with
// each aggregator carrying its OutputType().
type AggregateOp struct {
	child Operator
	aggs  []Aggregator
	// out is the single-row result Batch, allocated once at
	// construction so steady-state Next() does not allocate.
	out *Batch
	// done is true after the first Next() returns the result;
	// subsequent calls return (nil, false) until Reset.
	done bool
}
