package exec

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 4 Step 4: end-to-end tests for GroupByOp (single key,
// int64/string, hash-table GROUP BY). The naive baseline is a Go
// map over a row-at-a-time scan of the same RowGroup, applying the
// same semantics the aggregators do.

// makeGroupByRowGroup builds a 3-column fixture: int64 age bucket,
// float64 price, string city. Two candidate GROUP BY keys so tests
// can exercise both key types against the same data.
func makeGroupByRowGroup(t testing.TB, n int, cityPool []string) *storage.RowGroup {
	t.Helper()
	rng := rand.New(rand.NewPCG(17, 19))

	ages := make([]int64, n)
	prices := make([]float64, n)
	cities := make([]string, n)
	for i := range n {
		ages[i] = int64(rng.IntN(10)) // 10 age buckets
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

// drainGroupBy pulls exactly one batch from op, asserts subsequent
// Next is exhausted, and returns the batch.
func drainGroupBy(t *testing.T, op *GroupByOp) *Batch {
	t.Helper()
	b, ok := op.Next()
	if !ok {
		t.Fatal("GroupByOp returned no batch")
	}
	// Second Next must be exhausted.
	if _, ok := op.Next(); ok {
		t.Fatal("GroupByOp returned a second batch — should be done after first")
	}
	return b
}

// collectInt64KeyedBatch reads a result batch whose key column is
// int64 into a map[int64][]any, one entry per aggregator output,
// for ergonomic comparison with a naive baseline.
func collectInt64KeyedBatch(b *Batch) map[int64][]any {
	out := map[int64][]any{}
	keys := b.Vectors[0].Int64s()
	for _, i := range b.Sel.Indices() {
		row := make([]any, len(b.Vectors)-1)
		for ci := 1; ci < len(b.Vectors); ci++ {
			v := b.Vectors[ci]
			if v.IsNull(int(i)) {
				row[ci-1] = nil
				continue
			}
			switch v.Type {
			case storage.TypeInt64:
				row[ci-1] = v.Int64s()[i]
			case storage.TypeFloat64:
				row[ci-1] = v.Float64s()[i]
			}
		}
		out[keys[i]] = row
	}
	return out
}

func collectStringKeyedBatch(b *Batch) map[string][]any {
	out := map[string][]any{}
	keys := b.Vectors[0].Strings()
	for _, i := range b.Sel.Indices() {
		row := make([]any, len(b.Vectors)-1)
		for ci := 1; ci < len(b.Vectors); ci++ {
			v := b.Vectors[ci]
			if v.IsNull(int(i)) {
				row[ci-1] = nil
				continue
			}
			switch v.Type {
			case storage.TypeInt64:
				row[ci-1] = v.Int64s()[i]
			case storage.TypeFloat64:
				row[ci-1] = v.Float64s()[i]
			}
		}
		out[keys[i]] = row
	}
	return out
}

func TestGroupByOpInt64KeyCountStar(t *testing.T) {
	// SELECT age, COUNT(*) FROM t GROUP BY age
	const n = 5000
	rg := makeGroupByRowGroup(t, n, []string{"A", "B"})

	// Naive baseline.
	naive := map[int64]int64{}
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	for _, a := range ages {
		naive[a]++
	}

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drainGroupBy(t, op)
	if b.Len() != len(naive) {
		t.Fatalf("group count = %d, want %d", b.Len(), len(naive))
	}
	got := collectInt64KeyedBatch(b)
	if len(got) != len(naive) {
		t.Fatalf("distinct keys = %d, want %d", len(got), len(naive))
	}
	for k, wantN := range naive {
		row, ok := got[k]
		if !ok {
			t.Errorf("missing key %d", k)
			continue
		}
		if row[0] != wantN {
			t.Errorf("key %d: COUNT(*) = %v, want %d", k, row[0], wantN)
		}
	}
}

func TestGroupByOpStringKeyMulti(t *testing.T) {
	// SELECT city, COUNT(*), SUM(price), AVG(price) FROM t GROUP BY city
	const n = 3000
	cities := []string{"Paris", "Lyon", "Kunming", "Shanghai", "Beijing"}
	rg := makeGroupByRowGroup(t, n, cities)

	// Naive baseline.
	naiveCount := map[string]int64{}
	naiveSum := map[string]float64{}
	citiesCol := rg.ColumnByName("city").Values.(*storage.StringColumn).Values()
	pricesCol := rg.ColumnByName("price").Values.(*storage.Float64Column).Values()
	for i, c := range citiesCol {
		naiveCount[c]++
		naiveSum[c] += pricesCol[i]
	}

	scan, err := NewScanOp(rg, []string{"price", "city"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 1, storage.TypeString, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "sum_price", ColIndex: 0, Agg: &Float64Sum{}},
		{Name: "avg_price", ColIndex: 0, Agg: &Float64Avg{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drainGroupBy(t, op)
	if b.Len() != len(naiveCount) {
		t.Fatalf("group count = %d, want %d", b.Len(), len(naiveCount))
	}
	got := collectStringKeyedBatch(b)
	for k, wantN := range naiveCount {
		row, ok := got[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if row[0] != wantN {
			t.Errorf("key %q: COUNT(*) = %v, want %d", k, row[0], wantN)
		}
		if row[1] != naiveSum[k] {
			t.Errorf("key %q: SUM(price) = %v, want %v", k, row[1], naiveSum[k])
		}
		wantAvg := naiveSum[k] / float64(wantN)
		if row[2] != wantAvg {
			t.Errorf("key %q: AVG(price) = %v, want %v", k, row[2], wantAvg)
		}
	}
}

func TestGroupByOpMultiBatchAccumulation(t *testing.T) {
	// 3000 > VectorSize (1024) forces the child to emit multiple
	// batches. The hash table, aggregators, and keysInt64 slice must
	// all accumulate across batches.
	const n = 3000
	rg := makeGroupByRowGroup(t, n, []string{"A"})

	naive := map[int64]int64{}
	ages := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	for _, a := range ages {
		naive[a]++
	}

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drainGroupBy(t, op)
	got := collectInt64KeyedBatch(b)
	for k, wantN := range naive {
		if row := got[k]; row == nil || row[0] != wantN {
			t.Errorf("key %d: got %v, want %d", k, row, wantN)
		}
	}
}

func TestGroupByOpWithFilter(t *testing.T) {
	// SELECT city, COUNT(*) FROM t WHERE age > 5 GROUP BY city
	const n = 2000
	cities := []string{"A", "B", "C"}
	rg := makeGroupByRowGroup(t, n, cities)

	naive := map[string]int64{}
	citiesCol := rg.ColumnByName("city").Values.(*storage.StringColumn).Values()
	agesCol := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	for i, a := range agesCol {
		if a > 5 {
			naive[citiesCol[i]]++
		}
	}

	scan, err := NewScanOp(rg, []string{"age", "city"})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 5})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(filter, 1, storage.TypeString, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drainGroupBy(t, op)
	got := collectStringKeyedBatch(b)
	if len(got) != len(naive) {
		t.Fatalf("distinct keys post-filter = %d, want %d", len(got), len(naive))
	}
	for k, wantN := range naive {
		if row := got[k]; row == nil || row[0] != wantN {
			t.Errorf("key %q: got %v, want %d", k, row, wantN)
		}
	}
}

func TestGroupByOpNullKeyFormsItsOwnGroup(t *testing.T) {
	// Null keys get their own group (standard SQL). Verify that
	// non-null groups aggregate correctly alongside, and that the
	// null group's key column row is reported as null.
	n := 300
	keys := make([]int64, n)
	vals := make([]int64, n)
	nullBM := storage.NewNullBitmap(n)
	for i := range n {
		keys[i] = int64(i % 3) // three non-null groups 0,1,2
		vals[i] = int64(i)
		if i%7 == 0 {
			nullBM.SetNull(i) // sprinkle nulls across the key
		}
	}

	keyChunk, err := storage.NewColumnChunk("k", storage.NewInt64ColumnFromSlice(keys), nullBM)
	if err != nil {
		t.Fatal(err)
	}
	valChunk := storage.NewColumnChunkNoNulls("v", storage.NewInt64ColumnFromSlice(vals))
	rg, err := storage.NewRowGroup(keyChunk, valChunk)
	if err != nil {
		t.Fatal(err)
	}

	// Naive baseline.
	var naiveNullSum int64
	var naiveNullCount int64
	naive := map[int64]int64{}
	for i := range n {
		if nullBM.IsNull(i) {
			naiveNullSum += vals[i]
			naiveNullCount++
			continue
		}
		naive[keys[i]] += vals[i]
	}

	scan, err := NewScanOp(rg, []string{"k", "v"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "sum_v", ColIndex: 1, Agg: &Int64Sum{}},
		{Name: "n", ColIndex: 1, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drainGroupBy(t, op)

	if b.Len() != len(naive)+1 {
		t.Fatalf("group count = %d, want %d (non-null groups + 1 null group)", b.Len(), len(naive)+1)
	}

	// Find the null group and non-null groups.
	sawNull := false
	keysOut := b.Vectors[0].Int64s()
	sumsOut := b.Vectors[1].Int64s()
	countsOut := b.Vectors[2].Int64s()
	for _, i := range b.Sel.Indices() {
		if b.Vectors[0].IsNull(int(i)) {
			if sawNull {
				t.Error("more than one null group emitted")
			}
			sawNull = true
			if sumsOut[i] != naiveNullSum {
				t.Errorf("null group SUM(v) = %d, want %d", sumsOut[i], naiveNullSum)
			}
			if countsOut[i] != naiveNullCount {
				t.Errorf("null group COUNT(*) = %d, want %d", countsOut[i], naiveNullCount)
			}
			continue
		}
		want, ok := naive[keysOut[i]]
		if !ok {
			t.Errorf("unexpected key %d", keysOut[i])
			continue
		}
		if sumsOut[i] != want {
			t.Errorf("key %d: SUM(v) = %d, want %d", keysOut[i], sumsOut[i], want)
		}
	}
	if !sawNull {
		t.Error("null group was never emitted")
	}
}

func TestGroupByOpReset(t *testing.T) {
	// Drain twice; Reset must produce identical results. Also verifies
	// the in-place hash table clear() and keysInt64 slice truncation
	// don't leak state from the prior iteration.
	const n = 800
	rg := makeGroupByRowGroup(t, n, []string{"A", "B", "C"})
	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
		{Name: "sum", ColIndex: 0, Agg: &Int64Sum{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	first := drainGroupBy(t, op)
	firstMap := collectInt64KeyedBatch(first)
	firstKeys := make([]int64, 0, len(firstMap))
	for k := range firstMap {
		firstKeys = append(firstKeys, k)
	}
	sort.Slice(firstKeys, func(i, j int) bool { return firstKeys[i] < firstKeys[j] })

	op.Reset()

	second := drainGroupBy(t, op)
	secondMap := collectInt64KeyedBatch(second)
	if len(secondMap) != len(firstMap) {
		t.Fatalf("Reset: second distinct-key count = %d, want %d", len(secondMap), len(firstMap))
	}
	for _, k := range firstKeys {
		if firstMap[k][0] != secondMap[k][0] {
			t.Errorf("key %d: COUNT differs across Reset — first=%v second=%v", k, firstMap[k][0], secondMap[k][0])
		}
		if firstMap[k][1] != secondMap[k][1] {
			t.Errorf("key %d: SUM differs across Reset — first=%v second=%v", k, firstMap[k][1], secondMap[k][1])
		}
	}
}

func TestGroupByOpEmptyInputEmitsNoRows(t *testing.T) {
	// GROUP BY over an empty input yields zero rows (distinct from
	// scalar AggregateOp, which yields one).
	const n = 500
	rg := makeGroupByRowGroup(t, n, []string{"A"})

	scan, err := NewScanOp(rg, []string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewFilterOp(scan, 0, Int64Gt{Value: 1_000_000}) // nothing survives
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(filter, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := op.Next(); ok {
		t.Fatalf("expected EOF on empty input, got batch with %d rows", b.Len())
	}
}

func TestNewGroupByOpValidation(t *testing.T) {
	rg := makeGroupByRowGroup(t, 10, []string{"A"})
	scan, _ := NewScanOp(rg, []string{"age", "city"})

	if _, err := NewGroupByOp(nil, 0, storage.TypeInt64, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("nil child: want error")
	}
	if _, err := NewGroupByOp(scan, -1, storage.TypeInt64, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("negative keyColIndex: want error")
	}
	if _, err := NewGroupByOp(scan, 0, storage.TypeFloat64, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("unsupported key type: want error")
	}
	if _, err := NewGroupByOp(scan, 0, storage.TypeInt64, nil); err == nil {
		t.Error("nil specs: want error")
	}
	if _, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{{Name: "x", ColIndex: 0, Agg: nil}}); err == nil {
		t.Error("nil aggregator: want error")
	}
	if _, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{{Name: "x", ColIndex: -1, Agg: &CountStar{}}}); err == nil {
		t.Error("negative ColIndex: want error")
	}
	dup := &Int64Sum{}
	if _, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "a", ColIndex: 0, Agg: dup},
		{Name: "b", ColIndex: 0, Agg: dup},
	}); err == nil {
		t.Error("duplicate Aggregator pointer: want error")
	}
}

// =====================================================================
// Step 5: multi-key composite GROUP BY tests
// =====================================================================

func TestGroupByOpMultiTwoKeysCountAvg(t *testing.T) {
	// SELECT city, age, COUNT(*), AVG(price)
	// FROM t GROUP BY city, age
	const n = 3000
	cities := []string{"Paris", "Lyon", "Kunming"}
	rg := makeGroupByRowGroup(t, n, cities)

	type key struct {
		city string
		age  int64
	}
	naiveCount := map[key]int64{}
	naiveSum := map[key]float64{}
	citiesCol := rg.ColumnByName("city").Values.(*storage.StringColumn).Values()
	agesCol := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	pricesCol := rg.ColumnByName("price").Values.(*storage.Float64Column).Values()
	for i := 0; i < n; i++ {
		k := key{citiesCol[i], agesCol[i]}
		naiveCount[k]++
		naiveSum[k] += pricesCol[i]
	}

	scan, err := NewScanOp(rg, []string{"age", "price", "city"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOpMulti(
		scan,
		[]int{2, 0}, // city, age — column indexes into the scan projection
		[]storage.ColumnType{storage.TypeString, storage.TypeInt64},
		[]AggregateSpec{
			{Name: "n", ColIndex: 1, Agg: &CountStar{}},
			{Name: "avg_price", ColIndex: 1, Agg: &Float64Avg{}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch emitted")
	}
	if b.Len() != len(naiveCount) {
		t.Fatalf("group count = %d, want %d", b.Len(), len(naiveCount))
	}

	// Extract result rows into a map keyed by (city, age).
	cityOut := b.Vectors[0].Strings()
	ageOut := b.Vectors[1].Int64s()
	countOut := b.Vectors[2].Int64s()
	avgOut := b.Vectors[3].Float64s()
	for _, i := range b.Sel.Indices() {
		k := key{cityOut[i], ageOut[i]}
		wantN, ok := naiveCount[k]
		if !ok {
			t.Errorf("unexpected key %+v", k)
			continue
		}
		if countOut[i] != wantN {
			t.Errorf("key %+v: COUNT(*) = %d, want %d", k, countOut[i], wantN)
		}
		wantAvg := naiveSum[k] / float64(wantN)
		if avgOut[i] != wantAvg {
			t.Errorf("key %+v: AVG(price) = %v, want %v", k, avgOut[i], wantAvg)
		}
	}
	// Second Next is exhausted.
	if _, ok := op.Next(); ok {
		t.Fatal("second Next returned a batch — expected EOF")
	}
}

func TestGroupByOpMultiStringString(t *testing.T) {
	// Two string keys: make sure length-prefix encoding disambiguates
	// rows like ("a","bc") vs ("ab","c").
	a := []string{"a", "a", "ab", "ab", "a", "ab", "ab", "a"}
	b := []string{"bc", "bc", "c", "c", "bc", "c", "d", "bc"}
	v := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	aCol := storage.NewColumnChunkNoNulls("a", storage.NewStringColumnFromSlice(a))
	bCol := storage.NewColumnChunkNoNulls("b", storage.NewStringColumnFromSlice(b))
	vCol := storage.NewColumnChunkNoNulls("v", storage.NewInt64ColumnFromSlice(v))
	rg, err := storage.NewRowGroup(aCol, bCol, vCol)
	if err != nil {
		t.Fatal(err)
	}

	scan, err := NewScanOp(rg, []string{"a", "b", "v"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOpMulti(scan,
		[]int{0, 1},
		[]storage.ColumnType{storage.TypeString, storage.TypeString},
		[]AggregateSpec{{Name: "sum_v", ColIndex: 2, Agg: &Int64Sum{}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	batch, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}

	// Expected: ("a","bc")→1+2+5+8=16, ("ab","c")→3+4+6=13, ("ab","d")→7
	type k struct{ a, b string }
	naive := map[k]int64{
		{"a", "bc"}: 16,
		{"ab", "c"}: 13,
		{"ab", "d"}: 7,
	}
	if batch.Len() != len(naive) {
		t.Fatalf("groups = %d, want %d (length-prefix encoding must disambiguate prefix-collision pairs)", batch.Len(), len(naive))
	}
	aOut := batch.Vectors[0].Strings()
	bOut := batch.Vectors[1].Strings()
	sumOut := batch.Vectors[2].Int64s()
	for _, i := range batch.Sel.Indices() {
		key := k{aOut[i], bOut[i]}
		want, ok := naive[key]
		if !ok {
			t.Errorf("unexpected key %+v", key)
			continue
		}
		if sumOut[i] != want {
			t.Errorf("key %+v: SUM(v) = %d, want %d", key, sumOut[i], want)
		}
	}
}

func TestGroupByOpMultiNullsPerColumnDistinct(t *testing.T) {
	// SQL semantics: (NULL, 'x') and ('y', NULL) are distinct groups.
	// Two tuples both (NULL, NULL) form one group. Verify the null
	// tag byte in the composite encoding is what makes this work.
	n := 6
	a := []int64{0, 0, 0, 0, 0, 0}
	bStr := []string{"x", "x", "", "", "", ""}
	v := []int64{10, 20, 30, 40, 50, 60}
	aNulls := storage.NewNullBitmap(n)
	bNulls := storage.NewNullBitmap(n)
	// rows:
	// 0: (NULL, "x")
	// 1: (NULL, "x")   -> same as 0
	// 2: ("y", NULL) but we have a[2]=0, so make a[2]=7
	// 3: ("y", NULL) same as 2
	// 4: (NULL, NULL)
	// 5: (NULL, NULL) same as 4
	a[2] = 7
	a[3] = 7
	aNulls.SetNull(0)
	aNulls.SetNull(1)
	aNulls.SetNull(4)
	aNulls.SetNull(5)
	bNulls.SetNull(2)
	bNulls.SetNull(3)
	bNulls.SetNull(4)
	bNulls.SetNull(5)

	aCol, err := storage.NewColumnChunk("a", storage.NewInt64ColumnFromSlice(a), aNulls)
	if err != nil {
		t.Fatal(err)
	}
	bCol, err := storage.NewColumnChunk("b", storage.NewStringColumnFromSlice(bStr), bNulls)
	if err != nil {
		t.Fatal(err)
	}
	vCol := storage.NewColumnChunkNoNulls("v", storage.NewInt64ColumnFromSlice(v))
	rg, err := storage.NewRowGroup(aCol, bCol, vCol)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := NewScanOp(rg, []string{"a", "b", "v"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOpMulti(scan,
		[]int{0, 1},
		[]storage.ColumnType{storage.TypeInt64, storage.TypeString},
		[]AggregateSpec{{Name: "sum_v", ColIndex: 2, Agg: &Int64Sum{}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	batch, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	if batch.Len() != 3 {
		t.Fatalf("groups = %d, want 3 ((NULL,\"x\"), (7,NULL), (NULL,NULL))", batch.Len())
	}

	// Walk groups and verify each distinct null-pattern tuple got
	// its own ordinal with the right sum.
	aVec := batch.Vectors[0]
	bVec := batch.Vectors[1]
	aVals := aVec.Int64s()
	bVals := bVec.Strings()
	sumOut := batch.Vectors[2].Int64s()
	saw := map[string]int64{}
	for _, i := range batch.Sel.Indices() {
		aNull := aVec.IsNull(int(i))
		bNull := bVec.IsNull(int(i))
		var tag string
		switch {
		case aNull && bNull:
			tag = "(NULL,NULL)"
		case aNull && !bNull:
			tag = "(NULL," + bVals[i] + ")"
		case !aNull && bNull:
			tag = fmt.Sprintf("(%d,NULL)", aVals[i])
		default:
			tag = fmt.Sprintf("(%d,%s)", aVals[i], bVals[i])
		}
		saw[tag] = sumOut[i]
	}
	wantSums := map[string]int64{
		"(NULL,x)":    30,  // 10+20
		"(7,NULL)":    70,  // 30+40
		"(NULL,NULL)": 110, // 50+60
	}
	for tag, want := range wantSums {
		if saw[tag] != want {
			t.Errorf("group %s: SUM(v) = %d, want %d", tag, saw[tag], want)
		}
	}
}

func TestGroupByOpMultiMultiBatchAccumulation(t *testing.T) {
	// Multi-batch drain through the composite-key path.
	const n = 3000
	cities := []string{"A", "B"}
	rg := makeGroupByRowGroup(t, n, cities)

	type key struct {
		city string
		age  int64
	}
	naive := map[key]int64{}
	citiesCol := rg.ColumnByName("city").Values.(*storage.StringColumn).Values()
	agesCol := rg.ColumnByName("age").Values.(*storage.Int64Column).Values()
	for i := 0; i < n; i++ {
		naive[key{citiesCol[i], agesCol[i]}]++
	}

	scan, err := NewScanOp(rg, []string{"age", "city"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOpMulti(scan,
		[]int{1, 0},
		[]storage.ColumnType{storage.TypeString, storage.TypeInt64},
		[]AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	if b.Len() != len(naive) {
		t.Fatalf("groups = %d, want %d", b.Len(), len(naive))
	}
	cityOut := b.Vectors[0].Strings()
	ageOut := b.Vectors[1].Int64s()
	countOut := b.Vectors[2].Int64s()
	for _, i := range b.Sel.Indices() {
		k := key{cityOut[i], ageOut[i]}
		if countOut[i] != naive[k] {
			t.Errorf("%+v: count = %d, want %d", k, countOut[i], naive[k])
		}
	}
}

func TestGroupByOpMultiReset(t *testing.T) {
	const n = 600
	rg := makeGroupByRowGroup(t, n, []string{"A", "B", "C"})
	scan, err := NewScanOp(rg, []string{"age", "city"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOpMulti(scan,
		[]int{1, 0},
		[]storage.ColumnType{storage.TypeString, storage.TypeInt64},
		[]AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}},
	)
	if err != nil {
		t.Fatal(err)
	}

	b1, _ := op.Next()
	firstGroups := b1.Len()
	// Collect for comparison.
	type row struct {
		c string
		a int64
		n int64
	}
	collect := func(b *Batch) []row {
		out := make([]row, 0, b.Len())
		cities := b.Vectors[0].Strings()
		ages := b.Vectors[1].Int64s()
		counts := b.Vectors[2].Int64s()
		for _, i := range b.Sel.Indices() {
			out = append(out, row{cities[i], ages[i], counts[i]})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].c != out[j].c {
				return out[i].c < out[j].c
			}
			return out[i].a < out[j].a
		})
		return out
	}
	firstRows := collect(b1)

	op.Reset()
	b2, _ := op.Next()
	if b2.Len() != firstGroups {
		t.Fatalf("after Reset, groups = %d, want %d", b2.Len(), firstGroups)
	}
	secondRows := collect(b2)
	for i := range firstRows {
		if firstRows[i] != secondRows[i] {
			t.Errorf("Reset row %d mismatch: first=%+v second=%+v", i, firstRows[i], secondRows[i])
		}
	}
}

func TestNewGroupByOpMultiValidation(t *testing.T) {
	rg := makeGroupByRowGroup(t, 10, []string{"A"})
	scan, _ := NewScanOp(rg, []string{"age", "city"})

	mk := func(kci []int, kts []storage.ColumnType, specs []AggregateSpec) error {
		_, err := NewGroupByOpMulti(scan, kci, kts, specs)
		return err
	}
	if err := mk(nil, nil, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("empty key cols: want error")
	}
	if err := mk([]int{0, 1}, []storage.ColumnType{storage.TypeInt64}, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("mismatched lens: want error")
	}
	if err := mk([]int{-1}, []storage.ColumnType{storage.TypeInt64}, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("negative keyColIndex: want error")
	}
	if err := mk([]int{0}, []storage.ColumnType{storage.TypeFloat64}, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("unsupported key type: want error")
	}
	if err := mk([]int{0}, []storage.ColumnType{storage.TypeInt64}, nil); err == nil {
		t.Error("nil specs: want error")
	}
	dup := &Int64Sum{}
	if err := mk([]int{0}, []storage.ColumnType{storage.TypeInt64}, []AggregateSpec{
		{Name: "a", ColIndex: 0, Agg: dup},
		{Name: "b", ColIndex: 0, Agg: dup},
	}); err == nil {
		t.Error("duplicate aggregator pointer: want error")
	}
	if _, err := NewGroupByOpMulti(nil, []int{0}, []storage.ColumnType{storage.TypeInt64}, []AggregateSpec{{Name: "n", ColIndex: 0, Agg: &CountStar{}}}); err == nil {
		t.Error("nil child: want error")
	}
}

func TestGroupByOpTooManyGroupsErrs(t *testing.T) {
	// Step 4 limitation: ≤ VectorSize (1024) distinct groups, because
	// the result Vector's backing slice is VectorSize. Step 5 will
	// lift this. Until then, exceeding the cap must surface as a
	// sticky error on Err() and Next() must stop early.
	n := VectorSize + 100 // 1124 unique keys
	keys := make([]int64, n)
	for i := range n {
		keys[i] = int64(i)
	}
	keyCol := storage.NewColumnChunkNoNulls("k", storage.NewInt64ColumnFromSlice(keys))
	rg, err := storage.NewRowGroup(keyCol)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := NewScanOp(rg, []string{"k"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if ok || b != nil {
		t.Fatalf("expected (nil, false) on group-cap overflow, got (%v, %v)", b, ok)
	}
	if op.Err() == nil {
		t.Error("expected sticky error on Err(), got nil")
	}
	// numGroups must not have ballooned past the cap — the check is
	// inside the probe loop, not at batch end.
	if op.numGroups > VectorSize {
		t.Errorf("numGroups = %d exceeded cap %d (check fires too late)", op.numGroups, VectorSize)
	}
	if len(op.keysInt64) > VectorSize {
		t.Errorf("keysInt64 len = %d exceeded cap %d", len(op.keysInt64), VectorSize)
	}
	// Reset must clear the sticky error and let a subsequent drain
	// work again (over a smaller input).
	op.Reset()
	if op.Err() != nil {
		t.Errorf("Err() after Reset = %v, want nil", op.Err())
	}
}

func TestGroupByOpNullsClearedAcrossReset(t *testing.T) {
	// Reviewer blocker #1: make sure the key output Vector's null
	// bitmap does not leak null bits from a prior drain. We run a
	// fixture whose keys contain nulls first (so iteration 1 stamps
	// a null on the key Vector), Reset, then run a fixture with NO
	// nulls. Every emitted key must report IsNull == false.
	makeRG := func(withNulls bool) *storage.RowGroup {
		n := 50
		keys := make([]int64, n)
		for i := range n {
			keys[i] = int64(i % 3)
		}
		var nullBM *storage.NullBitmap
		if withNulls {
			nullBM = storage.NewNullBitmap(n)
			nullBM.SetNull(0)
			nullBM.SetNull(7)
		} else {
			nullBM = storage.NewNullBitmap(n)
		}
		kc, err := storage.NewColumnChunk("k", storage.NewInt64ColumnFromSlice(keys), nullBM)
		if err != nil {
			t.Fatal(err)
		}
		rg, err := storage.NewRowGroup(kc)
		if err != nil {
			t.Fatal(err)
		}
		return rg
	}

	// Two independent scans: op cannot rebind child, so we rebuild.
	// First drain: null keys present.
	scan1, _ := NewScanOp(makeRG(true), []string{"k"})
	op, err := NewGroupByOp(scan1, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "n", ColIndex: 0, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, ok := op.Next()
	if !ok {
		t.Fatal("first drain returned no batch")
	}
	sawNull := false
	for _, i := range first.Sel.Indices() {
		if first.Vectors[0].IsNull(int(i)) {
			sawNull = true
		}
	}
	if !sawNull {
		t.Fatal("first drain did not produce a null group — test precondition failed")
	}

	// Swap child for a no-null row group. Currently the child isn't
	// rebindable, so we just create a fresh op over the no-null rg;
	// the important check is that the prior null flag doesn't leak
	// forward through the out Vector when its .Reset() is called.
	// We simulate it by reaching into op to rebind the child. This
	// is a white-box test — operator reuse across different child
	// streams isn't part of the public contract, but the null-bit
	// clearing is what matters.
	scan2, _ := NewScanOp(makeRG(false), []string{"k"})
	op.child = scan2
	op.Reset()
	second, ok := op.Next()
	if !ok {
		t.Fatal("second drain returned no batch")
	}
	for _, i := range second.Sel.Indices() {
		if second.Vectors[0].IsNull(int(i)) {
			t.Errorf("row %d in second drain reports null, but no nulls were fed — null bitmap leaked across Reset", i)
		}
	}
}

func TestGroupByOpNullKeyMultiBatch(t *testing.T) {
	// Reviewer suggestion #7: the null-key group may be assigned in
	// a batch OTHER than the first, which exercises the "Grow for
	// the null group mid-stream" code path. Fixture: 3000 rows,
	// all keys non-null in the first VectorSize rows, nulls only
	// appearing from row VectorSize onward.
	const n = 3000
	keys := make([]int64, n)
	vals := make([]int64, n)
	nullBM := storage.NewNullBitmap(n)
	var naiveNullSum, naiveNullCount int64
	naive := map[int64]int64{}
	for i := range n {
		keys[i] = int64(i % 5)
		vals[i] = int64(i)
		if i >= VectorSize && i%11 == 0 {
			nullBM.SetNull(i)
			naiveNullSum += vals[i]
			naiveNullCount++
			continue
		}
		naive[keys[i]] += vals[i]
	}
	kc, err := storage.NewColumnChunk("k", storage.NewInt64ColumnFromSlice(keys), nullBM)
	if err != nil {
		t.Fatal(err)
	}
	vc := storage.NewColumnChunkNoNulls("v", storage.NewInt64ColumnFromSlice(vals))
	rg, err := storage.NewRowGroup(kc, vc)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := NewScanOp(rg, []string{"k", "v"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewGroupByOp(scan, 0, storage.TypeInt64, []AggregateSpec{
		{Name: "sum_v", ColIndex: 1, Agg: &Int64Sum{}},
		{Name: "n", ColIndex: 1, Agg: &CountStar{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := drainGroupBy(t, op)
	if b.Len() != len(naive)+1 {
		t.Fatalf("group count = %d, want %d", b.Len(), len(naive)+1)
	}
	keysOut := b.Vectors[0].Int64s()
	sumsOut := b.Vectors[1].Int64s()
	countsOut := b.Vectors[2].Int64s()
	sawNull := false
	for _, i := range b.Sel.Indices() {
		if b.Vectors[0].IsNull(int(i)) {
			sawNull = true
			if sumsOut[i] != naiveNullSum {
				t.Errorf("null group SUM(v) = %d, want %d", sumsOut[i], naiveNullSum)
			}
			if countsOut[i] != naiveNullCount {
				t.Errorf("null group COUNT(*) = %d, want %d", countsOut[i], naiveNullCount)
			}
			continue
		}
		if want, ok := naive[keysOut[i]]; ok {
			if sumsOut[i] != want {
				t.Errorf("key %d: SUM(v) = %d, want %d", keysOut[i], sumsOut[i], want)
			}
		} else {
			t.Errorf("unexpected key %d", keysOut[i])
		}
	}
	if !sawNull {
		t.Error("null group never emitted (assigned mid-stream — this is the path under test)")
	}
}
