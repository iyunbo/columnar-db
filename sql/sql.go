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
// FROM clause's identifier is validated against a caller-chosen
// table name (to be decided in Step 3). No multi-table catalog,
// no JOINs.
//
// Step 1: this entry point returns an error for every input
// because the lexer/parser/planner are stubs. Step 3 is the first
// step that makes the happy path work.
//
// Design note: Execute returns an Operator, not a []*Batch, so
// callers can stream results and reuse the operator via Reset().
// It mirrors the shape every Phase 2–4 operator already uses.
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
	_ = tokens
	// Step 3 wires up: ast, err := Parse(tokens); plan.Build(rg, ast).
	return nil, fmt.Errorf("sql: Execute is not implemented yet (Phase 5 Step 3)")
}
