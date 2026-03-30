package storage

import (
	"fmt"
)

// RowGroup is a collection of column chunks that share the same row count.
//
// Real columnar databases don't store billions of rows in one giant column.
// They split data into row groups (e.g., 65536 rows each). Each row group
// is independently readable, enabling:
//   - Parallel reads: different goroutines read different row groups
//   - Skip scanning: check row group stats → skip entire groups that don't match
//   - Memory management: process one row group at a time, not the whole table
//
// A row group is conceptually a "horizontal slice" of a table:
//
//	                col_A    col_B    col_C
//	Row Group 0:   [...]    [...]    [...]     ← 65536 rows
//	Row Group 1:   [...]    [...]    [...]     ← 65536 rows
//	Row Group 2:   [...]    [...]    [...]     ← last group (maybe fewer rows)
type RowGroup struct {
	Columns  []*ColumnChunk
	RowCount int
}

// Schema describes the structure of a row group: column names and types.
type Schema struct {
	Names []string
	Types []ColumnType
}

// NewRowGroup creates a row group from column chunks.
// All chunks must have the same row count.
func NewRowGroup(columns ...*ColumnChunk) (*RowGroup, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("row group must have at least one column")
	}

	rowCount := columns[0].Count
	for i, col := range columns[1:] {
		if col.Count != rowCount {
			return nil, fmt.Errorf(
				"column %d (%s) has %d rows, expected %d (matching column 0)",
				i+1, col.Name, col.Count, rowCount,
			)
		}
	}

	return &RowGroup{
		Columns:  columns,
		RowCount: rowCount,
	}, nil
}

// NumColumns returns the number of columns in this row group.
func (rg *RowGroup) NumColumns() int {
	return len(rg.Columns)
}

// Column returns the chunk for the given column index.
func (rg *RowGroup) Column(i int) *ColumnChunk {
	return rg.Columns[i]
}

// ColumnByName returns the chunk with the given name, or nil if not found.
func (rg *RowGroup) ColumnByName(name string) *ColumnChunk {
	for _, col := range rg.Columns {
		if col.Name == name {
			return col
		}
	}
	return nil
}

// Schema returns the schema (column names and types) of this row group.
func (rg *RowGroup) Schema() Schema {
	names := make([]string, len(rg.Columns))
	types := make([]ColumnType, len(rg.Columns))
	for i, col := range rg.Columns {
		names[i] = col.Name
		types[i] = col.ColType
	}
	return Schema{Names: names, Types: types}
}

// TotalNulls returns the total number of null values across all columns.
func (rg *RowGroup) TotalNulls() int {
	total := 0
	for _, col := range rg.Columns {
		total += col.NullCount
	}
	return total
}
