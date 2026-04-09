package sql

import (
	"strings"
	"testing"
)

func mustTokenize(t *testing.T, input string) []Token {
	t.Helper()
	toks, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize(%q): %v", input, err)
	}
	return toks
}

func TestParseSelectStar(t *testing.T) {
	stmt, err := Parse(mustTokenize(t, "SELECT * FROM t"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt.Items) != 1 || !stmt.Items[0].Star {
		t.Fatalf("items = %+v, want [star]", stmt.Items)
	}
	if stmt.FromTable != "t" {
		t.Errorf("from = %q, want %q", stmt.FromTable, "t")
	}
}

func TestParseSelectColumns(t *testing.T) {
	stmt, err := Parse(mustTokenize(t, "SELECT name, age FROM people"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt.Items) != 2 {
		t.Fatalf("items count = %d, want 2", len(stmt.Items))
	}
	if stmt.Items[0].Column != "name" {
		t.Errorf("items[0].Column = %q", stmt.Items[0].Column)
	}
	if stmt.Items[1].Column != "age" {
		t.Errorf("items[1].Column = %q", stmt.Items[1].Column)
	}
	if stmt.FromTable != "people" {
		t.Errorf("from = %q", stmt.FromTable)
	}
}

func TestParseSelectWithSemicolon(t *testing.T) {
	stmt, err := Parse(mustTokenize(t, "SELECT age FROM t;"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt.Items) != 1 || stmt.Items[0].Column != "age" {
		t.Fatalf("items = %+v", stmt.Items)
	}
}

func TestParseAggregateSelectItem(t *testing.T) {
	stmt, err := Parse(mustTokenize(t, "SELECT COUNT(*), SUM(age) FROM t"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt.Items) != 2 {
		t.Fatalf("items count = %d", len(stmt.Items))
	}
	if stmt.Items[0].AggFunc == "" || stmt.Items[0].AggArg != "*" {
		t.Errorf("items[0] = %+v, want COUNT(*)", stmt.Items[0])
	}
	if stmt.Items[1].AggFunc == "" || stmt.Items[1].AggArg != "age" {
		t.Errorf("items[1] = %+v, want SUM(age)", stmt.Items[1])
	}
}

func TestParseMissingFrom(t *testing.T) {
	_, err := Parse(mustTokenize(t, "SELECT age"))
	if err == nil {
		t.Fatal("missing FROM should error")
	}
}

func TestParseMissingTableName(t *testing.T) {
	_, err := Parse(mustTokenize(t, "SELECT age FROM"))
	if err == nil {
		t.Fatal("missing table name should error")
	}
}

func TestParseExtraTokensAfterStatement(t *testing.T) {
	_, err := Parse(mustTokenize(t, "SELECT age FROM t WHERE"))
	if err == nil {
		t.Fatal("WHERE should error in Step 3 (not implemented)")
	}
}

func TestParseWhereNotImplemented(t *testing.T) {
	_, err := Parse(mustTokenize(t, "SELECT age FROM t WHERE age > 30"))
	if err == nil {
		t.Fatal("WHERE should error in Step 3")
	}
	if !strings.Contains(err.Error(), "WHERE") {
		t.Errorf("error should mention WHERE, got %q", err.Error())
	}
}

func TestParseGroupByNotImplemented(t *testing.T) {
	_, err := Parse(mustTokenize(t, "SELECT city FROM t GROUP BY city"))
	if err == nil {
		t.Fatal("GROUP BY should error in Step 3")
	}
	if !strings.Contains(err.Error(), "GROUP BY") {
		t.Errorf("error should mention GROUP BY, got %q", err.Error())
	}
}

func TestParseErrorHasOffset(t *testing.T) {
	_, err := Parse(mustTokenize(t, "SELECT FROM"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("error should include offset, got %q", err.Error())
	}
}

func TestParseCaseInsensitive(t *testing.T) {
	stmt, err := Parse(mustTokenize(t, "select age from T"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt.Items) != 1 || stmt.Items[0].Column != "age" {
		t.Fatalf("items = %+v", stmt.Items)
	}
}
