package storage

// BoolColumn stores a contiguous slice of bool values.
//
// Note: Go's []bool uses 1 byte per value, not 1 bit.
// For storage-efficient representation, we'll use bit-packing
// in the null bitmap (Step 2) and encoding (Phase 2).
type BoolColumn struct {
	data []bool
}

func NewBoolColumn() *BoolColumn {
	return &BoolColumn{}
}

// NewBoolColumnSized creates an empty column with backing storage
// pre-allocated to the given capacity.
func NewBoolColumnSized(capacity int) *BoolColumn {
	return &BoolColumn{data: make([]bool, 0, capacity)}
}

// Reset truncates the column to zero length while retaining the backing
// array for reuse.
func (c *BoolColumn) Reset() {
	c.data = c.data[:0]
}

func NewBoolColumnFromSlice(data []bool) *BoolColumn {
	cp := make([]bool, len(data))
	copy(cp, data)
	return &BoolColumn{data: cp}
}

func (c *BoolColumn) Type() ColumnType { return TypeBool }
func (c *BoolColumn) Len() int         { return len(c.data) }

func (c *BoolColumn) Append(v bool) {
	c.data = append(c.data, v)
}

// AppendSlice bulk-appends a slice. Single memmove when capacity suffices.
func (c *BoolColumn) AppendSlice(vs []bool) {
	c.data = append(c.data, vs...)
}

func (c *BoolColumn) Get(i int) bool {
	return c.data[i]
}

func (c *BoolColumn) Values() []bool {
	return c.data
}
