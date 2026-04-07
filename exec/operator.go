package exec

// Operator is the universal pull-based contract of the vectorized
// execution engine. Every execution step — scan, filter, projection,
// aggregation — implements it, so pipelines compose by wrapping child
// operators.
//
// Next() returns the next Batch or (nil, false) when exhausted. The
// returned *Batch pointer is typically reused across calls: callers
// must consume or copy its contents before the next Next() invocation.
// This is the standard Volcano-batch contract (a.k.a. "materialized
// vectorized iteration").
//
// Reset() rewinds the operator to the start so the same pipeline can be
// drained again without reconstruction. Used by tests and by the Step 6
// benchmark to measure steady-state throughput.
type Operator interface {
	Next() (*Batch, bool)
	Reset()
}

// Compile-time check: every concrete operator in this package must
// satisfy Operator. Add new operators here as they are introduced.
var (
	_ Operator = (*ScanOp)(nil)
	_ Operator = (*FilterOp)(nil)
)
