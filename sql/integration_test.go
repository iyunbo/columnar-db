package sql

import (
	"testing"

	"github.com/iyunbo/columnar-db/exec"
	"github.com/iyunbo/columnar-db/storage"
)

// Phase 5 Step 6: broad end-to-end integration suite. Every test
// runs a SQL string through Execute and verifies the result against
// a known answer derived from the fixture. The fixture is the same
// 5-row 3-column RowGroup used throughout Phase 5 testing (ages
// 25/30/35/40/45, names Alice/Bob/Carol/Dave/Eve, cities
// Paris/Lyon/Paris/Lyon/Paris).

func makeIntegrationRG(t *testing.T) *storage.RowGroup {
	t.Helper()
	ages := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(
		[]int64{25, 30, 35, 40, 45},
	))
	names := storage.NewColumnChunkNoNulls("name", storage.NewStringColumnFromSlice(
		[]string{"Alice", "Bob", "Carol", "Dave", "Eve"},
	))
	cities := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(
		[]string{"Paris", "Lyon", "Paris", "Lyon", "Paris"},
	))
	prices := storage.NewColumnChunkNoNulls("price", storage.NewFloat64ColumnFromSlice(
		[]float64{10.5, 20.5, 30.5, 40.5, 50.5},
	))
	rg, err := storage.NewRowGroup(ages, names, cities, prices)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

// drain collects all rows from an operator into a slice of batches.
func drain(t *testing.T, rg *storage.RowGroup, query string) []*exec.Batch {
	t.Helper()
	op, err := Execute(rg, query)
	if err != nil {
		t.Fatalf("Execute(%q): %v", query, err)
	}
	var batches []*exec.Batch
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		batches = append(batches, b)
	}
	return batches
}

// drainCount returns the total row count across all batches.
func drainCount(t *testing.T, rg *storage.RowGroup, query string) int {
	t.Helper()
	batches := drain(t, rg, query)
	n := 0
	for _, b := range batches {
		n += b.Len()
	}
	return n
}

// =====================================================================
// SELECT / projection
// =====================================================================

func TestIntegrationSelectStar(t *testing.T) {
	rg := makeIntegrationRG(t)
	n := drainCount(t, rg, "SELECT * FROM t")
	if n != 5 {
		t.Errorf("rows = %d, want 5", n)
	}
}

func TestIntegrationSelectProjection(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT name, age FROM t")
	if len(bs) != 1 || bs[0].NumColumns() != 2 {
		t.Fatalf("batches=%d cols=%d", len(bs), bs[0].NumColumns())
	}
}

// =====================================================================
// WHERE
// =====================================================================

func TestIntegrationWhereGt(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT age FROM t WHERE age > 35"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestIntegrationWhereEq(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT name FROM t WHERE age = 30"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestIntegrationWhereStringEq(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT age FROM t WHERE city = 'Lyon'"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestIntegrationWhereFilterAll(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT age FROM t WHERE age > 100"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestIntegrationWhereColumnNotInSelect(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT name FROM t WHERE age > 40")
	if len(bs) != 1 || bs[0].NumColumns() != 1 {
		t.Fatalf("expected 1 column, got %d", bs[0].NumColumns())
	}
	if bs[0].Len() != 1 { // only age=45 survives
		t.Errorf("rows = %d, want 1", bs[0].Len())
	}
}

// =====================================================================
// Scalar aggregation
// =====================================================================

func TestIntegrationScalarCountStar(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT COUNT(*) FROM t")
	if bs[0].Vectors[0].Int64s()[0] != 5 {
		t.Errorf("COUNT(*) = %d", bs[0].Vectors[0].Int64s()[0])
	}
}

func TestIntegrationScalarSumMinMaxAvg(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT SUM(age), MIN(age), MAX(age), AVG(age) FROM t")
	b := bs[0]
	sum := b.Vectors[0].Int64s()[0]
	min := b.Vectors[1].Int64s()[0]
	max := b.Vectors[2].Int64s()[0]
	avg := b.Vectors[3].Float64s()[0]
	if sum != 175 || min != 25 || max != 45 || avg != 35.0 {
		t.Errorf("sum=%d min=%d max=%d avg=%v", sum, min, max, avg)
	}
}

func TestIntegrationScalarCountWithWhere(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT COUNT(*) FROM t WHERE age >= 35")
	if bs[0].Vectors[0].Int64s()[0] != 3 {
		t.Errorf("COUNT(*) WHERE age >= 35 = %d, want 3", bs[0].Vectors[0].Int64s()[0])
	}
}

func TestIntegrationScalarFloat64Sum(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT SUM(price) FROM t")
	sum := bs[0].Vectors[0].Float64s()[0]
	if sum != 152.5 { // 10.5+20.5+30.5+40.5+50.5
		t.Errorf("SUM(price) = %v, want 152.5", sum)
	}
}

// =====================================================================
// GROUP BY
// =====================================================================

func TestIntegrationGroupBySingleKey(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT city, COUNT(*), SUM(age) FROM t GROUP BY city")
	got := map[string][2]int64{}
	for _, b := range bs {
		cities := b.Vectors[0].Strings()
		counts := b.Vectors[1].Int64s()
		sums := b.Vectors[2].Int64s()
		for _, i := range b.Sel.Indices() {
			got[cities[i]] = [2]int64{counts[i], sums[i]}
		}
	}
	if got["Paris"] != [2]int64{3, 105} {
		t.Errorf("Paris = %v", got["Paris"])
	}
	if got["Lyon"] != [2]int64{2, 70} {
		t.Errorf("Lyon = %v", got["Lyon"])
	}
}

func TestIntegrationGroupByWithWhere(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT city, COUNT(*) FROM t WHERE age > 30 GROUP BY city")
	got := map[string]int64{}
	for _, b := range bs {
		cities := b.Vectors[0].Strings()
		counts := b.Vectors[1].Int64s()
		for _, i := range b.Sel.Indices() {
			got[cities[i]] = counts[i]
		}
	}
	if got["Paris"] != 2 || got["Lyon"] != 1 {
		t.Errorf("got %v", got)
	}
}

func TestIntegrationGroupByMultiKey(t *testing.T) {
	rg := makeIntegrationRG(t)
	n := drainCount(t, rg, "SELECT city, age, COUNT(*) FROM t GROUP BY city, age")
	if n != 5 { // 5 unique (city, age) pairs
		t.Errorf("groups = %d, want 5", n)
	}
}

func TestIntegrationGroupByAvg(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT city, AVG(age) FROM t GROUP BY city")
	got := map[string]float64{}
	for _, b := range bs {
		cities := b.Vectors[0].Strings()
		avgs := b.Vectors[1].Float64s()
		for _, i := range b.Sel.Indices() {
			got[cities[i]] = avgs[i]
		}
	}
	// Paris: (25+35+45)/3 = 35.0; Lyon: (30+40)/2 = 35.0
	if got["Paris"] != 35.0 || got["Lyon"] != 35.0 {
		t.Errorf("got %v", got)
	}
}

func TestIntegrationGroupByFloat64Agg(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT city, SUM(price), MIN(price), MAX(price) FROM t GROUP BY city")
	got := map[string][3]float64{}
	for _, b := range bs {
		cities := b.Vectors[0].Strings()
		sums := b.Vectors[1].Float64s()
		mins := b.Vectors[2].Float64s()
		maxs := b.Vectors[3].Float64s()
		for _, i := range b.Sel.Indices() {
			got[cities[i]] = [3]float64{sums[i], mins[i], maxs[i]}
		}
	}
	// Paris: 10.5+30.5+50.5=91.5, min=10.5, max=50.5
	if got["Paris"] != [3]float64{91.5, 10.5, 50.5} {
		t.Errorf("Paris = %v", got["Paris"])
	}
}

// =====================================================================
// ORDER BY
// =====================================================================

func TestIntegrationOrderByAsc(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT name, age FROM t ORDER BY age")
	ages := bs[0].Vectors[1].Int64s()
	for i := 1; i < len(ages); i++ {
		if ages[i] < ages[i-1] {
			t.Errorf("not sorted ASC: ages[%d]=%d < ages[%d]=%d", i, ages[i], i-1, ages[i-1])
		}
	}
}

func TestIntegrationOrderByDesc(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT name, age FROM t ORDER BY age DESC")
	ages := bs[0].Vectors[1].Int64s()
	for i := 1; i < len(ages); i++ {
		if ages[i] > ages[i-1] {
			t.Errorf("not sorted DESC: ages[%d]=%d > ages[%d]=%d", i, ages[i], i-1, ages[i-1])
		}
	}
}

func TestIntegrationOrderByString(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT name FROM t ORDER BY name")
	names := bs[0].Vectors[0].Strings()
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("not sorted: names[%d]=%q < names[%d]=%q", i, names[i], i-1, names[i-1])
		}
	}
}

func TestIntegrationOrderByWithWhere(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT age FROM t WHERE age > 30 ORDER BY age DESC")
	ages := bs[0].Vectors[0].Int64s()
	if len(ages) != 3 || ages[0] != 45 || ages[2] != 35 {
		t.Errorf("got %v, want [45 40 35]", ages)
	}
}

func TestIntegrationOrderByGroupBy(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT city, COUNT(*) FROM t GROUP BY city ORDER BY city")
	cities := bs[0].Vectors[0].Strings()
	if cities[0] != "Lyon" || cities[1] != "Paris" {
		t.Errorf("got %v, want [Lyon Paris]", cities)
	}
}

// =====================================================================
// LIMIT
// =====================================================================

func TestIntegrationLimit(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT age FROM t LIMIT 3"); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

func TestIntegrationLimitZero(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT age FROM t LIMIT 0"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestIntegrationLimitLargerThanTable(t *testing.T) {
	rg := makeIntegrationRG(t)
	if n := drainCount(t, rg, "SELECT age FROM t LIMIT 100"); n != 5 {
		t.Errorf("rows = %d, want 5", n)
	}
}

func TestIntegrationOrderByLimit(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT age FROM t ORDER BY age DESC LIMIT 2")
	n := 0
	for _, b := range bs {
		n += b.Len()
	}
	if n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	ages := bs[0].Vectors[0].Int64s()
	idx := bs[0].Sel.Indices()
	if ages[idx[0]] != 45 || ages[idx[1]] != 40 {
		t.Errorf("top 2 ages = %d, %d; want 45, 40", ages[idx[0]], ages[idx[1]])
	}
}

func TestIntegrationGroupByOrderByLimit(t *testing.T) {
	rg := makeIntegrationRG(t)
	bs := drain(t, rg, "SELECT city, COUNT(*) FROM t GROUP BY city ORDER BY city LIMIT 1")
	if len(bs) == 0 || bs[0].Len() != 1 {
		t.Fatalf("expected 1 row")
	}
	city := bs[0].Vectors[0].Strings()[bs[0].Sel.Indices()[0]]
	if city != "Lyon" {
		t.Errorf("city = %q, want Lyon (first alphabetically)", city)
	}
}

// =====================================================================
// Error cases
// =====================================================================

func TestIntegrationErrorUnknownColumn(t *testing.T) {
	rg := makeIntegrationRG(t)
	if _, err := Execute(rg, "SELECT bogus FROM t"); err == nil {
		t.Error("unknown column should error")
	}
}

func TestIntegrationErrorSyntax(t *testing.T) {
	rg := makeIntegrationRG(t)
	if _, err := Execute(rg, "SELEC age FROM t"); err == nil {
		t.Error("syntax error should propagate")
	}
}

func TestIntegrationErrorTypeMismatch(t *testing.T) {
	rg := makeIntegrationRG(t)
	if _, err := Execute(rg, "SELECT age FROM t WHERE city > 5"); err == nil {
		t.Error("string column with int literal should error")
	}
}

func TestIntegrationErrorAggOnString(t *testing.T) {
	rg := makeIntegrationRG(t)
	if _, err := Execute(rg, "SELECT SUM(name) FROM t"); err == nil {
		t.Error("SUM on string column should error")
	}
}

// =====================================================================
// Reset / reuse
// =====================================================================

func TestIntegrationResetGroupBy(t *testing.T) {
	rg := makeIntegrationRG(t)
	op, err := Execute(rg, "SELECT city, COUNT(*) FROM t GROUP BY city")
	if err != nil {
		t.Fatal(err)
	}
	count1 := 0
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		count1 += b.Len()
	}
	op.Reset()
	count2 := 0
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		count2 += b.Len()
	}
	if count1 != count2 || count1 != 2 {
		t.Errorf("count1=%d count2=%d, want 2", count1, count2)
	}
}
