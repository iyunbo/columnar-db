package exec

// Aggregator is the contract every aggregate function (COUNT, SUM,
// AVG, MIN, MAX, ...) implements. Aggregators are called by
// AggregateOp and (later) GroupByOp once per input batch.
//
// The lifecycle is:
//
//	a := someAggregator()
//	a.Init()                       // reset state
//	for each batch from upstream:
//	    a.Update(vec, sel)         // accumulate over selected rows
//	result := a.Finalize()         // produce final value
//
// Init() may allocate. Update() must not allocate on the steady-state
// hot path — the per-batch loop is the place vectorized aggregation
// is supposed to win, and any per-batch allocation defeats the model
// before it gets to demonstrate it.
//
// Update is passed:
//   - vec: the input column vector. Aggregators that operate on a
//     specific type (e.g. Int64Sum) must check vec.Type and panic on
//     mismatch — type checking happens once at construction in
//     AggregateOp/GroupByOp, not per batch.
//   - sel: the live selection vector. Aggregators must iterate
//     sel.Indices(), not 0..vec.Len(), so upstream FilterOps actually
//     filter.
//
// Finalize is allowed to return any concrete Go type (int64, float64,
// or nil for "no rows seen") because the result lands in a single-row
// output Batch where the column type is determined by the aggregator
// at planning time.
//
// Phase 4 Step 1: this is the interface only. Concrete implementations
// land in Step 2.
type Aggregator interface {
	Init()
	Update(vec *Vector, sel *Selection)
	Finalize() any
}

// AggregateOp is the scalar (non-grouping) aggregation operator. It
// pulls batches from a child operator, calls Update on each
// configured Aggregator per batch, and emits a single one-row Batch
// containing the finalized values on the first Next(); subsequent
// Next() calls return (nil, false).
//
// Pipeline shape:
//
//	Scan → Filter → AggregateOp → consumer
//
// Phase 4 Step 1: stub only — fields and constructor signature exist
// so dependent code (the architecture diagram, the Step 2 PRs) can
// reference the type, but Next/Reset are not implemented yet.
//
// The implementation lands in Step 3.
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
