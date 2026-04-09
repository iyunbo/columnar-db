package sql

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/iyunbo/columnar-db/exec"
	"github.com/iyunbo/columnar-db/storage"
)

// Plan translates a parsed SelectStmt into an exec.Operator tree
// that can be drained via Next()/Reset(). The RowGroup's schema is
// used to validate column names and resolve column indexes.
func Plan(rg *storage.RowGroup, stmt *SelectStmt) (exec.Operator, error) {
	schema := rg.Schema()

	// Classify: does the SELECT list contain aggregates?
	hasAgg := false
	for _, item := range stmt.Items {
		if item.AggFunc != "" {
			hasAgg = true
			break
		}
	}

	if hasAgg {
		return planAggregation(rg, schema, stmt)
	}
	return planSimpleSelect(rg, schema, stmt)
}

// planSimpleSelect handles SELECT (with optional WHERE, no aggregation).
func planSimpleSelect(rg *storage.RowGroup, schema storage.Schema, stmt *SelectStmt) (exec.Operator, error) {
	scanCols, err := resolvePlainScanColumns(schema, stmt)
	if err != nil {
		return nil, err
	}

	scanCols, whereColIdx, extraWhereCol, whereColType, err := expandWhereColumn(schema, scanCols, stmt)
	if err != nil {
		return nil, err
	}

	scan, err := exec.NewScanOp(rg, scanCols)
	if err != nil {
		return nil, fmt.Errorf("sql: planner: %w", err)
	}

	op, err := applyFilter(scan, stmt, whereColIdx, whereColType)
	if err != nil {
		return nil, err
	}

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

// planAggregation handles SELECT with aggregate functions (with or
// without GROUP BY).
func planAggregation(rg *storage.RowGroup, schema storage.Schema, stmt *SelectStmt) (exec.Operator, error) {
	groupKeys := stmt.GroupBy

	// Validate: SELECT * mixed with aggregates is rejected.
	for _, item := range stmt.Items {
		if item.Star {
			return nil, fmt.Errorf("sql: planner: SELECT * cannot be mixed with aggregate functions")
		}
	}

	// Validate: plain columns must be in GROUP BY.
	for _, item := range stmt.Items {
		if item.Column != "" {
			if len(groupKeys) == 0 {
				return nil, fmt.Errorf("sql: planner: column %q must appear in GROUP BY or be inside an aggregate function", item.Column)
			}
			if slices.Index(groupKeys, item.Column) < 0 {
				return nil, fmt.Errorf("sql: planner: column %q is not in GROUP BY clause", item.Column)
			}
		}
	}

	// Validate GROUP BY columns exist.
	for _, k := range groupKeys {
		if _, ok := schemaLookup(schema, k); !ok {
			return nil, fmt.Errorf("sql: planner: GROUP BY references unknown column %q", k)
		}
	}

	// Collect all columns the scan needs (deduped): GROUP BY keys +
	// aggregate input columns.
	scanCols := make([]string, 0, len(groupKeys)+len(stmt.Items))
	addUnique := func(name string) {
		if slices.Index(scanCols, name) < 0 {
			scanCols = append(scanCols, name)
		}
	}
	for _, k := range groupKeys {
		addUnique(k)
	}
	for _, item := range stmt.Items {
		if item.AggFunc != "" && item.AggArg != "*" {
			if _, ok := schemaLookup(schema, item.AggArg); !ok {
				return nil, fmt.Errorf("sql: planner: aggregate %s references unknown column %q", item.AggFunc, item.AggArg)
			}
			addUnique(item.AggArg)
		}
	}

	// ScanOp requires at least one column. If only COUNT(*) is used
	// (no key columns, no column-based aggregates), scan the first
	// schema column to get the row count.
	if len(scanCols) == 0 {
		scanCols = append(scanCols, schema.Names[0])
	}

	// WHERE column expansion.
	scanCols, whereColIdx, _, whereColType, err := expandWhereColumn(schema, scanCols, stmt)
	if err != nil {
		return nil, err
	}

	scan, err := exec.NewScanOp(rg, scanCols)
	if err != nil {
		return nil, fmt.Errorf("sql: planner: %w", err)
	}

	op, err := applyFilter(scan, stmt, whereColIdx, whereColType)
	if err != nil {
		return nil, err
	}

	// Build aggregate specs. ColIndex = position in scanCols.
	// Column type was already validated during scan-col collection
	// above, so schemaLookup here is guaranteed to succeed.
	specs := make([]exec.AggregateSpec, 0, len(stmt.Items))
	for _, item := range stmt.Items {
		if item.AggFunc == "" {
			continue
		}
		var colType storage.ColumnType
		colIdx := 0
		if item.AggArg != "*" {
			colType, _ = schemaLookup(schema, item.AggArg)
			colIdx = slices.Index(scanCols, item.AggArg)
		}
		agg, err := buildAggregator(item.AggFunc, colType, item.AggArg)
		if err != nil {
			return nil, err
		}
		specs = append(specs, exec.AggregateSpec{
			Name:     item.AggFunc,
			ColIndex: colIdx,
			Agg:      agg,
		})
	}

	if len(groupKeys) == 0 {
		aggOp, err := exec.NewAggregateOp(op, specs)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: %w", err)
		}
		return aggOp, nil
	}

	// Grouped aggregation.
	keyIdxs := make([]int, len(groupKeys))
	keyTypes := make([]storage.ColumnType, len(groupKeys))
	for i, k := range groupKeys {
		keyIdxs[i] = slices.Index(scanCols, k)
		keyTypes[i], _ = schemaLookup(schema, k)
	}

	var groupOp exec.Operator
	if len(groupKeys) == 1 {
		g, err := exec.NewGroupByOp(op, keyIdxs[0], keyTypes[0], specs)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: %w", err)
		}
		groupOp = g
	} else {
		g, err := exec.NewGroupByOpMulti(op, keyIdxs, keyTypes, specs)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: %w", err)
		}
		groupOp = g
	}

	// GroupByOp output: [key0..keyN, agg0..aggN]. If the SELECT
	// list interleaves keys and aggs differently, reorder via
	// ProjectOp.
	projMap := buildOutputProjection(stmt.Items, groupKeys)
	isIdentity := true
	for i, p := range projMap {
		if p != i {
			isIdentity = false
			break
		}
	}
	if !isIdentity {
		proj, err := exec.NewProjectOp(groupOp, projMap)
		if err != nil {
			return nil, fmt.Errorf("sql: planner: %w", err)
		}
		return proj, nil
	}
	return groupOp, nil
}

// buildOutputProjection maps SELECT-list positions to GroupByOp
// output positions ([keys..., aggs...]).
func buildOutputProjection(items []SelectItem, groupKeys []string) []int {
	projMap := make([]int, len(items))
	aggIdx := 0
	for i, item := range items {
		if item.AggFunc != "" {
			projMap[i] = len(groupKeys) + aggIdx
			aggIdx++
		} else {
			projMap[i] = slices.Index(groupKeys, item.Column)
		}
	}
	return projMap
}

// buildAggregator creates the right exec.Aggregator for the given
// aggregate function name (canonical uppercase from parser, e.g.
// "COUNT") and the pre-resolved column type. The caller has already
// validated the column exists and looked up its type — no redundant
// schemaLookup here.
func buildAggregator(funcName string, colType storage.ColumnType, colArg string) (exec.Aggregator, error) {
	if funcName == "COUNT" && colArg == "*" {
		return &exec.CountStar{}, nil
	}

	switch funcName {
	case "COUNT":
		// TODO: COUNT(col) should skip NULLs per SQL standard once
		// nullable columns are queryable. Currently maps to CountStar
		// which counts all rows — identical result when no nulls exist.
		return &exec.CountStar{}, nil
	case "SUM":
		switch colType {
		case storage.TypeInt64:
			return &exec.Int64Sum{}, nil
		case storage.TypeFloat64:
			return &exec.Float64Sum{}, nil
		default:
			return nil, fmt.Errorf("sql: planner: SUM not supported on %s column %q", colType, colArg)
		}
	case "MIN":
		switch colType {
		case storage.TypeInt64:
			return &exec.Int64Min{}, nil
		case storage.TypeFloat64:
			return &exec.Float64Min{}, nil
		default:
			return nil, fmt.Errorf("sql: planner: MIN not supported on %s column %q", colType, colArg)
		}
	case "MAX":
		switch colType {
		case storage.TypeInt64:
			return &exec.Int64Max{}, nil
		case storage.TypeFloat64:
			return &exec.Float64Max{}, nil
		default:
			return nil, fmt.Errorf("sql: planner: MAX not supported on %s column %q", colType, colArg)
		}
	case "AVG":
		switch colType {
		case storage.TypeInt64:
			return &exec.Int64Avg{}, nil
		case storage.TypeFloat64:
			return &exec.Float64Avg{}, nil
		default:
			return nil, fmt.Errorf("sql: planner: AVG not supported on %s column %q", colType, colArg)
		}
	}
	return nil, fmt.Errorf("sql: planner: unknown aggregate function %q", funcName)
}

// ---------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------

// expandWhereColumn appends the WHERE column to scanCols if it's
// not already present, returning the updated slice, the WHERE
// column's index, whether an extra column was added, and the
// column's type.
func expandWhereColumn(schema storage.Schema, scanCols []string, stmt *SelectStmt) ([]string, int, bool, storage.ColumnType, error) {
	if stmt.Where == nil {
		return scanCols, -1, false, 0, nil
	}
	colType, ok := schemaLookup(schema, stmt.Where.Column)
	if !ok {
		return nil, 0, false, 0, fmt.Errorf("sql: planner: WHERE references unknown column %q", stmt.Where.Column)
	}
	idx := slices.Index(scanCols, stmt.Where.Column)
	if idx < 0 {
		scanCols = append(scanCols, stmt.Where.Column)
		return scanCols, len(scanCols) - 1, true, colType, nil
	}
	return scanCols, idx, false, colType, nil
}

// applyFilter wraps op with a FilterOp if the statement has a WHERE.
func applyFilter(op exec.Operator, stmt *SelectStmt, whereColIdx int, whereColType storage.ColumnType) (exec.Operator, error) {
	if stmt.Where == nil {
		return op, nil
	}
	pred, err := buildPredicate(whereColType, stmt.Where)
	if err != nil {
		return nil, err
	}
	filter, err := exec.NewFilterOp(op, whereColIdx, pred)
	if err != nil {
		return nil, fmt.Errorf("sql: planner: %w", err)
	}
	return filter, nil
}

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

// resolvePlainScanColumns maps a non-aggregate select list to
// column names. SELECT * expands to all schema columns.
func resolvePlainScanColumns(schema storage.Schema, stmt *SelectStmt) ([]string, error) {
	if len(stmt.Items) == 1 && stmt.Items[0].Star {
		return schema.Names, nil
	}

	cols := make([]string, 0, len(stmt.Items))
	for _, item := range stmt.Items {
		if item.Star {
			return nil, fmt.Errorf("sql: planner: * must be the only select item when used")
		}
		if item.AggFunc != "" {
			return nil, fmt.Errorf("sql: planner: unexpected aggregate in non-aggregate query")
		}
		if _, ok := schemaLookup(schema, item.Column); !ok {
			return nil, fmt.Errorf("sql: planner: unknown column %q", item.Column)
		}
		cols = append(cols, item.Column)
	}
	return cols, nil
}

func schemaLookup(schema storage.Schema, name string) (storage.ColumnType, bool) {
	for i, n := range schema.Names {
		if n == name {
			return schema.Types[i], true
		}
	}
	return 0, false
}
