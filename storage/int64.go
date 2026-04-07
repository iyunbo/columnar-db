package storage

// Int64Column stores a contiguous slice of int64 values.
//
// In a columnar database, all values of one field are packed together.
// For example, an "age" column with 1M rows is stored as a single
// []int64 of length 1M — perfect for sequential scan and aggregation.
type Int64Column struct {
	data []int64
}

// NewInt64Column creates an empty Int64 column.
func NewInt64Column() *Int64Column {
	return &Int64Column{}
}

// NewInt64ColumnSized creates an empty Int64 column with backing storage
// pre-allocated to the given capacity. Use this when the final size is
// known up front (e.g. a vectorized batch of VectorSize values) to avoid
// grow-and-copy cycles during Append.
func NewInt64ColumnSized(capacity int) *Int64Column {
	return &Int64Column{data: make([]int64, 0, capacity)}
}

// Reset truncates the column to zero length while retaining the backing
// array for reuse. This is the allocation-free path for operators that
// recycle vectors across batches.
func (c *Int64Column) Reset() {
	c.data = c.data[:0]
}

// NewInt64ColumnFromSlice creates a column from existing data.
func NewInt64ColumnFromSlice(data []int64) *Int64Column {
	cp := make([]int64, len(data))
	copy(cp, data)
	return &Int64Column{data: cp}
}

func (c *Int64Column) Type() ColumnType { return TypeInt64 }
func (c *Int64Column) Len() int         { return len(c.data) }

// Append adds a value to the end of the column.
func (c *Int64Column) Append(v int64) {
	c.data = append(c.data, v)
}

// Get returns the value at index i. Panics if out of range.
func (c *Int64Column) Get(i int) int64 {
	return c.data[i]
}

// Values returns the underlying slice (read-only intent).
func (c *Int64Column) Values() []int64 {
	return c.data
}
