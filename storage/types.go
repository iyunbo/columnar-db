// Package storage implements column-oriented data storage.
//
// This is the foundation: typed column arrays that store values
// contiguously in memory — the key insight behind columnar databases.
//
// Why columnar? When you scan one field across millions of rows,
// you want those values packed together in memory for:
//   - CPU cache efficiency (sequential access)
//   - SIMD-friendly processing
//   - Better compression (similar values together)
package storage

// ColumnType identifies the data type of a column.
type ColumnType int

const (
	TypeInt64   ColumnType = iota // 64-bit signed integer
	TypeFloat64                  // 64-bit floating point
	TypeString                   // Variable-length UTF-8 string
	TypeBool                     // Boolean (true/false)
)

// String returns the human-readable name of a column type.
func (t ColumnType) String() string {
	switch t {
	case TypeInt64:
		return "Int64"
	case TypeFloat64:
		return "Float64"
	case TypeString:
		return "String"
	case TypeBool:
		return "Bool"
	default:
		return "Unknown"
	}
}

// Column is the interface for a typed column of values.
//
// Each implementation stores values as a contiguous typed slice,
// not as []interface{} — this avoids boxing overhead and keeps
// data cache-friendly.
type Column interface {
	// Type returns the column's data type.
	Type() ColumnType

	// Len returns the number of values in the column.
	Len() int
}
