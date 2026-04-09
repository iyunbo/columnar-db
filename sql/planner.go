package sql

import (
	"fmt"
	"strconv"

	"github.com/iyunbo/columnar-db/exec"
	"github.com/iyunbo/columnar-db/storage"
)

// Plan translates a parsed SelectStmt into an exec.Operator tree
// that can be drained via Next()/Reset(). The RowGroup's schema is
// used to validate column names and resolve column indexes.
func Plan(rg *storage.RowGroup, stmt *SelectStmt) (exec.Operator, error) {
	schema := rg.Schema()

	// Collect the column names the ScanOp needs to read.
	scanCols, err := resolveScanColumns(schema, stmt)
	if err != nil {
		return nil, err
	}

	// If WHERE references a column not in the select list, the scan
	// must include it so FilterOp can read it. We track whether we
	// added an extra column so we can drop it via ProjectOp after
	// filtering.
	extraWhereCol := false
	whereColIdx := -1
	if stmt.Where != nil {
		if !schemaHasColumn(schema, stmt.Where.Column) {
			return nil, fmt.Errorf("sql: planner: WHERE references unknown column %q", stmt.Where.Column)
		}
		idx := indexOf(scanCols, stmt.Where.Column)
		if idx < 0 {
			scanCols = append(scanCols, stmt.Where.Column)
			whereColIdx = len(scanCols) - 1
			extraWhereCol = true
		} else {
			whereColIdx = idx
		}
	}

	scan, err := exec.NewScanOp(rg, scanCols)
	if err != nil {
		return nil, fmt.Errorf("sql: planner: %w", err)
	}

	var op exec.Operator = scan

	// WHERE → FilterOp.
	if stmt.Where != nil {
		colType := schemaColumnType(schema, stmt.Where.Column)
		pred, err := buildPredicate(colType, stmt.Where)
		if err != nil {
			return nil, err
		}
		filter, err := exec.NewFilterOp(op, whereColIdx, pred)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: %w", err)
		}
		op = filter
	}

	// If we added an extra column for WHERE, drop it via ProjectOp
	// so the output matches the original select list.
	if extraWhereCol {
		projCols := make([]int, len(scanCols)-1)
		for i := range projCols {
			projCols[i] = i
		}
		proj, err := exec.NewProjectOp(op, projCols)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: %w", err)
		}
		op = proj
	}

	return op, nil
}

// buildPredicate creates an exec.Predicate from the parsed WHERE
// clause. Coercion rules (from docs/phase5-steps.md):
//
//   - Int literal + Int64 column → Int64 predicate
//   - String literal + String column → StringEq (= only)
//   - Other combinations → planner error
func buildPredicate(colType storage.ColumnType, w *Predicate) (exec.Predicate, error) {
	switch colType {
	case storage.TypeInt64:
		v, err := strconv.ParseInt(w.Literal.Value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: cannot parse %q as int64 for column %q: %w", w.Literal.Value, w.Column, err)
		}
		switch w.Op {
		case TokEq:
			return exec.Int64Eq{Value: v}, nil
		case TokNe:
			return exec.Int64Ne{Value: v}, nil
		case TokLt:
			return exec.Int64Lt{Value: v}, nil
		case TokLe:
			return exec.Int64Le{Value: v}, nil
		case TokGt:
			return exec.Int64Gt{Value: v}, nil
		case TokGe:
			return exec.Int64Ge{Value: v}, nil
		default:
			return nil, fmt.Errorf("sql: planner: unsupported operator %s for int64 column %q", w.Op, w.Column)
		}
	case storage.TypeString:
		if w.Literal.Kind != TokString {
			return nil, fmt.Errorf("sql: planner: WHERE on string column %q requires a string literal, got %s", w.Column, w.Literal.Kind)
		}
		if w.Op != TokEq {
			return nil, fmt.Errorf("sql: planner: string column %q only supports = comparison (no %s)", w.Column, w.Op)
		}
		return exec.StringEq{Value: w.Literal.Value}, nil
	case storage.TypeFloat64:
		return nil, fmt.Errorf("sql: planner: WHERE on float64 column %q not supported yet (no Float64 predicates)", w.Column)
	}
	return nil, fmt.Errorf("sql: planner: unsupported column type %s for WHERE", colType)
}

// resolveScanColumns maps the select list to the RowGroup's schema,
// returning the column names in select-list order. SELECT * expands
// to all columns in schema order.
func resolveScanColumns(schema storage.Schema, stmt *SelectStmt) ([]string, error) {
	if len(stmt.Items) == 1 && stmt.Items[0].Star {
		return schema.Names, nil
	}

	cols := make([]string, 0, len(stmt.Items))
	for _, item := range stmt.Items {
		if item.Star {
			return nil, fmt.Errorf("sql: planner: * must be the only select item when used")
		}
		if item.AggFunc != "" {
			return nil, fmt.Errorf("sql: planner: aggregate functions not implemented yet")
		}
		name := item.Column
		if !schemaHasColumn(schema, name) {
			return nil, fmt.Errorf("sql: planner: unknown column %q", name)
		}
		cols = append(cols, name)
	}
	return cols, nil
}

func schemaHasColumn(schema storage.Schema, name string) bool {
	for _, n := range schema.Names {
		if n == name {
			return true
		}
	}
	return false
}

func schemaColumnType(schema storage.Schema, name string) storage.ColumnType {
	for i, n := range schema.Names {
		if n == name {
			return schema.Types[i]
		}
	}
	return 0 // unreachable if caller already validated
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}
