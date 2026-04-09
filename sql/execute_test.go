package sql

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 5 Step 3: end-to-end integration tests verifying that
// sql.Execute produces byte-identical output to a hand-wired
// ScanOp on the same RowGroup.

func makeTestRowGroup(t *testing.T) *storage.RowGroup {
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
	rg, err := storage.NewRowGroup(ages, names, cities)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

func TestExecuteSelectStar(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT * FROM people")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	// 3 columns, 5 rows.
	if b.NumColumns() != 3 {
		t.Errorf("columns = %d, want 3", b.NumColumns())
	}
	if b.Len() != 5 {
		t.Errorf("rows = %d, want 5", b.Len())
	}
	// Verify column content matches the fixture.
	ages := b.Vectors[0].Int64s()
	if ages[0] != 25 || ages[4] != 45 {
		t.Errorf("age[0]=%d age[4]=%d", ages[0], ages[4])
	}
	names := b.Vectors[1].Strings()
	if names[0] != "Alice" || names[4] != "Eve" {
		t.Errorf("name[0]=%q name[4]=%q", names[0], names[4])
	}
	// Second Next → EOF.
	if _, ok := op.Next(); ok {
		t.Error("expected EOF after first batch")
	}
}

func TestExecuteSelectColumns(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT name, age FROM people")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	// Projection: name first, age second.
	if b.NumColumns() != 2 {
		t.Fatalf("columns = %d, want 2", b.NumColumns())
	}
	names := b.Vectors[0].Strings()
	ages := b.Vectors[1].Int64s()
	if names[0] != "Alice" || ages[0] != 25 {
		t.Errorf("row 0: name=%q age=%d", names[0], ages[0])
	}
}

func TestExecuteSelectSingleColumn(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT city FROM whatever")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	if b.NumColumns() != 1 {
		t.Fatalf("columns = %d, want 1", b.NumColumns())
	}
	cities := b.Vectors[0].Strings()
	if cities[2] != "Paris" {
		t.Errorf("city[2] = %q, want %q", cities[2], "Paris")
	}
}

func TestExecuteUnknownColumn(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT bogus FROM t")
	if err == nil {
		t.Fatal("unknown column should error")
	}
}

func TestExecuteResetProducesSameResult(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT age FROM t")
	if err != nil {
		t.Fatal(err)
	}
	// First drain.
	b1, _ := op.Next()
	first := make([]int64, len(b1.Vectors[0].Int64s()))
	copy(first, b1.Vectors[0].Int64s())
	// Reset + second drain.
	op.Reset()
	b2, _ := op.Next()
	second := b2.Vectors[0].Int64s()
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("row %d: first=%d second=%d", i, first[i], second[i])
		}
	}
}

func TestExecuteSemicolonOptional(t *testing.T) {
	rg := makeTestRowGroup(t)
	if _, err := Execute(rg, "SELECT age FROM t;"); err != nil {
		t.Errorf("trailing semicolon should be accepted: %v", err)
	}
}

func TestExecuteDuplicateColumns(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT age, age FROM t")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	if b.NumColumns() != 2 {
		t.Fatalf("columns = %d, want 2", b.NumColumns())
	}
	// Both columns should have the same values.
	a0 := b.Vectors[0].Int64s()
	a1 := b.Vectors[1].Int64s()
	for i := range a0 {
		if a0[i] != a1[i] {
			t.Errorf("row %d: col0=%d col1=%d", i, a0[i], a1[i])
		}
	}
}

func TestExecuteWhereInt64Gt(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT name, age FROM people WHERE age > 30")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	// Fixture: ages 25,30,35,40,45. Only 35,40,45 survive.
	if b.Len() != 3 {
		t.Fatalf("rows = %d, want 3", b.Len())
	}
	names := b.Vectors[0].Strings()
	ages := b.Vectors[1].Int64s()
	for _, i := range b.Sel.Indices() {
		if ages[i] <= 30 {
			t.Errorf("row %d: age=%d should not survive WHERE age > 30", i, ages[i])
		}
		_ = names[i] // ensure projection includes name
	}
}

func TestExecuteWhereStringEq(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT name FROM people WHERE city = 'Paris'")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	// Fixture: cities Paris,Lyon,Paris,Lyon,Paris → 3 Paris rows.
	if b.Len() != 3 {
		t.Fatalf("rows = %d, want 3", b.Len())
	}
	// Output should be 1 column (name only) — city was added for
	// the filter and then dropped via ProjectOp.
	if b.NumColumns() != 1 {
		t.Fatalf("columns = %d, want 1 (city should be projected out)", b.NumColumns())
	}
}

func TestExecuteWhereColumnInSelectList(t *testing.T) {
	rg := makeTestRowGroup(t)
	// age is in both SELECT and WHERE — no extra column, no ProjectOp.
	op, err := Execute(rg, "SELECT age FROM t WHERE age >= 40")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	if b.Len() != 2 { // 40, 45
		t.Fatalf("rows = %d, want 2", b.Len())
	}
	if b.NumColumns() != 1 {
		t.Fatalf("columns = %d, want 1", b.NumColumns())
	}
}

func TestExecuteWhereSelectStar(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT * FROM t WHERE age < 30")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	// Only age=25 survives.
	if b.Len() != 1 {
		t.Fatalf("rows = %d, want 1", b.Len())
	}
	if b.NumColumns() != 3 {
		t.Fatalf("columns = %d, want 3", b.NumColumns())
	}
}

func TestExecuteWhereUnknownColumn(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT age FROM t WHERE bogus > 10")
	if err == nil {
		t.Fatal("unknown WHERE column should error")
	}
}

func TestExecuteWhereTypeMismatch(t *testing.T) {
	rg := makeTestRowGroup(t)
	// city is string, but comparing with int literal.
	_, err := Execute(rg, "SELECT name FROM t WHERE city > 5")
	if err == nil {
		t.Fatal("string column with int literal should error")
	}
}

func TestExecuteWhereStringNonEqErrors(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT name FROM t WHERE city > 'Paris'")
	if err == nil {
		t.Fatal("string > comparison should error (only = supported)")
	}
}

func TestExecuteWhereAllOps(t *testing.T) {
	rg := makeTestRowGroup(t)
	// ages: 25, 30, 35, 40, 45
	tests := []struct {
		sql  string
		want int
	}{
		{"SELECT age FROM t WHERE age = 30", 1},
		{"SELECT age FROM t WHERE age != 30", 4},
		{"SELECT age FROM t WHERE age < 35", 2},
		{"SELECT age FROM t WHERE age <= 35", 3},
		{"SELECT age FROM t WHERE age > 35", 2},
		{"SELECT age FROM t WHERE age >= 35", 3},
	}
	for _, tc := range tests {
		op, err := Execute(rg, tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		total := 0
		for {
			b, ok := op.Next()
			if !ok {
				break
			}
			total += b.Len()
		}
		if total != tc.want {
			t.Errorf("%s: got %d rows, want %d", tc.sql, total, tc.want)
		}
	}
}

// =====================================================================
// Aggregation (Step 5)
// =====================================================================

func TestExecuteScalarCount(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT COUNT(*) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	if b.Len() != 1 {
		t.Fatalf("rows = %d, want 1", b.Len())
	}
	if b.Vectors[0].Int64s()[0] != 5 {
		t.Errorf("COUNT(*) = %d, want 5", b.Vectors[0].Int64s()[0])
	}
}

func TestExecuteScalarCountWithWhere(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT COUNT(*) FROM t WHERE age > 30")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := op.Next()
	// ages 25,30,35,40,45 → 3 survive
	if b.Vectors[0].Int64s()[0] != 3 {
		t.Errorf("COUNT(*) WHERE age > 30 = %d, want 3", b.Vectors[0].Int64s()[0])
	}
}

func TestExecuteScalarMultiAgg(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT COUNT(*), SUM(age), MIN(age), MAX(age) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := op.Next()
	count := b.Vectors[0].Int64s()[0]
	sum := b.Vectors[1].Int64s()[0]
	min := b.Vectors[2].Int64s()[0]
	max := b.Vectors[3].Int64s()[0]
	if count != 5 || sum != 175 || min != 25 || max != 45 {
		t.Errorf("count=%d sum=%d min=%d max=%d", count, sum, min, max)
	}
}

func TestExecuteGroupBySingleKey(t *testing.T) {
	// SELECT city, COUNT(*), SUM(age) FROM t GROUP BY city
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT city, COUNT(*), SUM(age) FROM people GROUP BY city")
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		city  string
		count int64
		sum   int64
	}
	got := map[string]row{}
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		cities := b.Vectors[0].Strings()
		counts := b.Vectors[1].Int64s()
		sums := b.Vectors[2].Int64s()
		for _, i := range b.Sel.Indices() {
			got[cities[i]] = row{cities[i], counts[i], sums[i]}
		}
	}
	// Fixture: Paris(25,35,45)=3 rows sum=105; Lyon(30,40)=2 rows sum=70
	if got["Paris"].count != 3 || got["Paris"].sum != 105 {
		t.Errorf("Paris: %+v", got["Paris"])
	}
	if got["Lyon"].count != 2 || got["Lyon"].sum != 70 {
		t.Errorf("Lyon: %+v", got["Lyon"])
	}
}

func TestExecuteGroupByWithWhere(t *testing.T) {
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT city, COUNT(*) FROM t WHERE age > 30 GROUP BY city")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		cities := b.Vectors[0].Strings()
		counts := b.Vectors[1].Int64s()
		for _, i := range b.Sel.Indices() {
			got[cities[i]] = counts[i]
		}
	}
	// After age > 30: Paris(35,45)=2, Lyon(40)=1
	if got["Paris"] != 2 || got["Lyon"] != 1 {
		t.Errorf("got %v", got)
	}
}

func TestExecuteGroupByMultiKey(t *testing.T) {
	// Multi-key: GROUP BY city, age (produces one group per unique pair)
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT city, age, COUNT(*) FROM t GROUP BY city, age")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		total += b.Len()
		// Each row is a unique (city, age) pair → COUNT must be 1.
		counts := b.Vectors[2].Int64s()
		for _, i := range b.Sel.Indices() {
			if counts[i] != 1 {
				t.Errorf("expected COUNT=1 per unique pair, got %d", counts[i])
			}
		}
	}
	// 5 rows with unique (city, age) → 5 groups.
	if total != 5 {
		t.Errorf("total groups = %d, want 5", total)
	}
}

func TestExecuteAggOnStringColumnErrors(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT SUM(city) FROM t")
	if err == nil {
		t.Fatal("SUM on string column should error")
	}
}

func TestExecutePlainColumnWithoutGroupByErrors(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT city, COUNT(*) FROM t")
	if err == nil {
		t.Fatal("plain column with aggregate and no GROUP BY should error")
	}
}

func TestExecuteColumnNotInGroupByErrors(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT city, name, COUNT(*) FROM t GROUP BY city")
	if err == nil {
		t.Fatal("column not in GROUP BY should error")
	}
}

func TestExecuteGroupByInterleavedSelectOrder(t *testing.T) {
	// SELECT list: COUNT(*), city — different from GroupByOp output
	// [city, COUNT(*)]. Planner must add ProjectOp to reorder.
	rg := makeTestRowGroup(t)
	op, err := Execute(rg, "SELECT COUNT(*), city FROM t GROUP BY city")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := op.Next()
	if !ok {
		t.Fatal("no batch")
	}
	// Column 0 should be COUNT (int64), column 1 should be city (string).
	if b.Vectors[0].Type != storage.TypeInt64 {
		t.Errorf("col 0 type = %s, want int64 (COUNT)", b.Vectors[0].Type)
	}
	if b.Vectors[1].Type != storage.TypeString {
		t.Errorf("col 1 type = %s, want string (city)", b.Vectors[1].Type)
	}
}

func TestExecuteLexerError(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT @")
	if err == nil {
		t.Fatal("lexer error should propagate")
	}
}
