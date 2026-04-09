package sql

import (
	"fmt"

	"github.com/iyunbo/columnar-db/exec"
	"github.com/iyunbo/columnar-db/storage"
)

// Execute parses the SQL query, plans it against the supplied
// RowGroup, and returns an Operator whose Next()/Reset() drain
// yields the result batches.
//
// Phase 5 scope: a single in-memory RowGroup is the table. The
// FROM clause's identifier is ignored; multi-table catalog
// support is filed for Phase 6+.
//
// Execute returns an exec.Operator (not []*Batch) so callers can
// stream results and reuse via Reset(), matching every other
// layer's contract.
func Execute(rg *storage.RowGroup, query string) (exec.Operator, error) {
	if rg == nil {
		return nil, fmt.Errorf("sql: Execute requires a non-nil RowGroup")
	}
	if query == "" {
		return nil, fmt.Errorf("sql: Execute requires a non-empty query")
	}
	tokens, err := Tokenize(query)
	if err != nil {
		return nil, err
	}
	stmt, err := Parse(tokens)
	if err != nil {
		return nil, err
	}
	return Plan(rg, stmt)
}
