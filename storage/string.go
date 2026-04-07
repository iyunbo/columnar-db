package storage

// StringColumn stores a contiguous slice of string values.
//
// Note: strings are variable-length, so this is inherently less
// cache-friendly than fixed-size columns. In Phase 2, dictionary
// encoding will convert low-cardinality strings into integer indices.
type StringColumn struct {
	data []string
}

func NewStringColumn() *StringColumn {
	return &StringColumn{}
}

// NewStringColumnSized creates an empty column with backing storage
// pre-allocated to the given capacity.
func NewStringColumnSized(capacity int) *StringColumn {
	return &StringColumn{data: make([]string, 0, capacity)}
}

// Reset truncates the column to zero length while retaining the backing
// array for reuse. Note: previously stored string headers remain in the
// unused tail of the backing array until overwritten — if you're worried
// about GC pinning large strings, use NewStringColumn instead.
func (c *StringColumn) Reset() {
	c.data = c.data[:0]
}

func NewStringColumnFromSlice(data []string) *StringColumn {
	cp := make([]string, len(data))
	copy(cp, data)
	return &StringColumn{data: cp}
}

func (c *StringColumn) Type() ColumnType { return TypeString }
func (c *StringColumn) Len() int         { return len(c.data) }

func (c *StringColumn) Append(v string) {
	c.data = append(c.data, v)
}

func (c *StringColumn) Get(i int) string {
	return c.data[i]
}

func (c *StringColumn) Values() []string {
	return c.data
}
