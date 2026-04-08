package sql

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

func TestTokenKindString(t *testing.T) {
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

func TestExecuteRejectsNilRowGroup(t *testing.T) {
	if _, err := Execute(nil, "SELECT 1"); err == nil {
		t.Fatal("Execute(nil, ...) should error")
	}
}

func TestExecuteRejectsEmptyQuery(t *testing.T) {
	rg := makeTinyRowGroup(t)
	if _, err := Execute(rg, ""); err == nil {
		t.Fatal("Execute with empty query should error")
	}
}

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
