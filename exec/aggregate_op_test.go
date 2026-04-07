package exec

import (
	"math/rand/v2"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 4 Step 3: end-to-end tests for AggregateOp.
//
// Pipeline shape under test: ScanOp → FilterOp → AggregateOp → consumer.
// The naive baseline is a row-at-a-time loop over the same RowGroup
// applying the same predicate and aggregator semantics.

// makeAggRowGroup builds a deterministic 3-column fixture suited for
// aggregation testing: int64 age, float64 price, string city.
func makeAggRowGroup(t testing.TB, n int) *storage.RowGroup {
	t.Helper()
	rng := rand.New(rand.NewPCG(7, 11))

	ages := make([]int64, n)
	prices := make([]float64, n)
	cities := make([]string, n)
	cityPool := []string{"Paris", "Lyon", "Kunming", "Shanghai", "Beijing"}
	for i := range n {
		ages[i] = int64(rng.IntN(90))
		prices[i] = float64(rng.IntN(1000)) + 0.5
		cities[i] = cityPool[i%len(cityPool)]
	}

	ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
	priceCol := storage.NewColumnChunkNoNulls("price", storage.NewFloat64ColumnFromSlice(prices))
	cityCol := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(cities))
	rg, err := storage.NewRowGroup(ageCol, priceCol, cityCol)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

// drain1Row pulls one batch from op and asserts it has exactly one row.
func drain1Row(t *testing.T, op *AggregateOp) *Batch {
	t.Helper()
	b, ok := op.Next()
	if !ok {
		t.Fatal("AggregateOp returned no batch")
	}
	if b.Len() != 1 {
		t.Fatalf("AggregateOp emitted %d rows, want 1", b.Len())
	}
	// Subsequent Next must report exhausted.
	if _, ok := op.Next(); ok {
		t.Fatal("AggregateOp returned a second batch — should be done after first")
	}
	return b
}

func TestAggregateOpCountStar(t *testing.T) {
	const n = 1000
	rg := makeAggRowGroup(t, n)
	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(scan, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drain1Row(t, op)
	if got := b.Vectors[0].Int64s()[0]; got != n {
		t.Errorf("COUNT(*) = %d, want %d", got, n)
	}
}

func TestAggregateOpCountStarWithFilter(t *testing.T) {
	// COUNT(*) WHERE age > 30 — verifies the operator respects
	// upstream filter selection.
	const n = 5000
	rg := makeAggRowGroup(t, n)

	// Naive baseline.
	naive := 0
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	for _, a := range ages {
		if a > 30 {
			naive++
		}
	}

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(filter, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drain1Row(t, op)
	if got := int(b.Vectors[0].Int64s()[0]); got != naive {
		t.Errorf("COUNT(*) WHERE age>30 = %d, want %d", got, naive)
	}
}

func TestAggregateOpMultipleAggregators(t *testing.T) {
	// SELECT COUNT(*), SUM(age), MIN(age), MAX(age), AVG(age),
	//        SUM(price), AVG(price)
	// FROM t WHERE age > 30
	const n = 3000
	rg := makeAggRowGroup(t, n)

	// Naive baseline.
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	prices := rg.ColumnByName("price").Values.(*storage.Float64Column).Values()
	var nCount, naiveSum, naiveMin, naiveMax int64
	naiveMin = 1<<62
	naiveMax = -(1 << 62)
	var naivePriceSum float64
	for i, a := range ages {
		if a <= 30 {
			continue
		}
		nCount++
		naiveSum += a
		if a < naiveMin {
			naiveMin = a
		}
		if a > naiveMax {
			naiveMax = a
		}
		naivePriceSum += prices[i]
	}
	naiveAvg := float64(naiveSum) / float64(nCount)
	naivePriceAvg := naivePriceSum / float64(nCount)

	scan, err := NewScanOp(rg, []string{"age", "price"})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 30})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(filter, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "sum_age", ColIndex: 0, Agg: &Int64Sum{}},
		{Name: "min_age", ColIndex: 0, Agg: &Int64Min{}},
		{Name: "max_age", ColIndex: 0, Agg: &Int64Max{}},
		{Name: "avg_age", ColIndex: 0, Agg: &Int64Avg{}},
		{Name: "sum_price", ColIndex: 1, Agg: &Float64Sum{}},
		{Name: "avg_price", ColIndex: 1, Agg: &Float64Avg{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drain1Row(t, op)

	if got := b.Vectors[0].Int64s()[0]; got != nCount {
		t.Errorf("COUNT(*) = %d, want %d", got, nCount)
	}
	if got := b.Vectors[1].Int64s()[0]; got != naiveSum {
		t.Errorf("SUM(age) = %d, want %d", got, naiveSum)
	}
	if got := b.Vectors[2].Int64s()[0]; got != naiveMin {
		t.Errorf("MIN(age) = %d, want %d", got, naiveMin)
	}
	if got := b.Vectors[3].Int64s()[0]; got != naiveMax {
		t.Errorf("MAX(age) = %d, want %d", got, naiveMax)
	}
	if got := b.Vectors[4].Float64s()[0]; got != naiveAvg {
		t.Errorf("AVG(age) = %v, want %v", got, naiveAvg)
	}
	if got := b.Vectors[5].Float64s()[0]; got != naivePriceSum {
		t.Errorf("SUM(price) = %v, want %v", got, naivePriceSum)
	}
	if got := b.Vectors[6].Float64s()[0]; got != naivePriceAvg {
		t.Errorf("AVG(price) = %v, want %v", got, naivePriceAvg)
	}
}

func TestAggregateOpMultiBatchAccumulation(t *testing.T) {
	// 3000 rows > VectorSize (1024), so the child emits 3 batches.
	// AggregateOp must accumulate state across all of them.
	const n = 3000
	rg := makeAggRowGroup(t, n)
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	var want int64
	for _, a := range ages {
		want += a
	}

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(scan, []AggregateSpec{
		{Name: "sum_age", ColIndex: 0, Agg: &Int64Sum{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drain1Row(t, op)
	if got := b.Vectors[0].Int64s()[0]; got != want {
		t.Errorf("multi-batch SUM = %d, want %d", got, want)
	}
}

func TestAggregateOpReset(t *testing.T) {
	// Drain twice — Reset must produce the same result.
	const n = 1500
	rg := makeAggRowGroup(t, n)
	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(scan, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "sum_age", ColIndex: 0, Agg: &Int64Sum{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	first := drain1Row(t, op)
	firstCount := first.Vectors[0].Int64s()[0]
	firstSum := first.Vectors[1].Int64s()[0]

	op.Reset()

	second := drain1Row(t, op)
	if got := second.Vectors[0].Int64s()[0]; got != firstCount {
		t.Errorf("Reset COUNT mismatch: first=%d second=%d", firstCount, got)
	}
	if got := second.Vectors[1].Int64s()[0]; got != firstSum {
		t.Errorf("Reset SUM mismatch: first=%d second=%d", firstSum, got)
	}
}

func TestAggregateOpEmptyInput(t *testing.T) {
	// Filter that lets nothing through. SUM/MIN/MAX/AVG should be
	// null; COUNT(*) should be 0.
	const n = 1000
	rg := makeAggRowGroup(t, n)
	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 1_000_000}) // unreachable
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(filter, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "sum_age", ColIndex: 0, Agg: &Int64Sum{}},
		{Name: "min_age", ColIndex: 0, Agg: &Int64Min{}},
		{Name: "avg_age", ColIndex: 0, Agg: &Int64Avg{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drain1Row(t, op)

	if got := b.Vectors[0].Int64s()[0]; got != 0 {
		t.Errorf("COUNT(*) of empty input = %d, want 0", got)
	}
	if !b.Vectors[1].IsNull(0) {
		t.Errorf("SUM of empty input must be null")
	}
	if !b.Vectors[2].IsNull(0) {
		t.Errorf("MIN of empty input must be null")
	}
	if !b.Vectors[3].IsNull(0) {
		t.Errorf("AVG of empty input must be null (not divide-by-zero)")
	}
}

func TestNewAggregateOpValidation(t *testing.T) {
	scan, _ := NewScanOp(makeAggRowGroup(t, 10), []string{"age"})

	if _, err := NewAggregateOp(scan, nil); err == nil {
		t.Error("nil specs: want error")
	}
	if _, err := NewAggregateOp(scan, []AggregateSpec{}); err == nil {
		t.Error("empty specs: want error")
	}
	if _, err := NewAggregateOp(scan, []AggregateSpec{{Name: "x", ColIndex: 0, Agg: nil}}); err == nil {
		t.Error("nil aggregator: want error")
	}
	if _, err := NewAggregateOp(scan, []AggregateSpec{{Name: "x", ColIndex: -1, Agg: &CountStar{}}}); err == nil {
		t.Error("negative ColIndex: want error")
	}
	// Duplicate Aggregator pointer across specs is rejected: each
	// spec writes to its own output Vector, so reusing one
	// Aggregator instance would silently double-count and emit a
	// wrong arithmetic answer with no crash. Reviewer-flagged
	// footgun.
	dup := &Int64Sum{}
	if _, err := NewAggregateOp(scan, []AggregateSpec{
		{Name: "a", ColIndex: 0, Agg: dup},
		{Name: "b", ColIndex: 0, Agg: dup},
	}); err == nil {
		t.Error("duplicate Aggregator pointer: want error")
	}
}

func TestAggregateOpResetIsZeroAlloc(t *testing.T) {
	// Reset must zero per-group state in place — not via Init's
	// make+copy. This matters for Step 6's b.Loop benchmark, which
	// runs Reset once per iteration and would otherwise pay
	// (numAggregators × 2 small allocs) per iteration that the
	// naive baseline does not.
	const n = 1000
	rg := makeAggRowGroup(t, n)
	scan, err := NewScanOp(rg, []string{"age", "price"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewAggregateOp(scan, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "sum_age", ColIndex: 0, Agg: &Int64Sum{}},
		{Name: "min_age", ColIndex: 0, Agg: &Int64Min{}},
		{Name: "max_age", ColIndex: 0, Agg: &Int64Max{}},
		{Name: "avg_age", ColIndex: 0, Agg: &Int64Avg{}},
		{Name: "sum_price", ColIndex: 1, Agg: &Float64Sum{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drain once to warm everything up.
	drain1Row(t, op)

	allocs := testing.AllocsPerRun(50, func() {
		op.Reset()
		// Drain again into oblivion.
		for {
			b, ok := op.Next()
			if !ok {
				break
			}
			_ = b
		}
	})
	// ScanOp.Reset / FilterOp.Reset / etc. are also in the path;
	// the contract is "no aggregator-related allocations". Empirically
	// the whole drain+Reset path is 0 here. Allow up to 1 to absorb
	// any harmless harness alloc, but flag anything bigger.
	if allocs > 1 {
		t.Errorf("AggregateOp Reset+drain allocs/op = %v, want ≤ 1", allocs)
	}
}
