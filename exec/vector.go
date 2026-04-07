// Package exec implements the vectorized execution engine.
//
// Vectorized execution processes data in batches of ~1024 values from one
// column at a time, in tight loops over typed arrays. This is the opposite
// of the row-at-a-time Volcano model: it's cache-friendly, branch-predictor
// friendly, and maps naturally onto SIMD — and it's what makes modern
// columnar engines (DuckDB, ClickHouse, Vectorwise) fast.
//
// Error conventions in this package:
//   - Out-of-range index access panics (matches Go slice semantics).
//   - API misuse that a caller can meaningfully recover from (wrong type,
//     vector full) returns an error.
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
//
// Layout invariant: Values.Len() == length at all times, even when nulls
// are appended (nulls store a zero-value placeholder in Values so that
// row index i maps to the same position in both Values and Nulls). This
// keeps the filter hot path able to index the typed slice directly
// without consulting the null bitmap.
type Vector struct {
	// Type of every value in this vector.
	Type storage.ColumnType

	// Values holds the actual data as a typed column (reuses Phase 1
	// storage.Column implementations). Backing slice is pre-allocated
	// to VectorSize at construction to avoid grow-and-copy during fill.
	//
	// Prefer the typed accessors (Int64s / Float64s / Strings / Bools)
	// over type-asserting this field directly — they do the assertion
	// once and return a raw slice that Filter can iterate in a tight
	// loop with no interface overhead.
	Values storage.Column

	// Nulls is a bitmap of length VectorSize. Bit i is set iff row i is NULL.
	// Always non-nil, even when there are no nulls, so the filter hot path
	// can do a branchless check.
	Nulls *storage.NullBitmap

	// length is the number of live values in the vector. Unexported so
	// callers cannot desync it from Values and Nulls — use Len() to read,
	// Append* / AppendNull / Reset to mutate.
	length int
}

// NewVector creates an empty vector for the given type with backing
// storage pre-allocated to VectorSize. Length starts at 0 — call the
// type-specific Append helpers (or Scan, later) to fill it.
func NewVector(t storage.ColumnType) *Vector {
	return &Vector{
		Type:   t,
		Values: newSizedColumnForType(t, VectorSize),
		Nulls:  storage.NewNullBitmap(VectorSize),
		length: 0,
	}
}

// Len returns the number of live values in the vector.
func (v *Vector) Len() int { return v.length }

// IsNull reports whether row i in the vector is NULL.
// Panics if i is out of range.
func (v *Vector) IsNull(i int) bool {
	if i < 0 || i >= v.length {
		panic(fmt.Sprintf("exec: Vector.IsNull index %d out of range [0,%d)", i, v.length))
	}
	return v.Nulls.IsNull(i)
}

// Reset clears the vector for reuse without allocating. The backing
// typed slice is truncated in place and the null bitmap is zeroed —
// operators can recycle a single Vector across every batch of a scan.
func (v *Vector) Reset() {
	switch c := v.Values.(type) {
	case *storage.Int64Column:
		c.Reset()
	case *storage.Float64Column:
		c.Reset()
	case *storage.StringColumn:
		c.Reset()
	case *storage.BoolColumn:
		c.Reset()
	default:
		panic(fmt.Sprintf("exec: Reset on unknown column type %s", v.Type))
	}
	v.Nulls.Reset()
	v.length = 0
}

// Int64s returns the underlying []int64 backing an Int64 vector. Panics
// if the vector is not of type Int64. The returned slice has length
// equal to v.Len() — safe to iterate with a tight for-range loop in the
// filter hot path.
func (v *Vector) Int64s() []int64 {
	return v.Values.(*storage.Int64Column).Values()[:v.length]
}

// Float64s returns the underlying []float64 backing a Float64 vector.
func (v *Vector) Float64s() []float64 {
	return v.Values.(*storage.Float64Column).Values()[:v.length]
}

// Strings returns the underlying []string backing a String vector.
func (v *Vector) Strings() []string {
	return v.Values.(*storage.StringColumn).Values()[:v.length]
}

// Bools returns the underlying []bool backing a Bool vector.
func (v *Vector) Bools() []bool {
	return v.Values.(*storage.BoolColumn).Values()[:v.length]
}

// AppendInt64 appends an Int64 value to the vector. Returns an error if
// the vector type is not Int64 or the vector is already full.
func (v *Vector) AppendInt64(x int64) error {
	if v.Type != storage.TypeInt64 {
		return fmt.Errorf("exec: AppendInt64 on %s vector", v.Type)
	}
	if v.length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.Int64Column).Append(x)
	v.length++
	return nil
}

// AppendFloat64 appends a Float64 value.
func (v *Vector) AppendFloat64(x float64) error {
	if v.Type != storage.TypeFloat64 {
		return fmt.Errorf("exec: AppendFloat64 on %s vector", v.Type)
	}
	if v.length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.Float64Column).Append(x)
	v.length++
	return nil
}

// AppendString appends a String value.
func (v *Vector) AppendString(s string) error {
	if v.Type != storage.TypeString {
		return fmt.Errorf("exec: AppendString on %s vector", v.Type)
	}
	if v.length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.StringColumn).Append(s)
	v.length++
	return nil
}

// AppendBool appends a Bool value.
func (v *Vector) AppendBool(b bool) error {
	if v.Type != storage.TypeBool {
		return fmt.Errorf("exec: AppendBool on %s vector", v.Type)
	}
	if v.length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
	v.Values.(*storage.BoolColumn).Append(b)
	v.length++
	return nil
}

// CopyFromChunk bulk-copies rows [start, start+n) from a source ColumnChunk
// into this (empty, just-Reset) vector. The vector type must match the
// chunk type and n must be ≤ VectorSize. This is the hot path used by the
// Scan operator: one typed memmove per column per batch, no per-row interface
// calls.
//
// Semantics: writes are absolute, not appending. length is set to n (not
// incremented), so calling this on a non-empty vector will desync length
// from the underlying Values slice. Always Reset() first — ScanOp does this
// via Batch.Reset() at the top of every Next().
func (v *Vector) CopyFromChunk(chunk *storage.ColumnChunk, start, n int) error {
	if v.Type != chunk.ColType {
		return fmt.Errorf("exec: CopyFromChunk type mismatch: vector %s, chunk %s", v.Type, chunk.ColType)
	}
	if n > VectorSize {
		return fmt.Errorf("exec: CopyFromChunk n=%d exceeds VectorSize=%d", n, VectorSize)
	}
	if start < 0 || start+n > chunk.Count {
		return fmt.Errorf("exec: CopyFromChunk range [%d,%d) out of chunk count %d", start, start+n, chunk.Count)
	}

	end := start + n
	switch v.Type {
	case storage.TypeInt64:
		src := chunk.Values.(*storage.Int64Column).Values()
		v.Values.(*storage.Int64Column).AppendSlice(src[start:end])
	case storage.TypeFloat64:
		src := chunk.Values.(*storage.Float64Column).Values()
		v.Values.(*storage.Float64Column).AppendSlice(src[start:end])
	case storage.TypeString:
		src := chunk.Values.(*storage.StringColumn).Values()
		v.Values.(*storage.StringColumn).AppendSlice(src[start:end])
	case storage.TypeBool:
		src := chunk.Values.(*storage.BoolColumn).Values()
		v.Values.(*storage.BoolColumn).AppendSlice(src[start:end])
	default:
		return fmt.Errorf("exec: CopyFromChunk unknown type %s", v.Type)
	}
	v.length = n

	// Copy nulls. The source chunk's null bitmap is indexed by absolute row
	// [start,end); the destination bitmap is indexed by [0,n). We ask
	// HasNullsInRange about just this batch's slice rather than the whole
	// chunk — null-free data is HasNulls()'s worst case (must scan every
	// byte to confirm), and chunks grow much larger than batches. See
	// docs/phase3-profiling.md for the profiling story.
	if chunk.Nulls.HasNullsInRange(start, n) {
		for i := range n {
			if chunk.Nulls.IsNull(start + i) {
				v.Nulls.SetNull(i)
			}
		}
	}
	return nil
}

// AppendNull appends a NULL at the next row. Internally it stores a
// zero-value placeholder in Values and marks the bit in Nulls, so the
// Values/Nulls alignment invariant is preserved. Returns an error if
// the vector is full.
func (v *Vector) AppendNull() error {
	if v.length >= VectorSize {
		return fmt.Errorf("exec: vector full (size %d)", VectorSize)
	}
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
		panic(fmt.Sprintf("exec: AppendNull on unknown vector type %s", v.Type))
	}
	v.Nulls.SetNull(v.length)
	v.length++
	return nil
}

// newSizedColumnForType constructs a typed column with backing slice
// pre-allocated to capacity.
func newSizedColumnForType(t storage.ColumnType, capacity int) storage.Column {
	switch t {
	case storage.TypeInt64:
		return storage.NewInt64ColumnSized(capacity)
	case storage.TypeFloat64:
		return storage.NewFloat64ColumnSized(capacity)
	case storage.TypeString:
		return storage.NewStringColumnSized(capacity)
	case storage.TypeBool:
		return storage.NewBoolColumnSized(capacity)
	default:
		panic(fmt.Sprintf("exec: unknown column type %s", t))
	}
}
