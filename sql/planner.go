package sql

import (
	"fmt"

	"github.com/iyunbo/columnar-db/exec"
	"github.com/iyunbo/columnar-db/storage"
)

// Plan translates a parsed SelectStmt into an exec.Operator tree
// that can be drained via Next()/Reset(). The RowGroup's schema is
// used to validate column names and resolve column indexes.
//
// Phase 5 Step 3 scope: SELECT { * | col_list } FROM t →
// ScanOp (with only the requested columns projected). WHERE and
// GROUP BY are stubs returning errors; Steps 4 and 5 fill them in.
func Plan(rg *storage.RowGroup, stmt *SelectStmt) (exec.Operator, error) {
	schema := rg.Schema()

	// Validate that the select list only uses columns that exist.
	// Collect the column names the ScanOp needs to read.
	scanCols, err := resolveScanColumns(schema, stmt)
	if err != nil {
		return nil, err
	}

	scan, err := exec.NewScanOp(rg, scanCols)
	if err != nil {
		return nil, fmt.Errorf("sql: planner: %w", err)
	}

	// For Step 3, no WHERE and no GROUP BY — just return ScanOp
	// directly. The select-list order is already the scan order
	// because resolveScanColumns returns columns in select-list
	// order. No ProjectOp needed unless Step 5 adds aggregates
	// that reorder the output.

	return scan, nil
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
