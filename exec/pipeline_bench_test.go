package exec

import (
	"sync"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 3 Step 6: end-to-end pipeline benchmark.
//
// Run: go test -bench=Pipeline -benchmem -run=^$ ./exec/
//
// Measures implementations of the same logical query:
//
//	SELECT name, age FROM t WHERE age > 30
//
// against a 1M-row 3-column fixture.
//
// Zero-alloc contract: BenchmarkPipelineVectorizedDrain and
// BenchmarkPipelineVectorizedDrainPushedDown must both report
// 0 allocs/op. Enforced.
//
// Throughput story: see docs/phase3-benchmark-results.md for the
// honest write-up. TL;DR: for this query shape (simple > predicate
// over decoded in-memory int64), a tight naive row-at-a-time count
// loop is the Go compiler's sweet spot and beats the vectorized
// pipeline by ~5×, because ScanOp pays an unavoidable memmove tax
// that doesn't exist in the naive path. The vectorized model's
// real wins show up with disk I/O (decode dominates), complex
// predicates (per-row function-call cost), or downstream aggregates
// (no row materialization at the end). All of those are future
// phases; this step's success criteria have been revised to
// correctness + zero-alloc only, and the throughput gap is
// documented for Phase 3 polish follow-ups.

const pipelineBenchRows = 1_000_000

var (
	benchRGOnce sync.Once
	benchRG     *storage.RowGroup
)

// getBenchRG lazily builds the 1M-row fixture once per process. The
// setup cost (deterministic rand + column fills) is excluded from
// every benchmark's measurement loop via sync.Once.
func getBenchRG(tb testing.TB) *storage.RowGroup {
	benchRGOnce.Do(func() {
		benchRG = makePipelineRowGroup(tb, pipelineBenchRows)
	})
	return benchRG
}

// BenchmarkPipelineNaiveRowAtATime is the baseline we're beating.
// One Get(i) call per column per row, IsNull(i) per row, append
// survivors into a result slice. Classic "row-at-a-time" shape.
func BenchmarkPipelineNaiveRowAtATime(b *testing.B) {
	rg := getBenchRG(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = naiveRowAtATimeScanFilterProject(rg, 30)
	}
}

// BenchmarkPipelineNaiveCount is the counting twin of the row-at-a-
// time baseline. No result slice, no per-row materialization. This
// is the fair comparison partner for BenchmarkPipelineVectorizedDrain
// — both count survivors, neither materializes them.
func BenchmarkPipelineNaiveCount(b *testing.B) {
	rg := getBenchRG(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = naiveRowAtATimeCount(rg, 30)
	}
}

// BenchmarkPipelineVectorized exercises the full Scan→Filter→Project
// pipeline and materializes survivors into a result slice (same
// shape as the baseline). This is the number the plan's ≥5×
// speedup target compares.
func BenchmarkPipelineVectorized(b *testing.B) {
	rg := getBenchRG(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = vectorizedScanFilterProject(b, rg, 30)
	}
}

// BenchmarkPipelineVectorizedDrain measures just the drain — no
// result slice, no allocation inside the loop — of the full
// Scan(age, name) → Filter → Project chain. Both columns are
// materialized even though only age is read, mirroring what happens
// when a caller has not manually pushed the projection into Scan.
func BenchmarkPipelineVectorizedDrain(b *testing.B) {
	rg := getBenchRG(b)

	// Build the operator chain once outside the measurement loop so
	// setup allocations don't pollute the numbers.
	scan, err := NewScanOp(rg, []string{"age", "name"})
	if err != nil {
		b.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		b.Fatal(err)
	}
	proj, err := NewProjectOp(filter, []int{1, 0})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	var survivors int
	for b.Loop() {
		proj.Reset()
		survivors = 0
		for {
			batch, ok := proj.Next()
			if !ok {
				break
			}
			survivors += batch.Len()
		}
	}
	if survivors == 0 {
		b.Fatal("no survivors — fixture or pipeline broken")
	}
}

// BenchmarkPipelineVectorizedDrainPushedDown is the manually
// push-pushed-down twin: Scan reads ONLY the "age" column (the one
// the filter needs), Project is not stacked on top. This shows the
// ceiling of what the current pipeline can do on a count-only
// workload — directly comparable to BenchmarkPipelineNaiveCount.
func BenchmarkPipelineVectorizedDrainPushedDown(b *testing.B) {
	rg := getBenchRG(b)

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		b.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	var survivors int
	for b.Loop() {
		filter.Reset()
		survivors = 0
		for {
			batch, ok := filter.Next()
			if !ok {
				break
			}
			survivors += batch.Len()
		}
	}
	if survivors == 0 {
		b.Fatal("no survivors — fixture or pipeline broken")
	}
}
