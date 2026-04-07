package exec

import "github.com/iyunbo/columnar-db/storage"

// Concrete aggregators implementing the Aggregator interface
// (see aggregate.go for the interface contract).
//
// Shape conventions used by every aggregator in this file:
//
//   - State is per-group columnar: a typed slice indexed by group
//     ordinal. Init allocates; Grow extends with copy.
//   - Update is the steady-state hot path. It branches once on
//     vec.Nulls.HasNulls() to choose between a null-free fast path
//     and a null-aware path, then once more on (groupIDs == nil)
//     to choose between scalar (single group 0) and grouped paths.
//     Four loop bodies per aggregator — verbose, but the only shape
//     that gives the compiler tight-loop predictability.
//   - groupIDs is dense in survivor order: groupIDs[k] is the group
//     for the k-th selected row, where the k-th selected row has
//     index sel.Indices()[k] in the vector. len(groupIDs) ==
//     sel.Len() when groupIDs != nil.
//   - Finalize uses the Vector Append API; callers must finalize in
//     group order.
//
// Compile-time interface checks:
var (
	_ Aggregator = (*CountStar)(nil)
	_ Aggregator = (*Int64Sum)(nil)
	_ Aggregator = (*Int64Min)(nil)
	_ Aggregator = (*Int64Max)(nil)
	_ Aggregator = (*Int64Avg)(nil)
	_ Aggregator = (*Float64Sum)(nil)
	_ Aggregator = (*Float64Min)(nil)
	_ Aggregator = (*Float64Max)(nil)
	_ Aggregator = (*Float64Avg)(nil)
)

// growInt64 / growBool / growFloat64 are tiny copy-grow helpers used
// by every aggregator's Grow. Inlined by the compiler.
func growInt64(s []int64, n int) []int64 {
	if n <= len(s) {
		return s
	}
	g := make([]int64, n)
	copy(g, s)
	return g
}
func growFloat64(s []float64, n int) []float64 {
	if n <= len(s) {
		return s
	}
	g := make([]float64, n)
	copy(g, s)
	return g
}
func growBool(s []bool, n int) []bool {
	if n <= len(s) {
		return s
	}
	g := make([]bool, n)
	copy(g, s)
	return g
}

// =====================================================================
// CountStar — counts selected rows. Ignores vec entirely (does not
// look at vec.Nulls), matching standard SQL COUNT(*) semantics: a row
// counts as long as it survives upstream filtering. Result is never
// null; an empty group counts 0.
// =====================================================================

type CountStar struct {
	counts []int64
}

func (c *CountStar) Init(numGroups int) { c.counts = make([]int64, numGroups) }
func (c *CountStar) Grow(numGroups int) { c.counts = growInt64(c.counts, numGroups) }

func (c *CountStar) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	if groupIDs == nil {
		c.counts[0] += int64(sel.Len())
		return
	}
	for _, g := range groupIDs {
		c.counts[g]++
	}
}

func (c *CountStar) OutputType() storage.ColumnType { return storage.TypeInt64 }
func (c *CountStar) Finalize(group int, out *Vector) {
	_ = out.AppendInt64(c.counts[group])
}

// =====================================================================
// Int64Sum — sums non-null int64 values. All-null group returns null.
// =====================================================================

type Int64Sum struct {
	sums     []int64
	hasValue []bool
}

func (a *Int64Sum) Init(numGroups int) {
	a.sums = make([]int64, numGroups)
	a.hasValue = make([]bool, numGroups)
}
func (a *Int64Sum) Grow(numGroups int) {
	a.sums = growInt64(a.sums, numGroups)
	a.hasValue = growBool(a.hasValue, numGroups)
}

func (a *Int64Sum) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			if len(indices) == 0 {
				return
			}
			sum := a.sums[0]
			for _, i := range indices {
				sum += vals[i]
			}
			a.sums[0] = sum
			a.hasValue[0] = true
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			a.sums[g] += vals[i]
			a.hasValue[g] = true
		}
		return
	}
	// null-aware
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			a.sums[0] += vals[i]
			a.hasValue[0] = true
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		a.sums[g] += vals[i]
		a.hasValue[g] = true
	}
}

func (a *Int64Sum) OutputType() storage.ColumnType { return storage.TypeInt64 }
func (a *Int64Sum) Finalize(group int, out *Vector) {
	if !a.hasValue[group] {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendInt64(a.sums[group])
}

// =====================================================================
// Int64Min / Int64Max — track minimum/maximum non-null int64 per
// group. All-null group returns null.
// =====================================================================

type Int64Min struct {
	mins     []int64
	hasValue []bool
}

func (a *Int64Min) Init(numGroups int) {
	a.mins = make([]int64, numGroups)
	a.hasValue = make([]bool, numGroups)
}
func (a *Int64Min) Grow(numGroups int) {
	a.mins = growInt64(a.mins, numGroups)
	a.hasValue = growBool(a.hasValue, numGroups)
}

func (a *Int64Min) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			for _, i := range indices {
				v := vals[i]
				if !a.hasValue[0] || v < a.mins[0] {
					a.mins[0] = v
					a.hasValue[0] = true
				}
			}
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			v := vals[i]
			if !a.hasValue[g] || v < a.mins[g] {
				a.mins[g] = v
				a.hasValue[g] = true
			}
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			v := vals[i]
			if !a.hasValue[0] || v < a.mins[0] {
				a.mins[0] = v
				a.hasValue[0] = true
			}
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		v := vals[i]
		if !a.hasValue[g] || v < a.mins[g] {
			a.mins[g] = v
			a.hasValue[g] = true
		}
	}
}

func (a *Int64Min) OutputType() storage.ColumnType { return storage.TypeInt64 }
func (a *Int64Min) Finalize(group int, out *Vector) {
	if !a.hasValue[group] {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendInt64(a.mins[group])
}

type Int64Max struct {
	maxs     []int64
	hasValue []bool
}

func (a *Int64Max) Init(numGroups int) {
	a.maxs = make([]int64, numGroups)
	a.hasValue = make([]bool, numGroups)
}
func (a *Int64Max) Grow(numGroups int) {
	a.maxs = growInt64(a.maxs, numGroups)
	a.hasValue = growBool(a.hasValue, numGroups)
}

func (a *Int64Max) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			for _, i := range indices {
				v := vals[i]
				if !a.hasValue[0] || v > a.maxs[0] {
					a.maxs[0] = v
					a.hasValue[0] = true
				}
			}
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			v := vals[i]
			if !a.hasValue[g] || v > a.maxs[g] {
				a.maxs[g] = v
				a.hasValue[g] = true
			}
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			v := vals[i]
			if !a.hasValue[0] || v > a.maxs[0] {
				a.maxs[0] = v
				a.hasValue[0] = true
			}
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		v := vals[i]
		if !a.hasValue[g] || v > a.maxs[g] {
			a.maxs[g] = v
			a.hasValue[g] = true
		}
	}
}

func (a *Int64Max) OutputType() storage.ColumnType { return storage.TypeInt64 }
func (a *Int64Max) Finalize(group int, out *Vector) {
	if !a.hasValue[group] {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendInt64(a.maxs[group])
}

// =====================================================================
// Int64Avg — averages non-null int64 values per group, output as
// float64. All-null group returns null (NOT divide-by-zero).
// =====================================================================

type Int64Avg struct {
	sums   []int64
	counts []int64
}

func (a *Int64Avg) Init(numGroups int) {
	a.sums = make([]int64, numGroups)
	a.counts = make([]int64, numGroups)
}
func (a *Int64Avg) Grow(numGroups int) {
	a.sums = growInt64(a.sums, numGroups)
	a.counts = growInt64(a.counts, numGroups)
}

func (a *Int64Avg) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			for _, i := range indices {
				a.sums[0] += vals[i]
			}
			a.counts[0] += int64(len(indices))
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			a.sums[g] += vals[i]
			a.counts[g]++
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			a.sums[0] += vals[i]
			a.counts[0]++
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		a.sums[g] += vals[i]
		a.counts[g]++
	}
}

func (a *Int64Avg) OutputType() storage.ColumnType { return storage.TypeFloat64 }
func (a *Int64Avg) Finalize(group int, out *Vector) {
	if a.counts[group] == 0 {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendFloat64(float64(a.sums[group]) / float64(a.counts[group]))
}

// =====================================================================
// Float64Sum / Float64Min / Float64Max / Float64Avg — same shapes as
// the int64 variants. NaN handling is the natural one Go gives:
// NaN compared with `<` or `>` returns false, so NaN values neither
// move Min nor Max but DO contribute to Sum/Avg. SQL standard
// behavior on NaN is implementation-defined; documenting ours here.
// =====================================================================

type Float64Sum struct {
	sums     []float64
	hasValue []bool
}

func (a *Float64Sum) Init(numGroups int) {
	a.sums = make([]float64, numGroups)
	a.hasValue = make([]bool, numGroups)
}
func (a *Float64Sum) Grow(numGroups int) {
	a.sums = growFloat64(a.sums, numGroups)
	a.hasValue = growBool(a.hasValue, numGroups)
}

func (a *Float64Sum) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Float64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			if len(indices) == 0 {
				return
			}
			sum := a.sums[0]
			for _, i := range indices {
				sum += vals[i]
			}
			a.sums[0] = sum
			a.hasValue[0] = true
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			a.sums[g] += vals[i]
			a.hasValue[g] = true
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			a.sums[0] += vals[i]
			a.hasValue[0] = true
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		a.sums[g] += vals[i]
		a.hasValue[g] = true
	}
}

func (a *Float64Sum) OutputType() storage.ColumnType { return storage.TypeFloat64 }
func (a *Float64Sum) Finalize(group int, out *Vector) {
	if !a.hasValue[group] {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendFloat64(a.sums[group])
}

type Float64Min struct {
	mins     []float64
	hasValue []bool
}

func (a *Float64Min) Init(numGroups int) {
	a.mins = make([]float64, numGroups)
	a.hasValue = make([]bool, numGroups)
}
func (a *Float64Min) Grow(numGroups int) {
	a.mins = growFloat64(a.mins, numGroups)
	a.hasValue = growBool(a.hasValue, numGroups)
}

func (a *Float64Min) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Float64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			for _, i := range indices {
				v := vals[i]
				if !a.hasValue[0] || v < a.mins[0] {
					a.mins[0] = v
					a.hasValue[0] = true
				}
			}
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			v := vals[i]
			if !a.hasValue[g] || v < a.mins[g] {
				a.mins[g] = v
				a.hasValue[g] = true
			}
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			v := vals[i]
			if !a.hasValue[0] || v < a.mins[0] {
				a.mins[0] = v
				a.hasValue[0] = true
			}
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		v := vals[i]
		if !a.hasValue[g] || v < a.mins[g] {
			a.mins[g] = v
			a.hasValue[g] = true
		}
	}
}

func (a *Float64Min) OutputType() storage.ColumnType { return storage.TypeFloat64 }
func (a *Float64Min) Finalize(group int, out *Vector) {
	if !a.hasValue[group] {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendFloat64(a.mins[group])
}

type Float64Max struct {
	maxs     []float64
	hasValue []bool
}

func (a *Float64Max) Init(numGroups int) {
	a.maxs = make([]float64, numGroups)
	a.hasValue = make([]bool, numGroups)
}
func (a *Float64Max) Grow(numGroups int) {
	a.maxs = growFloat64(a.maxs, numGroups)
	a.hasValue = growBool(a.hasValue, numGroups)
}

func (a *Float64Max) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Float64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			for _, i := range indices {
				v := vals[i]
				if !a.hasValue[0] || v > a.maxs[0] {
					a.maxs[0] = v
					a.hasValue[0] = true
				}
			}
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			v := vals[i]
			if !a.hasValue[g] || v > a.maxs[g] {
				a.maxs[g] = v
				a.hasValue[g] = true
			}
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			v := vals[i]
			if !a.hasValue[0] || v > a.maxs[0] {
				a.maxs[0] = v
				a.hasValue[0] = true
			}
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		v := vals[i]
		if !a.hasValue[g] || v > a.maxs[g] {
			a.maxs[g] = v
			a.hasValue[g] = true
		}
	}
}

func (a *Float64Max) OutputType() storage.ColumnType { return storage.TypeFloat64 }
func (a *Float64Max) Finalize(group int, out *Vector) {
	if !a.hasValue[group] {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendFloat64(a.maxs[group])
}

type Float64Avg struct {
	sums   []float64
	counts []int64
}

func (a *Float64Avg) Init(numGroups int) {
	a.sums = make([]float64, numGroups)
	a.counts = make([]int64, numGroups)
}
func (a *Float64Avg) Grow(numGroups int) {
	a.sums = growFloat64(a.sums, numGroups)
	a.counts = growInt64(a.counts, numGroups)
}

func (a *Float64Avg) Update(vec *Vector, sel *Selection, groupIDs []int32) {
	vals := vec.Float64s()
	nulls := vec.Nulls
	indices := sel.Indices()

	if !nulls.HasNulls() {
		if groupIDs == nil {
			for _, i := range indices {
				a.sums[0] += vals[i]
			}
			a.counts[0] += int64(len(indices))
			return
		}
		for k, i := range indices {
			g := groupIDs[k]
			a.sums[g] += vals[i]
			a.counts[g]++
		}
		return
	}
	if groupIDs == nil {
		for _, i := range indices {
			if nulls.IsNull(int(i)) {
				continue
			}
			a.sums[0] += vals[i]
			a.counts[0]++
		}
		return
	}
	for k, i := range indices {
		if nulls.IsNull(int(i)) {
			continue
		}
		g := groupIDs[k]
		a.sums[g] += vals[i]
		a.counts[g]++
	}
}

func (a *Float64Avg) OutputType() storage.ColumnType { return storage.TypeFloat64 }
func (a *Float64Avg) Finalize(group int, out *Vector) {
	if a.counts[group] == 0 {
		_ = out.AppendNull()
		return
	}
	_ = out.AppendFloat64(a.sums[group] / float64(a.counts[group]))
}
