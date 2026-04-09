package exec

import (
	"fmt"
	"sort"

	"github.com/iyunbo/columnar-db/storage"
)

// OrderByOp sorts the output of its child by a single column.
// It is a blocking operator: the first Next() drains the child
// entirely, sorts the collected rows, and emits them in
// VectorSize-wide batches via a cursor (same shape as GroupByOp).
//
// Implementation: collect all surviving rows column-wise into
// typed Go slices, build a permutation index sorted by the
// designated column, then emit rows in permuted order. This
// materializes the full result set — ORDER BY is inherently
// incompatible with streaming, same as in any real database.
//
// Phase 5.5 scope: single sort column only. Multi-column sort
// is a natural extension (sort.Slice with a chained comparator).
type OrderByOp struct {
	child     Operator
	sortCol   int // column index in child output to sort by
	ascending bool

	// Collected data (populated on first Next).
	cols       []collectedCol
	perm       []int // permutation: perm[i] = source row of output row i
	totalRows  int
	ingested   bool
	emitCursor int

	out *Batch
}

type collectedCol struct {
	typ      storage.ColumnType
	int64s   []int64
	float64s []float64
	strings  []string
	nulls    []bool
}

var _ Operator = (*OrderByOp)(nil)

func NewOrderByOp(child Operator, sortCol int, ascending bool) *OrderByOp {
	return &OrderByOp{
		child:     child,
		sortCol:   sortCol,
		ascending: ascending,
	}
}

func (o *OrderByOp) Next() (*Batch, bool) {
	if !o.ingested {
		if err := o.ingest(); err != nil {
			panic(err) // structural error, not user input
		}
		o.ingested = true
	}
	if o.emitCursor >= o.totalRows {
		return nil, false
	}

	start := o.emitCursor
	end := start + VectorSize
	if end > o.totalRows {
		end = o.totalRows
	}

	if o.out == nil {
		vecs := make([]*Vector, len(o.cols))
		for i, c := range o.cols {
			vecs[i] = NewVector(c.typ)
		}
		o.out = &Batch{Vectors: vecs, Sel: NewSelection()}
	} else {
		o.out.Reset()
	}

	for ci, c := range o.cols {
		vec := o.out.Vectors[ci]
		for ri := start; ri < end; ri++ {
			srcRow := o.perm[ri]
			if c.nulls[srcRow] {
				_ = vec.AppendNull()
				continue
			}
			switch c.typ {
			case storage.TypeInt64:
				_ = vec.AppendInt64(c.int64s[srcRow])
			case storage.TypeFloat64:
				_ = vec.AppendFloat64(c.float64s[srcRow])
			case storage.TypeString:
				_ = vec.AppendString(c.strings[srcRow])
			case storage.TypeBool:
				_ = vec.AppendBool(c.int64s[srcRow] != 0)
			}
		}
	}

	n := end - start
	for i := range n {
		o.out.Sel.Add(i)
	}
	o.emitCursor = end
	return o.out, true
}

func (o *OrderByOp) Reset() {
	o.child.Reset()
	o.cols = nil
	o.perm = nil
	o.totalRows = 0
	o.ingested = false
	o.emitCursor = 0
	if o.out != nil {
		o.out.Reset()
	}
}

func (o *OrderByOp) ingest() error {
	// Drain child, collecting all rows column-wise.
	var numCols int
	for {
		b, ok := o.child.Next()
		if !ok {
			break
		}
		if o.cols == nil {
			numCols = b.NumColumns()
			o.cols = make([]collectedCol, numCols)
			for i, v := range b.Vectors {
				o.cols[i].typ = v.Type
			}
		}
		for _, idx := range b.Sel.Indices() {
			for ci, v := range b.Vectors {
				c := &o.cols[ci]
				isNull := v.Nulls.HasNulls() && v.Nulls.IsNull(int(idx))
				c.nulls = append(c.nulls, isNull)
				if isNull {
					switch c.typ {
					case storage.TypeInt64:
						c.int64s = append(c.int64s, 0)
					case storage.TypeFloat64:
						c.float64s = append(c.float64s, 0)
					case storage.TypeString:
						c.strings = append(c.strings, "")
					}
					continue
				}
				switch c.typ {
				case storage.TypeInt64:
					c.int64s = append(c.int64s, v.Int64s()[idx])
				case storage.TypeFloat64:
					c.float64s = append(c.float64s, v.Float64s()[idx])
				case storage.TypeString:
					c.strings = append(c.strings, v.Strings()[idx])
				}
			}
			o.totalRows++
		}
	}

	if o.totalRows == 0 {
		return nil
	}
	if o.sortCol < 0 || o.sortCol >= numCols {
		return fmt.Errorf("exec: OrderByOp sortCol %d out of range [0,%d)", o.sortCol, numCols)
	}

	// Build permutation sorted by the designated column.
	o.perm = make([]int, o.totalRows)
	for i := range o.perm {
		o.perm[i] = i
	}

	sc := &o.cols[o.sortCol]
	asc := o.ascending
	sort.SliceStable(o.perm, func(i, j int) bool {
		a, b := o.perm[i], o.perm[j]
		// Nulls sort last regardless of direction.
		if sc.nulls[a] != sc.nulls[b] {
			return !sc.nulls[a]
		}
		if sc.nulls[a] {
			return false
		}
		switch sc.typ {
		case storage.TypeInt64:
			if asc {
				return sc.int64s[a] < sc.int64s[b]
			}
			return sc.int64s[a] > sc.int64s[b]
		case storage.TypeFloat64:
			if asc {
				return sc.float64s[a] < sc.float64s[b]
			}
			return sc.float64s[a] > sc.float64s[b]
		case storage.TypeString:
			if asc {
				return sc.strings[a] < sc.strings[b]
			}
			return sc.strings[a] > sc.strings[b]
		}
		return false
	})
	return nil
}
