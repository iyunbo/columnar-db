package sql

import (
	"strings"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 5 Step 1: skeleton-only tests. Real functional coverage
// lands as Step 2 fills in the lexer, Step 3 the parser/planner,
// and so on. These tests pin the package's current shape so a
// downstream step cannot accidentally break the stub contract.

func TestTokenKindString(t *testing.T) {
	// Spot-check a few String() returns. Exhaustive coverage is
	// overkill for a Step 1 skeleton; Step 2 will add its own
	// lexer-level tests that incidentally exercise these.
	cases := map[TokenKind]string{
		TokSelect: "SELECT",
		TokFrom:   "FROM",
		TokEq:     "=",
		TokNe:     "!=",
		TokLe:     "<=",
		TokEOF:    "EOF",
		TokIdent:  "IDENT",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("TokenKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestTokenizeStubReturnsError(t *testing.T) {
	// Step 1: Tokenize is a stub. Downstream steps replace it;
	// until then it must return an error so no caller silently
	// uses an empty token stream.
	toks, err := Tokenize("SELECT 1")
	if err == nil {
		t.Fatal("Tokenize stub should return an error in Step 1")
	}
	if toks != nil {
		t.Errorf("Tokenize stub should return nil tokens, got %v", toks)
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("stub error should mention 'not implemented', got %q", err.Error())
	}
}

func TestExecuteRejectsNilRowGroup(t *testing.T) {
	_, err := Execute(nil, "SELECT 1")
	if err == nil {
		t.Fatal("Execute(nil, ...) should error")
	}
}

func TestExecuteRejectsEmptyQuery(t *testing.T) {
	rg := makeTinyRowGroup(t)
	_, err := Execute(rg, "")
	if err == nil {
		t.Fatal("Execute with empty query should error")
	}
}

func TestExecuteStubReturnsError(t *testing.T) {
	// Even with a valid RowGroup + non-empty query, Execute is
	// a Step 1 stub that must surface its not-implemented state
	// clearly rather than return a working but empty stream.
	rg := makeTinyRowGroup(t)
	_, err := Execute(rg, "SELECT 1")
	if err == nil {
		t.Fatal("Execute stub should return an error in Step 1")
	}
}

// makeTinyRowGroup builds a 3-row single-column fixture. Only
// used for skeleton tests that need a non-nil RowGroup.
func makeTinyRowGroup(t *testing.T) *storage.RowGroup {
	t.Helper()
	col := storage.NewColumnChunkNoNulls(
		"age",
		storage.NewInt64ColumnFromSlice([]int64{10, 20, 30}),
	)
	rg, err := storage.NewRowGroup(col)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}
