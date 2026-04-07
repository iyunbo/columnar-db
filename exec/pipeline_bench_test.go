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

// benchRG is shared across every pipeline benchmark via sync.Once.
// It MUST be treated as read-only. No operator in this package
// currently mutates a RowGroup it scans, but if that ever changes
// the fixture must be rebuilt per benchmark, not shared.

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

// --- Compound predicate benchmarks (Phase 3 polish #2) ---
//
// Profiling Phase 3 Step 6 (see docs/phase3-profiling.md) showed the
// vectorized engine's overhead is dominated by selection-vector
// materialization inside Int64Gt.Eval. The hypothesis is that this
// overhead pays off when the selection is *reused* by a second
// downstream operator: vectorized only iterates surviving indices,
// while the row-at-a-time baseline must evaluate both predicates per
// row regardless. The compound query 30 < age < 60 (over a uniform
// 0..89 distribution, ~33% selectivity) is the smallest fair test.
//
// Twin benchmarks: same logical query, two implementations.

// naiveRowAtATimeCountCompound is the row-at-a-time baseline for the
// compound query: count rows where lo < age < hi. Both predicates are
// evaluated for every non-null row — there is no selection vector to
// "skip ahead" with.
func naiveRowAtATimeCountCompound(rg *storage.RowGroup, lo, hi int64) int {
	ageChunk := rg.ColumnByName("age")
	ageVals := ageChunk.Values.(*storage.Int64Column)
	ageNulls := ageChunk.Nulls

	count := 0
	for i := range rg.RowCount {
		if ageNulls.IsNull(i) {
			continue
		}
		a := ageVals.Get(i)
		if a > lo && a < hi {
			count++
		}
	}
	return count
}

// BenchmarkPipelineNaiveCountCompound is the naive twin of the
// vectorized compound benchmark below.
func BenchmarkPipelineNaiveCountCompound(b *testing.B) {
	rg := getBenchRG(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = naiveRowAtATimeCountCompound(rg, 30, 60)
	}
}

// BenchmarkPipelineVectorizedDrainCompound is the compound vectorized
// pipeline: Scan(age) → Filter(age > 30) → Filter(age < 60) → drain.
// The second FilterOp's Eval only walks the surviving indices in the
// selection vector — that's the late-materialization win we are trying
// to demonstrate. If this benchmark is faster than (or competitive
// with) BenchmarkPipelineNaiveCountCompound, the negative result from
// Phase 3 Step 6 is bounded to single-predicate queries and the
// architecture is vindicated for any pipeline with two or more filters.
func BenchmarkPipelineVectorizedDrainCompound(b *testing.B) {
	rg := getBenchRG(b)

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		b.Fatal(err)
	}
	filter1, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		b.Fatal(err)
	}
	filter2, err := NewFilterOp(filter1, 0, Int64Lt{Value: 60})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	var survivors int
	for b.Loop() {
		filter2.Reset()
		survivors = 0
		for {
			batch, ok := filter2.Next()
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

// naiveRowAtATimeCountTriple is the row-at-a-time baseline for the
// 3-predicate query lo < age < hi AND age != mid.
func naiveRowAtATimeCountTriple(rg *storage.RowGroup, lo, hi, mid int64) int {
	ageChunk := rg.ColumnByName("age")
	ageVals := ageChunk.Values.(*storage.Int64Column)
	ageNulls := ageChunk.Nulls

	count := 0
	for i := range rg.RowCount {
		if ageNulls.IsNull(i) {
			continue
		}
		a := ageVals.Get(i)
		if a > lo && a < hi && a != mid {
			count++
		}
	}
	return count
}

// BenchmarkPipelineNaiveCountTriple measures the naive baseline with
// three predicates. Used to find the crossover point with the
// vectorized engine — the 2-predicate compound benchmark already shows
// vectorized's marginal cost per added predicate is lower than naive's
// (~2.05 ms vs ~3.43 ms), and a third predicate should make the gap
// visible in absolute numbers.
func BenchmarkPipelineNaiveCountTriple(b *testing.B) {
	rg := getBenchRG(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = naiveRowAtATimeCountTriple(rg, 30, 60, 45)
	}
}

// BenchmarkPipelineVectorizedDrainTriple is the 3-filter vectorized
// pipeline: Scan(age) → Filter(>30) → Filter(<60) → Filter(!=45) →
// drain. Filter 2 and Filter 3 only walk surviving indices via the
// selection vector — exactly the late-materialization win.
func BenchmarkPipelineVectorizedDrainTriple(b *testing.B) {
	rg := getBenchRG(b)

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		b.Fatal(err)
	}
	f1, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		b.Fatal(err)
	}
	f2, err := NewFilterOp(f1, 0, Int64Lt{Value: 60})
	if err != nil {
		b.Fatal(err)
	}
	f3, err := NewFilterOp(f2, 0, Int64Ne{Value: 45})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	var survivors int
	for b.Loop() {
		f3.Reset()
		survivors = 0
		for {
			batch, ok := f3.Next()
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

// BenchmarkPipelineVectorizedDrainPushedDown is the optimistic
// ceiling, not the current cost: Scan reads ONLY the "age" column
// (as if a planner had pushed the projection into Scan), no
// ProjectOp is stacked on top. Directly comparable to
// BenchmarkPipelineNaiveCount and reveals the irreducible per-batch
// overhead of the vectorized model on a single-column count query.
// The real pipeline does not yet push projection into Scan
// automatically — see docs/phase3-benchmark-results.md follow-up #1.
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
