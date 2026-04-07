package storage

import (
	"fmt"
	"math"
)

// ColumnChunk is the fundamental storage unit: a named, typed column
// of values with null tracking and summary statistics.
//
// In a columnar database, data is stored as column chunks — each chunk
// holds all values of one field for a group of rows. This is the building
// block that row groups and the file format are built on.
//
// A chunk combines:
//   - Values: contiguous typed array (Int64, Float64, String, Bool)
//   - Nulls:  bit-packed null bitmap
//   - Stats:  min, max, null count (for zone maps / skip scanning later)
//
// Nulls is a live bitmap — the null count is NOT cached on the struct.
// Earlier versions stored NullCount as a field set at construction,
// which silently drifted if any caller called SetNull on the bitmap
// afterwards (Phase 3 Step 6 benchmark write-up flagged this). Now
// the count is computed on demand from Nulls.NullCount(), so the two
// can never disagree.
type ColumnChunk struct {
	Name    string     // column name (e.g., "age", "city")
	ColType ColumnType // data type
	Values  Column     // typed values (implements Column interface)
	Nulls   *NullBitmap
	Count   int // total number of rows
}

// NullCount returns the number of NULL rows. Computed on every call
// by walking the null bitmap (O(n_bytes) with popcount), so it is
// always consistent with the current state of Nulls.
func (c *ColumnChunk) NullCount() int {
	return c.Nulls.NullCount()
}

// NewColumnChunk creates a chunk from a typed column and null bitmap.
func NewColumnChunk(name string, values Column, nulls *NullBitmap) (*ColumnChunk, error) {
	if values.Len() != nulls.Len() {
		return nil, fmt.Errorf("values length (%d) != bitmap length (%d)", values.Len(), nulls.Len())
	}

	return &ColumnChunk{
		Name:    name,
		ColType: values.Type(),
		Values:  values,
		Nulls:   nulls,
		Count:   values.Len(),
	}, nil
}

// NewColumnChunkNoNulls creates a chunk with no null values.
func NewColumnChunkNoNulls(name string, values Column) *ColumnChunk {
	nulls := NewNullBitmap(values.Len())
	return &ColumnChunk{
		Name:    name,
		ColType: values.Type(),
		Values:  values,
		Nulls:   nulls,
		Count:   values.Len(),
	}
}

// ColumnStats holds summary statistics for a column chunk.
// Used for zone maps / skip scanning in Phase 6.
type ColumnStats struct {
	Min       any // minimum non-null value
	Max       any // maximum non-null value
	NullCount int
	Count     int
}

// Stats computes summary statistics for this chunk.
func (c *ColumnChunk) Stats() ColumnStats {
	stats := ColumnStats{
		NullCount: c.NullCount(),
		Count:     c.Count,
	}

	switch col := c.Values.(type) {
	case *Int64Column:
		stats.Min, stats.Max = int64Stats(col, c.Nulls)
	case *Float64Column:
		stats.Min, stats.Max = float64Stats(col, c.Nulls)
	case *StringColumn:
		stats.Min, stats.Max = stringStats(col, c.Nulls)
	case *BoolColumn:
		stats.Min, stats.Max = boolStats(col, c.Nulls)
	}

	return stats
}

// --- Stats helpers ---

func int64Stats(col *Int64Column, nulls *NullBitmap) (any, any) {
	var min, max int64
	first := true
	for i := 0; i < col.Len(); i++ {
		if nulls.IsNull(i) {
			continue
		}
		v := col.Get(i)
		if first {
			min, max = v, v
			first = false
		} else {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if first {
		return nil, nil // all nulls
	}
	return min, max
}

func float64Stats(col *Float64Column, nulls *NullBitmap) (any, any) {
	min, max := math.Inf(1), math.Inf(-1)
	first := true
	for i := 0; i < col.Len(); i++ {
		if nulls.IsNull(i) {
			continue
		}
		v := col.Get(i)
		if first {
			min, max = v, v
			first = false
		} else {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if first {
		return nil, nil
	}
	return min, max
}

func stringStats(col *StringColumn, nulls *NullBitmap) (any, any) {
	var min, max string
	first := true
	for i := 0; i < col.Len(); i++ {
		if nulls.IsNull(i) {
			continue
		}
		v := col.Get(i)
		if first {
			min, max = v, v
			first = false
		} else {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if first {
		return nil, nil
	}
	return min, max
}

func boolStats(col *BoolColumn, nulls *NullBitmap) (any, any) {
	hasFalse, hasTrue := false, false
	for i := 0; i < col.Len(); i++ {
		if nulls.IsNull(i) {
			continue
		}
		if col.Get(i) {
			hasTrue = true
		} else {
			hasFalse = true
		}
		if hasFalse && hasTrue {
			break
		}
	}
	if !hasFalse && !hasTrue {
		return nil, nil // all nulls
	}
	return hasFalse || !hasTrue, hasTrue // min=false(0), max=true(1) when both exist
}
