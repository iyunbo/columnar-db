// Package exec implements the vectorized execution engine.
//
// Vectorized execution processes data in batches of ~1024 values from one
// column at a time, in tight loops over typed arrays. This is the opposite
// of the row-at-a-time Volcano model: it's cache-friendly, branch-predictor
// friendly, and maps naturally onto SIMD — and it's what makes modern
// columnar engines (DuckDB, ClickHouse, Vectorwise) fast.
package exec

import (
	"fmt"

	"github.com/iyunbo/columnar-db/storage"
)

// VectorSize is the maximum number of values in a single Vector.
//
// Why 1024?
//   - Large enough to amortize per-call operator overhead
//   - Small enough that a batch of typed values fits comfortably in L1/L2
//   - Matches the "magic number" used by DuckDB and Vectorwise
const VectorSize = 1024

// Vector is a typed batch of up to VectorSize values from a single column.
//
// A Vector is the unit of data that flows through the execution engine:
// Scan produces Vectors, Filter narrows them (via a Selection), Project
// drops them, and aggregation consumes them. Nothing in the engine ever
// materializes a full row until the very end — values live column-wise
// in these batches from disk to final output.
type Vector struct {
	// Type of every value in this vector.
	Type storage.ColumnType

	// Values holds the actual data as a typed column (reuses Phase 1
	// storage.Column implementations). Capacity is always VectorSize.
	Values storage.Column

	// Nulls is a bitmap of length VectorSize. Bit i is set iff row i is NULL.
	// Always non-nil, even when there are no nulls, so the filter hot path
	// can do a branchless check.
	Nulls *storage.NullBitmap

	// Length is the number of live values in the vector. May be less than
	// VectorSize for the last batch of a row group.
	Length int
}

// NewVector creates an empty vector for the given type, pre-sized to
// VectorSize. Length starts at 0 — call the type-specific Append* helpers
// (or Scan, later) to fill it.
func NewVector(t storage.ColumnType) *Vector {
	return &Vector{
		Type:   t,
		Values: newColumnForType(t),
		Nulls:  storage.NewNullBitmap(VectorSize),
		Length: 0,
	}
}

// Len returns the number of live values in the vector.
func (v *Vector) Len() int { return v.Length }

// IsNull reports whether row i in the vector is NULL.
// Panics if i is out of range.
func (v *Vector) IsNull(i int) bool {
	if i < 0 || i >= v.Length {
		panic(fmt.Sprintf("exec: Vector.IsNull index %d out of range [0,%d)", i, v.Length))
	}
	return v.Nulls.IsNull(i)
}

// Reset clears the vector for reuse without reallocating. The underlying
// typed column is replaced with a fresh empty one (cheap — just a slice
// header reset) and the null bitmap is zeroed.
//
// Operators reuse Vectors across batches to keep allocations out of the
// hot path.
func (v *Vector) Reset() {
	v.Values = newColumnForType(v.Type)
	v.Nulls = storage.NewNullBitmap(VectorSize)
	v.Length = 0
}

// AppendInt64 appends an Int64 value to the vector. Returns an error if
// the vector type is not Int64 or the vector is already full.
func (v *Vector) AppendInt64(x int64) error {
	if v.Type != storage.TypeInt64 {
		return fmt.Errorf("exec: AppendInt64 on %s vector", v.Type)
	}
	if v.Length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.Int64Column).Append(x)
	v.Length++
	return nil
}

// AppendFloat64 appends a Float64 value.
func (v *Vector) AppendFloat64(x float64) error {
	if v.Type != storage.TypeFloat64 {
		return fmt.Errorf("exec: AppendFloat64 on %s vector", v.Type)
	}
	if v.Length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.Float64Column).Append(x)
	v.Length++
	return nil
}

// AppendString appends a String value.
func (v *Vector) AppendString(s string) error {
	if v.Type != storage.TypeString {
		return fmt.Errorf("exec: AppendString on %s vector", v.Type)
	}
	if v.Length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.StringColumn).Append(s)
	v.Length++
	return nil
}

// AppendBool appends a Bool value.
func (v *Vector) AppendBool(b bool) error {
	if v.Type != storage.TypeBool {
		return fmt.Errorf("exec: AppendBool on %s vector", v.Type)
	}
	if v.Length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.BoolColumn).Append(b)
	v.Length++
	return nil
}

// AppendNull appends a NULL placeholder. The caller must still append a
// dummy typed value of the vector's type so that Values and Nulls stay
// aligned on row indices — callers should prefer the convenience helpers
// below which do both at once.
func (v *Vector) AppendNull() error {
	if v.Length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	// Store a zero-value placeholder so Values.Len() stays in sync with Length.
	switch v.Type {
	case storage.TypeInt64:
		v.Values.(*storage.Int64Column).Append(0)
	case storage.TypeFloat64:
		v.Values.(*storage.Float64Column).Append(0)
	case storage.TypeString:
		v.Values.(*storage.StringColumn).Append("")
	case storage.TypeBool:
		v.Values.(*storage.BoolColumn).Append(false)
	default:
		return fmt.Errorf("exec: unknown vector type %s", v.Type)
	}
	v.Nulls.SetNull(v.Length)
	v.Length++
	return nil
}

// newColumnForType constructs an empty typed column for use as a Vector's
// backing storage.
func newColumnForType(t storage.ColumnType) storage.Column {
	switch t {
	case storage.TypeInt64:
		return storage.NewInt64Column()
	case storage.TypeFloat64:
		return storage.NewFloat64Column()
	case storage.TypeString:
		return storage.NewStringColumn()
	case storage.TypeBool:
		return storage.NewBoolColumn()
	default:
		panic(fmt.Sprintf("exec: unknown column type %s", t))
	}
}
