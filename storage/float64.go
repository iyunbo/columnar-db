package storage

// Float64Column stores a contiguous slice of float64 values.
type Float64Column struct {
	data []float64
}

func NewFloat64Column() *Float64Column {
	return &Float64Column{}
}

// NewFloat64ColumnSized creates an empty column with backing storage
// pre-allocated to the given capacity.
func NewFloat64ColumnSized(capacity int) *Float64Column {
	return &Float64Column{data: make([]float64, 0, capacity)}
}

// Reset truncates the column to zero length while retaining the backing
// array for reuse.
func (c *Float64Column) Reset() {
	c.data = c.data[:0]
}

func NewFloat64ColumnFromSlice(data []float64) *Float64Column {
	cp := make([]float64, len(data))
	copy(cp, data)
	return &Float64Column{data: cp}
}

func (c *Float64Column) Type() ColumnType { return TypeFloat64 }
func (c *Float64Column) Len() int         { return len(c.data) }

func (c *Float64Column) Append(v float64) {
	c.data = append(c.data, v)
}

// AppendSlice bulk-appends a slice. Single memmove when capacity suffices.
func (c *Float64Column) AppendSlice(vs []float64) {
	c.data = append(c.data, vs...)
}

func (c *Float64Column) Get(i int) float64 {
	return c.data[i]
}

func (c *Float64Column) Values() []float64 {
	return c.data
}
