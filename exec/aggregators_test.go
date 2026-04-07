package exec

import (
	"math"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Test helpers ---------------------------------------------------------

func makeInt64Vec(t *testing.T, vals []int64, nullPositions []int) *Vector {
	t.Helper()
	v := NewVector(storage.TypeInt64)
	for _, x := range vals {
		if err := v.AppendInt64(x); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range nullPositions {
		v.Nulls.SetNull(p)
	}
	return v
}

func makeFloat64Vec(t *testing.T, vals []float64, nullPositions []int) *Vector {
	t.Helper()
	v := NewVector(storage.TypeFloat64)
	for _, x := range vals {
		if err := v.AppendFloat64(x); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range nullPositions {
		v.Nulls.SetNull(p)
	}
	return v
}

func fullSel(n int) *Selection {
	s := NewSelection()
	s.ResetFull(n)
	return s
}

// finalizeAll calls Finalize for groups 0..numGroups-1 in order and
// returns the resulting output vector. The vector is sized to
// numGroups via successive Append/AppendNull calls.
func finalizeAll(t *testing.T, agg Aggregator, numGroups int) *Vector {
	t.Helper()
	out := NewVector(agg.OutputType())
	for g := 0; g < numGroups; g++ {
		agg.Finalize(g, out)
	}
	return out
}

// CountStar -----------------------------------------------------------

func TestCountStarScalar(t *testing.T) {
	c := &CountStar{}
	c.Init(1)
	v := makeInt64Vec(t, []int64{10, 20, 30, 40, 50}, nil)
	c.Update(v, fullSel(5), nil)
	out := finalizeAll(t, c, 1)
	if got := out.Int64s()[0]; got != 5 {
		t.Errorf("CountStar = %d, want 5", got)
	}
	if out.IsNull(0) {
		t.Errorf("CountStar should never produce null")
	}
}

func TestCountStarIgnoresNulls(t *testing.T) {
	// COUNT(*) counts rows regardless of any column's nulls — it
	// counts what the upstream filter let through, full stop.
	c := &CountStar{}
	c.Init(1)
	v := makeInt64Vec(t, []int64{10, 20, 30}, []int{0, 2})
	c.Update(v, fullSel(3), nil)
	out := finalizeAll(t, c, 1)
	if got := out.Int64s()[0]; got != 3 {
		t.Errorf("CountStar (with input nulls) = %d, want 3", got)
	}
}

func TestCountStarGrouped(t *testing.T) {
	c := &CountStar{}
	c.Init(3)
	v := makeInt64Vec(t, []int64{1, 2, 3, 4, 5}, nil)
	gids := []int32{0, 1, 0, 2, 1}
	c.Update(v, fullSel(5), gids)
	out := finalizeAll(t, c, 3)
	want := []int64{2, 2, 1}
	for g, w := range want {
		if got := out.Int64s()[g]; got != w {
			t.Errorf("group %d: got %d, want %d", g, got, w)
		}
	}
}

func TestCountStarEmptyGroup(t *testing.T) {
	c := &CountStar{}
	c.Init(3)
	// Only feeds groups 0 and 2; group 1 stays empty.
	v := makeInt64Vec(t, []int64{1, 2}, nil)
	gids := []int32{0, 2}
	c.Update(v, fullSel(2), gids)
	out := finalizeAll(t, c, 3)
	if got := out.Int64s()[1]; got != 0 {
		t.Errorf("empty group should count 0, got %d", got)
	}
	if out.IsNull(1) {
		t.Errorf("CountStar of empty group is 0, not null")
	}
}

// Int64Sum ------------------------------------------------------------

func TestInt64SumScalar(t *testing.T) {
	a := &Int64Sum{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{10, 20, 30, 40}, nil)
	a.Update(v, fullSel(4), nil)
	out := finalizeAll(t, a, 1)
	if got := out.Int64s()[0]; got != 100 {
		t.Errorf("sum = %d, want 100", got)
	}
}

func TestInt64SumNullAware(t *testing.T) {
	a := &Int64Sum{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{10, 20, 30, 40}, []int{1, 3})
	a.Update(v, fullSel(4), nil)
	out := finalizeAll(t, a, 1)
	if got := out.Int64s()[0]; got != 40 {
		t.Errorf("sum (with nulls) = %d, want 40", got)
	}
}

func TestInt64SumAllNull(t *testing.T) {
	a := &Int64Sum{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{10, 20}, []int{0, 1})
	a.Update(v, fullSel(2), nil)
	out := finalizeAll(t, a, 1)
	if !out.IsNull(0) {
		t.Errorf("all-null group should produce null sum")
	}
}

func TestInt64SumGrouped(t *testing.T) {
	a := &Int64Sum{}
	a.Init(3)
	v := makeInt64Vec(t, []int64{10, 20, 30, 40, 50}, nil)
	gids := []int32{0, 1, 0, 2, 1}
	a.Update(v, fullSel(5), gids)
	out := finalizeAll(t, a, 3)
	want := []int64{40, 70, 40}
	for g, w := range want {
		if got := out.Int64s()[g]; got != w {
			t.Errorf("group %d: got %d, want %d", g, got, w)
		}
	}
}

func TestInt64SumGroupedWithNulls(t *testing.T) {
	a := &Int64Sum{}
	a.Init(3)
	// vals: 10, 20, NULL, 40, NULL — group 0: 10, group 1: 20+NULL=20,
	// group 2: NULL → null
	v := makeInt64Vec(t, []int64{10, 20, 30, 40, 50}, []int{2, 4})
	gids := []int32{0, 1, 1, 0, 2}
	a.Update(v, fullSel(5), gids)
	out := finalizeAll(t, a, 3)
	if got := out.Int64s()[0]; got != 50 {
		t.Errorf("group 0: got %d, want 50", got)
	}
	if got := out.Int64s()[1]; got != 20 {
		t.Errorf("group 1: got %d, want 20", got)
	}
	if !out.IsNull(2) {
		t.Errorf("group 2 (all-null): want null")
	}
}

// Int64Min / Int64Max -------------------------------------------------

func TestInt64Min(t *testing.T) {
	a := &Int64Min{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{30, 10, 20, 5, 40}, nil)
	a.Update(v, fullSel(5), nil)
	out := finalizeAll(t, a, 1)
	if got := out.Int64s()[0]; got != 5 {
		t.Errorf("min = %d, want 5", got)
	}
}

func TestInt64MinNullAware(t *testing.T) {
	a := &Int64Min{}
	a.Init(1)
	// 5 is null → min is 10
	v := makeInt64Vec(t, []int64{30, 10, 20, 5, 40}, []int{3})
	a.Update(v, fullSel(5), nil)
	out := finalizeAll(t, a, 1)
	if got := out.Int64s()[0]; got != 10 {
		t.Errorf("min (5 null) = %d, want 10", got)
	}
}

func TestInt64MinAllNull(t *testing.T) {
	a := &Int64Min{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{1, 2}, []int{0, 1})
	a.Update(v, fullSel(2), nil)
	out := finalizeAll(t, a, 1)
	if !out.IsNull(0) {
		t.Errorf("all-null min should be null")
	}
}

func TestInt64MaxGrouped(t *testing.T) {
	a := &Int64Max{}
	a.Init(2)
	v := makeInt64Vec(t, []int64{10, 50, 30, 20, 40}, nil)
	gids := []int32{0, 0, 1, 1, 0}
	a.Update(v, fullSel(5), gids)
	out := finalizeAll(t, a, 2)
	if got := out.Int64s()[0]; got != 50 {
		t.Errorf("group 0 max = %d, want 50", got)
	}
	if got := out.Int64s()[1]; got != 30 {
		t.Errorf("group 1 max = %d, want 30", got)
	}
}

// Int64Avg ------------------------------------------------------------

func TestInt64Avg(t *testing.T) {
	a := &Int64Avg{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{10, 20, 30}, nil)
	a.Update(v, fullSel(3), nil)
	out := finalizeAll(t, a, 1)
	if got := out.Float64s()[0]; got != 20.0 {
		t.Errorf("avg = %v, want 20", got)
	}
}

func TestInt64AvgAllNull(t *testing.T) {
	a := &Int64Avg{}
	a.Init(1)
	v := makeInt64Vec(t, []int64{10}, []int{0})
	a.Update(v, fullSel(1), nil)
	out := finalizeAll(t, a, 1)
	if !out.IsNull(0) {
		t.Errorf("all-null avg should be null (not divide-by-zero)")
	}
}

func TestInt64AvgGrouped(t *testing.T) {
	a := &Int64Avg{}
	a.Init(2)
	v := makeInt64Vec(t, []int64{10, 20, 30, 40}, nil)
	gids := []int32{0, 0, 1, 1}
	a.Update(v, fullSel(4), gids)
	out := finalizeAll(t, a, 2)
	if got := out.Float64s()[0]; got != 15 {
		t.Errorf("group 0 avg = %v, want 15", got)
	}
	if got := out.Float64s()[1]; got != 35 {
		t.Errorf("group 1 avg = %v, want 35", got)
	}
}

// Float64 variants ----------------------------------------------------

func TestFloat64SumMinMaxAvg(t *testing.T) {
	v := makeFloat64Vec(t, []float64{1.5, 2.5, 3.5, 4.5}, nil)

	sum := &Float64Sum{}
	sum.Init(1)
	sum.Update(v, fullSel(4), nil)
	if got := finalizeAll(t, sum, 1).Float64s()[0]; got != 12.0 {
		t.Errorf("Float64Sum = %v, want 12", got)
	}

	min := &Float64Min{}
	min.Init(1)
	min.Update(v, fullSel(4), nil)
	if got := finalizeAll(t, min, 1).Float64s()[0]; got != 1.5 {
		t.Errorf("Float64Min = %v, want 1.5", got)
	}

	max := &Float64Max{}
	max.Init(1)
	max.Update(v, fullSel(4), nil)
	if got := finalizeAll(t, max, 1).Float64s()[0]; got != 4.5 {
		t.Errorf("Float64Max = %v, want 4.5", got)
	}

	avg := &Float64Avg{}
	avg.Init(1)
	avg.Update(v, fullSel(4), nil)
	if got := finalizeAll(t, avg, 1).Float64s()[0]; got != 3.0 {
		t.Errorf("Float64Avg = %v, want 3", got)
	}
}

func TestFloat64NaN(t *testing.T) {
	// Documented behavior (per the file header comment): NaN never
	// moves Min or Max but DOES contribute to Sum and (via count) Avg.
	v := makeFloat64Vec(t, []float64{1.0, math.NaN(), 3.0}, nil)

	min := &Float64Min{}
	min.Init(1)
	min.Update(v, fullSel(3), nil)
	if got := finalizeAll(t, min, 1).Float64s()[0]; got != 1.0 {
		t.Errorf("Float64Min with NaN = %v, want 1.0 (NaN should not displace)", got)
	}

	max := &Float64Max{}
	max.Init(1)
	max.Update(v, fullSel(3), nil)
	if got := finalizeAll(t, max, 1).Float64s()[0]; got != 3.0 {
		t.Errorf("Float64Max with NaN = %v, want 3.0", got)
	}

	sum := &Float64Sum{}
	sum.Init(1)
	sum.Update(v, fullSel(3), nil)
	if got := finalizeAll(t, sum, 1).Float64s()[0]; !math.IsNaN(got) {
		t.Errorf("Float64Sum with NaN = %v, want NaN (NaN poisons sum)", got)
	}
}

// Multi-batch + Grow --------------------------------------------------

func TestInt64SumMultiBatch(t *testing.T) {
	// Aggregators are stateful across batches — this is the actual
	// AggregateOp/GroupByOp use case. State must accumulate.
	a := &Int64Sum{}
	a.Init(2)
	v1 := makeInt64Vec(t, []int64{10, 20}, nil)
	v2 := makeInt64Vec(t, []int64{30, 40, 50}, nil)
	a.Update(v1, fullSel(2), []int32{0, 1})
	a.Update(v2, fullSel(3), []int32{0, 1, 0})
	out := finalizeAll(t, a, 2)
	// group 0: 10 + 30 + 50 = 90; group 1: 20 + 40 = 60
	if got := out.Int64s()[0]; got != 90 {
		t.Errorf("group 0 sum = %d, want 90", got)
	}
	if got := out.Int64s()[1]; got != 60 {
		t.Errorf("group 1 sum = %d, want 60", got)
	}
}

func TestGrowPreservesState(t *testing.T) {
	// GroupByOp grows the hash table mid-query; aggregators must
	// preserve existing per-group state on Grow.
	a := &Int64Sum{}
	a.Init(2)
	v := makeInt64Vec(t, []int64{10, 20}, nil)
	a.Update(v, fullSel(2), []int32{0, 1})

	a.Grow(5)

	v2 := makeInt64Vec(t, []int64{100, 200, 300}, nil)
	a.Update(v2, fullSel(3), []int32{2, 3, 4})

	out := finalizeAll(t, a, 5)
	want := []int64{10, 20, 100, 200, 300}
	for g, w := range want {
		if got := out.Int64s()[g]; got != w {
			t.Errorf("group %d after Grow: got %d, want %d", g, got, w)
		}
	}
}

// Selection respect ---------------------------------------------------

func TestAggregatorsRespectSelection(t *testing.T) {
	// Aggregators iterate sel.Indices(), not 0..vec.Len(), so a
	// narrowed selection from upstream FilterOp must skip filtered rows.
	v := makeInt64Vec(t, []int64{10, 20, 30, 40, 50}, nil)
	sel := NewSelection()
	// Pick rows 0, 2, 4 (values 10, 30, 50; sum = 90).
	sel.Add(0)
	sel.Add(2)
	sel.Add(4)

	a := &Int64Sum{}
	a.Init(1)
	a.Update(v, sel, nil)
	out := finalizeAll(t, a, 1)
	if got := out.Int64s()[0]; got != 90 {
		t.Errorf("sum over partial selection = %d, want 90", got)
	}
}

// Compound: scalar agg matches naive baseline -------------------------

func TestAggregatorsMatchNaiveBaseline(t *testing.T) {
	// Cross-check every numeric aggregator against a naive scalar
	// loop over the same data, with nulls injected. This is the
	// correctness twin for Step 3's AggregateOp benchmark.
	const n = 1000
	vals := make([]int64, n)
	nulls := make([]int, 0, n/10)
	for i := range vals {
		vals[i] = int64(i*7 - 100)
		if i%17 == 0 {
			nulls = append(nulls, i)
		}
	}
	v := makeInt64Vec(t, vals, nulls)

	// Naive baseline.
	var naiveSum int64
	naiveMin, naiveMax := int64(math.MaxInt64), int64(math.MinInt64)
	var naiveCount int64
	for i, x := range vals {
		isNull := false
		for _, p := range nulls {
			if p == i {
				isNull = true
				break
			}
		}
		if isNull {
			continue
		}
		naiveSum += x
		if x < naiveMin {
			naiveMin = x
		}
		if x > naiveMax {
			naiveMax = x
		}
		naiveCount++
	}
	naiveAvg := float64(naiveSum) / float64(naiveCount)

	sum := &Int64Sum{}
	sum.Init(1)
	sum.Update(v, fullSel(n), nil)
	if got := finalizeAll(t, sum, 1).Int64s()[0]; got != naiveSum {
		t.Errorf("Sum: vec=%d naive=%d", got, naiveSum)
	}

	min := &Int64Min{}
	min.Init(1)
	min.Update(v, fullSel(n), nil)
	if got := finalizeAll(t, min, 1).Int64s()[0]; got != naiveMin {
		t.Errorf("Min: vec=%d naive=%d", got, naiveMin)
	}

	max := &Int64Max{}
	max.Init(1)
	max.Update(v, fullSel(n), nil)
	if got := finalizeAll(t, max, 1).Int64s()[0]; got != naiveMax {
		t.Errorf("Max: vec=%d naive=%d", got, naiveMax)
	}

	avg := &Int64Avg{}
	avg.Init(1)
	avg.Update(v, fullSel(n), nil)
	if got := finalizeAll(t, avg, 1).Float64s()[0]; got != naiveAvg {
		t.Errorf("Avg: vec=%v naive=%v", got, naiveAvg)
	}
}
