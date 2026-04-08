package exec

import (
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 4 Step 6: twin GROUP BY benchmarks — naive row-at-a-time
// vs vectorized GroupByOp — at two cardinalities.
//
// Run: go test -bench=GroupBy -benchmem -run=^$ ./exec/
//
// Canonical query:
//
//	SELECT key, COUNT(*), AVG(age) FROM t GROUP BY key
//
// Fixture: 1M rows. Two cardinality regimes:
//
//  1. Low (~10 groups): 10 distinct string city values. Hash table
//     fits in L1; both implementations should be fast. If vectorized
//     doesn't at least match naive here, the whole vector-at-a-time
//     model is in trouble — this is the friendliest case.
//
//  2. High (~10 000 groups): 10 000 distinct int64 keys
//     (rowIdx % 10000). Hash table spills out of L1; hashing
//     dominates; this is where per-batch hash probe amortization
//     and late materialization are supposed to pay off.
//
// Phase 4 success criterion (from docs/phase4-steps.md):
// vectorized at least matches naive (≥1.0×) on BOTH cardinalities.
// Stretch: ≥2× on the high-cardinality case.
//
// The write-up lives in docs/phase4-benchmark.md with the same
// intellectual-honesty discipline as Phase 3 — negative results are
// documented, not explained away.

const groupByBenchRows = 1_000_000

// Low-cardinality fixture: 10 city strings, deterministic RNG.
var (
	lowCardRGOnce sync.Once
	lowCardRG     *storage.RowGroup
)

var lowCardCities = []string{
	"Paris", "Lyon", "Kunming", "Shanghai", "Beijing",
	"Tokyo", "London", "Berlin", "Madrid", "Rome",
}

func getLowCardBenchRG(tb testing.TB) *storage.RowGroup {
	lowCardRGOnce.Do(func() {
		rng := rand.New(rand.NewPCG(1001, 2002))
		cities := make([]string, groupByBenchRows)
		ages := make([]int64, groupByBenchRows)
		for i := range groupByBenchRows {
			cities[i] = lowCardCities[rng.IntN(len(lowCardCities))]
			ages[i] = int64(rng.IntN(90))
		}
		cityCol := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(cities))
		ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
		rg, err := storage.NewRowGroup(cityCol, ageCol)
		if err != nil {
			tb.Fatal(err)
		}
		lowCardRG = rg
	})
	return lowCardRG
}

// High-cardinality fixture: 10 000 distinct int64 user IDs
// (rowIdx % 10 000), deterministic age values.
var (
	highCardRGOnce sync.Once
	highCardRG     *storage.RowGroup
)

const highCardDistinct = 10_000

// Extra-high cardinality (100k groups) is a stress run to confirm the
// high-card trend extrapolates and the decision's "zero-alloc scales"
// argument is evidence, not conjecture. rowIdx % 100000 over 1M rows
// yields ~10 rows per group — the naive map carries 100k *acc cells
// plus every bucket rehash Go's growth schedule triggers.
const extraHighCardDistinct = 100_000

var (
	extraHighCardRGOnce sync.Once
	extraHighCardRG     *storage.RowGroup
)

func getExtraHighCardBenchRG(tb testing.TB) *storage.RowGroup {
	extraHighCardRGOnce.Do(func() {
		rng := rand.New(rand.NewPCG(5005, 6006))
		ids := make([]int64, groupByBenchRows)
		ages := make([]int64, groupByBenchRows)
		for i := range groupByBenchRows {
			ids[i] = int64(rng.IntN(extraHighCardDistinct))
			ages[i] = int64(rng.IntN(90))
		}
		idCol := storage.NewColumnChunkNoNulls("user_id", storage.NewInt64ColumnFromSlice(ids))
		ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
		rg, err := storage.NewRowGroup(idCol, ageCol)
		if err != nil {
			tb.Fatal(err)
		}
		extraHighCardRG = rg
	})
	return extraHighCardRG
}

func getHighCardBenchRG(tb testing.TB) *storage.RowGroup {
	highCardRGOnce.Do(func() {
		rng := rand.New(rand.NewPCG(3003, 4004))
		ids := make([]int64, groupByBenchRows)
		ages := make([]int64, groupByBenchRows)
		for i := range groupByBenchRows {
			ids[i] = int64(rng.IntN(highCardDistinct))
			ages[i] = int64(rng.IntN(90))
		}
		idCol := storage.NewColumnChunkNoNulls("user_id", storage.NewInt64ColumnFromSlice(ids))
		ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
		rg, err := storage.NewRowGroup(idCol, ageCol)
		if err != nil {
			tb.Fatal(err)
		}
		highCardRG = rg
	})
	return highCardRG
}

// =====================================================================
// Low cardinality (string key, ~10 groups)
// =====================================================================

// naiveGroupByLowCard is the row-at-a-time baseline: walk raw
// columns once, maintain a map[string]struct{count,sum} keyed by
// city. This is what a textbook "row-oriented database in a
// weekend" would write — no vectorization, no late materialization,
// just a tight row loop.
func naiveGroupByLowCard(rg *storage.RowGroup) (totalCount int64, totalSum int64) {
	cities := rg.ColumnByName("city").Values.(*storage.StringColumn).Values()
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()

	type acc struct {
		count int64
		sum   int64
	}
	m := make(map[string]*acc, len(lowCardCities))
	for i, c := range cities {
		a, ok := m[c]
		if !ok {
			a = &acc{}
			m[c] = a
		}
		a.count++
		a.sum += ages[i]
	}
	for _, a := range m {
		totalCount += a.count
		totalSum += a.sum
	}
	return
}

func BenchmarkGroupByLowCardNaive(b *testing.B) {
	rg := getLowCardBenchRG(b)
	var tc int64
	b.ReportAllocs()
	for b.Loop() {
		tc, _ = naiveGroupByLowCard(rg)
	}
	if tc != groupByBenchRows {
		b.Fatalf("totalCount = %d, want %d", tc, groupByBenchRows)
	}
}

func BenchmarkGroupByLowCardVectorized(b *testing.B) {
	rg := getLowCardBenchRG(b)
	scan, err := NewScanOp(rg, []string{"city", "age"})
	if err != nil {
		b.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeString, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "avg_age", ColIndex: 1, Agg: &Int64Avg{}},
	})
	if err != nil {
		b.Fatal(err)
	}

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op.Reset()
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != groupByBenchRows {
		b.Fatalf("total rows = %d, want %d", total, groupByBenchRows)
	}
}

// =====================================================================
// High cardinality (int64 key, ~10k groups)
// =====================================================================

// naiveGroupByHighCard is the row-at-a-time baseline for a
// high-cardinality int64 key. Same shape as the low-card naive
// baseline, different map type.
func naiveGroupByHighCard(rg *storage.RowGroup) (totalCount int64, totalSum int64) {
	ids := rg.ColumnByName("user_id").Values.(*storage.Int64Column).Values()
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()

	type acc struct {
		count int64
		sum   int64
	}
	m := make(map[int64]*acc, highCardDistinct)
	for i, id := range ids {
		a, ok := m[id]
		if !ok {
			a = &acc{}
			m[id] = a
		}
		a.count++
		a.sum += ages[i]
	}
	for _, a := range m {
		totalCount += a.count
		totalSum += a.sum
	}
	return
}

func BenchmarkGroupByHighCardNaive(b *testing.B) {
	rg := getHighCardBenchRG(b)
	var tc int64
	b.ReportAllocs()
	for b.Loop() {
		tc, _ = naiveGroupByHighCard(rg)
	}
	if tc != groupByBenchRows {
		b.Fatalf("totalCount = %d, want %d", tc, groupByBenchRows)
	}
}

func BenchmarkGroupByHighCardVectorized(b *testing.B) {
	rg := getHighCardBenchRG(b)
	scan, err := NewScanOp(rg, []string{"user_id", "age"})
	if err != nil {
		b.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "avg_age", ColIndex: 1, Agg: &Int64Avg{}},
	})
	if err != nil {
		b.Fatal(err)
	}

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op.Reset()
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != groupByBenchRows {
		b.Fatalf("total rows = %d, want %d", total, groupByBenchRows)
	}
}

// =====================================================================
// Extra-high cardinality (100k int64 groups) — stress run
// =====================================================================

func naiveGroupByExtraHighCard(rg *storage.RowGroup) (totalCount int64, totalSum int64) {
	ids := rg.ColumnByName("user_id").Values.(*storage.Int64Column).Values()
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()

	type acc struct {
		count int64
		sum   int64
	}
	m := make(map[int64]*acc, extraHighCardDistinct)
	for i, id := range ids {
		a, ok := m[id]
		if !ok {
			a = &acc{}
			m[id] = a
		}
		a.count++
		a.sum += ages[i]
	}
	for _, a := range m {
		totalCount += a.count
		totalSum += a.sum
	}
	return
}

func BenchmarkGroupByExtraHighCardNaive(b *testing.B) {
	rg := getExtraHighCardBenchRG(b)
	var tc int64
	b.ReportAllocs()
	for b.Loop() {
		tc, _ = naiveGroupByExtraHighCard(rg)
	}
	if tc != groupByBenchRows {
		b.Fatalf("totalCount = %d, want %d", tc, groupByBenchRows)
	}
}

func BenchmarkGroupByExtraHighCardVectorized(b *testing.B) {
	rg := getExtraHighCardBenchRG(b)
	scan, err := NewScanOp(rg, []string{"user_id", "age"})
	if err != nil {
		b.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "avg_age", ColIndex: 1, Agg: &Int64Avg{}},
	})
	if err != nil {
		b.Fatal(err)
	}

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op.Reset()
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != groupByBenchRows {
		b.Fatalf("total rows = %d, want %d", total, groupByBenchRows)
	}
}
