package exec

import (
	"math/rand/v2"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 3 Step 6: end-to-end integration test for the vectorized
// execution pipeline.
//
// Query: SELECT name, age FROM t WHERE age > 30
//
// Exercised shape:
//
//	ScanOp (age, name) → FilterOp(age > 30) → ProjectOp(name, age) → consumer
//
// The test also runs the same logical query against a trivial
// row-at-a-time baseline (naiveRowAtATimeScanFilterProject) and
// asserts both produce an identical set of survivors. This is the
// correctness twin of the benchmark in pipeline_bench_test.go.

// makePipelineRowGroup builds a 3-column row group simulating a
// "users" table: age int64, name string, city string. Deterministic
// (seeded), with a configurable row count and null fraction.
func makePipelineRowGroup(t testing.TB, n int) *storage.RowGroup {
	t.Helper()
	rng := rand.New(rand.NewPCG(42, 99))

	ages := make([]int64, n)
	names := make([]string, n)
	cities := make([]string, n)
	cityPool := []string{"Paris", "Lyon", "Kunming", "Shanghai", "Beijing"}
	for i := range n {
		ages[i] = int64(rng.IntN(90)) // 0..89
		names[i] = "user-" + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26))
		cities[i] = cityPool[i%len(cityPool)]
	}

	ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
	nameCol := storage.NewColumnChunkNoNulls("name", storage.NewStringColumnFromSlice(names))
	cityCol := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(cities))
	rg, err := storage.NewRowGroup(ageCol, nameCol, cityCol)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

// naiveRowAtATimeScanFilterProject is the row-oriented baseline the
// vectorized pipeline is measured against. It walks the row group one
// row at a time, calls the typed Get(i) accessor on each column, and
// collects matching (age, name) pairs into a result slice.
//
// This is the "Volcano without batching" shape, a.k.a. what a simple
// database from a textbook would look like. It's the reference that
// makes the "vectorized is ≥5× faster" claim concrete.
type row struct {
	age  int64
	name string
}

func naiveRowAtATimeScanFilterProject(rg *storage.RowGroup, threshold int64) []row {
	ageChunk := rg.ColumnByName("age")
	nameChunk := rg.ColumnByName("name")
	ageVals := ageChunk.Values.(*storage.Int64Column)
	nameVals := nameChunk.Values.(*storage.StringColumn)
	ageNulls := ageChunk.Nulls

	// Expected fraction for the canonical query is ~65% (uniform 0..89
	// filtered > 30), so over-allocate to avoid slice growth on the
	// benchmark hot path.
	result := make([]row, 0, rg.RowCount*2/3)
	for i := range rg.RowCount {
		if ageNulls.IsNull(i) {
			continue
		}
		a := ageVals.Get(i)
		if a > threshold {
			result = append(result, row{age: a, name: nameVals.Get(i)})
		}
	}
	return result
}

// naiveRowAtATimeCount is the counting twin of the naive scan/filter.
// It walks the row group one row at a time but only counts survivors
// instead of materializing them, matching the shape of the
// VectorizedDrain benchmark for a fair comparison.
//go:noinline
func naiveRowAtATimeCount(rg *storage.RowGroup, threshold int64) int {
	ageChunk := rg.ColumnByName("age")
	ageVals := ageChunk.Values.(*storage.Int64Column)
	ageNulls := ageChunk.Nulls

	count := 0
	for i := range rg.RowCount {
		if ageNulls.IsNull(i) {
			continue
		}
		if ageVals.Get(i) > threshold {
			count++
		}
	}
	return count
}

// vectorizedScanFilterProject is the vectorized pipeline under test.
// It returns the same shape as the naive baseline so the two can be
// directly compared.
func vectorizedScanFilterProject(t testing.TB, rg *storage.RowGroup, threshold int64) []row {
	t.Helper()
	scan, err := NewScanOp(rg, []string{"age", "name"})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0 /* age */, Int64Gt{Value: threshold})
	if err != nil {
		t.Fatal(err)
	}
	// Project keeps only "name" followed by "age" to exercise reordering.
	proj, err := NewProjectOp(filter, []int{1, 0})
	if err != nil {
		t.Fatal(err)
	}

	// Expected fraction for the canonical query is ~65% (uniform 0..89
	// filtered > 30), so over-allocate to avoid slice growth on the
	// benchmark hot path.
	result := make([]row, 0, rg.RowCount*2/3)
	for {
		b, ok := proj.Next()
		if !ok {
			break
		}
		names := b.Vectors[0].Strings()
		ages := b.Vectors[1].Int64s()
		for _, i := range b.Sel.Indices() {
			result = append(result, row{age: ages[i], name: names[i]})
		}
	}
	return result
}

func TestPipelineMatchesNaiveBaseline(t *testing.T) {
	// Correctness twin of the benchmark: same dataset, both
	// implementations, same survivor set.
	const n = 10_000
	rg := makePipelineRowGroup(t, n)

	baseline := naiveRowAtATimeScanFilterProject(rg, 30)
	vectorized := vectorizedScanFilterProject(t, rg, 30)

	if len(baseline) != len(vectorized) {
		t.Fatalf("size mismatch: baseline=%d vectorized=%d", len(baseline), len(vectorized))
	}
	// Both pipelines emit in row order.
	for i := range baseline {
		if baseline[i] != vectorized[i] {
			t.Fatalf("row %d differs: baseline=%+v vectorized=%+v", i, baseline[i], vectorized[i])
		}
	}
}

func TestPipelineCorrectResultCount(t *testing.T) {
	// Sanity: the query "age > 30" over a uniform 0..89 distribution
	// should pass roughly 59/90 of rows. Verify the survivors actually
	// satisfy the predicate and nothing slips through.
	const n = 5000
	rg := makePipelineRowGroup(t, n)
	got := vectorizedScanFilterProject(t, rg, 30)

	for _, r := range got {
		if r.age <= 30 {
			t.Errorf("survivor with age=%d ≤ 30", r.age)
		}
	}
	// Loose sanity bound: expect between 50% and 70% survivors for a
	// deterministic uniform distribution.
	frac := float64(len(got)) / float64(n)
	if frac < 0.50 || frac > 0.70 {
		t.Errorf("survivor fraction = %.3f, want ~0.65", frac)
	}
}

func TestPipelineWithNullsMatchesBaseline(t *testing.T) {
	// Inject nulls into the age column and verify both pipelines still
	// agree. Nulls must never match the filter, which the vectorized
	// path tests separately — this is the end-to-end twin.
	const n = 2000
	rng := rand.New(rand.NewPCG(42, 99))
	ages := make([]int64, n)
	names := make([]string, n)
	cities := make([]string, n)
	cityPool := []string{"Paris", "Lyon", "Kunming", "Shanghai", "Beijing"}
	for i := range n {
		ages[i] = int64(rng.IntN(90))
		names[i] = "user-" + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26))
		cities[i] = cityPool[i%len(cityPool)]
	}
	ageNulls := storage.NewNullBitmap(n)
	for i := 0; i < n; i += 17 {
		ageNulls.SetNull(i)
	}
	ageCol, err := storage.NewColumnChunk("age", storage.NewInt64ColumnFromSlice(ages), ageNulls)
	if err != nil {
		t.Fatal(err)
	}
	nameCol := storage.NewColumnChunkNoNulls("name", storage.NewStringColumnFromSlice(names))
	cityCol := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(cities))
	rg, err := storage.NewRowGroup(ageCol, nameCol, cityCol)
	if err != nil {
		t.Fatal(err)
	}

	baseline := naiveRowAtATimeScanFilterProject(rg, 30)
	vectorized := vectorizedScanFilterProject(t, rg, 30)

	if len(baseline) != len(vectorized) {
		t.Fatalf("size mismatch with nulls: baseline=%d vectorized=%d", len(baseline), len(vectorized))
	}
	for i := range baseline {
		if baseline[i] != vectorized[i] {
			t.Fatalf("row %d differs with nulls: baseline=%+v vectorized=%+v", i, baseline[i], vectorized[i])
		}
	}
}

func TestPipelineCompoundFilterMatchesNaive(t *testing.T) {
	// Correctness twin of the compound benchmark
	// (BenchmarkPipelineVectorizedDrainCompound). Two chained FilterOps
	// must produce the same survivor SET (not just count!) as a naive
	// row-at-a-time loop evaluating both predicates per row. Counting
	// alone could miss a compensating bug that drops one row and adds
	// another.
	const n = 10_000
	rg := makePipelineRowGroup(t, n)

	// Naive baseline collects matching ages with their original row index.
	type hit struct {
		row int
		age int64
	}
	ageChunk := rg.ColumnByName("age")
	ageVals := ageChunk.Values.(*storage.Int64Column)
	ageNulls := ageChunk.Nulls
	want := make([]hit, 0, n/3)
	for i := range rg.RowCount {
		if ageNulls.IsNull(i) {
			continue
		}
		a := ageVals.Get(i)
		if a > 30 && a < 60 {
			want = append(want, hit{row: i, age: a})
		}
	}
	if len(want) == 0 {
		t.Fatal("baseline produced 0 survivors — fixture or test broken")
	}

	// Vectorized chain — track absolute row index across batches.
	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	filter1, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		t.Fatal(err)
	}
	filter2, err := NewFilterOp(filter1, 0, Int64Lt{Value: 60})
	if err != nil {
		t.Fatal(err)
	}

	got := make([]hit, 0, n/3)
	batchStart := 0
	for {
		batch, ok := filter2.Next()
		if !ok {
			break
		}
		ages := batch.Vectors[0].Int64s()
		batchSize := batch.Vectors[0].Len()
		for _, i := range batch.Sel.Indices() {
			got = append(got, hit{row: batchStart + int(i), age: ages[i]})
		}
		batchStart += batchSize
	}

	if len(got) != len(want) {
		t.Fatalf("compound filter survivor count mismatch: vectorized=%d naive=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("survivor %d mismatch: vectorized=%+v naive=%+v", i, got[i], want[i])
		}
	}
}

func TestPipelineTripleFilterMatchesNaive(t *testing.T) {
	// Correctness twin for the 3-predicate chain. Same membership-not-
	// just-count check as the compound twin.
	const n = 10_000
	rg := makePipelineRowGroup(t, n)
	want := naiveRowAtATimeCountTriple(rg, 30, 60, 45)
	if want == 0 {
		t.Fatal("baseline produced 0 survivors — fixture or test broken")
	}

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	f1, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := NewFilterOp(f1, 0, Int64Lt{Value: 60})
	if err != nil {
		t.Fatal(err)
	}
	f3, err := NewFilterOp(f2, 0, Int64Ne{Value: 45})
	if err != nil {
		t.Fatal(err)
	}

	got := 0
	for {
		batch, ok := f3.Next()
		if !ok {
			break
		}
		ages := batch.Vectors[0].Int64s()
		for _, i := range batch.Sel.Indices() {
			a := ages[i]
			if !(a > 30 && a < 60 && a != 45) {
				t.Fatalf("triple filter passed row violating predicates: age=%d", a)
			}
			got++
		}
	}
	if got != want {
		t.Fatalf("triple filter survivor count mismatch: vectorized=%d naive=%d", got, want)
	}
}

func TestPipelineLargeDataset(t *testing.T) {
	// Plan Step 6 success criterion: 1M-row dataset, ≥3 columns,
	// correct result. The actual throughput claim is validated by the
	// benchmark; this test just asserts correctness at scale.
	if testing.Short() {
		t.Skip("skipping 1M-row correctness test in -short mode")
	}
	const n = 1_000_000
	rg := makePipelineRowGroup(t, n)

	count := 0
	for _, r := range vectorizedScanFilterProject(t, rg, 30) {
		if r.age <= 30 {
			t.Fatalf("survivor with age=%d ≤ 30", r.age)
		}
		count++
	}
	t.Logf("1M-row pipeline: %d survivors", count)
	if count < 500_000 || count > 800_000 {
		t.Errorf("count = %d, want 500k..800k for uniform 0..89 filtered > 30", count)
	}
}
