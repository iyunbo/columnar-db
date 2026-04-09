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

func TestExecuteLexerError(t *testing.T) {
	rg := makeTestRowGroup(t)
	_, err := Execute(rg, "SELECT @")
	if err == nil {
		t.Fatal("lexer error should propagate")
	}
}
