package exec

// The six Int64 predicates below are intentionally written out as
// separate functions with duplicated null-free and null-aware loops
// (12 near-identical loop bodies total). This is deliberate: generics
// or helper closures would route the comparison through a function
// value, which the current Go compiler does not reliably inline tight
// enough to keep the loop SIMD-friendly. The zero-alloc steady-state
// benchmark (Step 6) depends on this being a plain indexable typed
// slice loop. If you refactor, re-run the benchmark — regressions here
// cascade through every query.

// Predicate applies a boolean test against values in a single Vector
// and narrows an input Selection into an output Selection.
//
// Contract:
//   - `vec` is the column to test.
//   - `in` holds the row indices that are still alive on entry.
//   - `out` is filled with the subset of `in` for which the predicate
//     returns true. `out` is Reset() inside Eval — callers pass in a
//     scratch Selection, they don't have to clear it first.
//   - Nulls NEVER match any predicate (simple three-valued logic). A
//     row that is null passes through all comparison filters as
//     "false", so it never appears in `out`.
//
// Hot-path shape: a typical Eval is a tight `for _, i := range
// in.Indices()` loop over the typed slice (vec.Int64s(), ...) with no
// interface calls inside the loop body. Implementations are expected
// to branch once on Nulls.HasNulls() and pick between a fast null-free
// path and a slower null-aware path.
type Predicate interface {
	Eval(vec *Vector, in, out *Selection)
}

// ---------------------------------------------------------------------
// Int64 predicates
// ---------------------------------------------------------------------

// Int64Eq matches rows where the column value equals Value (and is not null).
type Int64Eq struct{ Value int64 }

// Eval implements Predicate.
func (p Int64Eq) Eval(vec *Vector, in, out *Selection) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if vals[i] == p.Value {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && vals[i] == p.Value {
			out.AppendUint16Unchecked(i)
		}
	}
}

// Int64Ne matches rows where the column value is not equal to Value.
type Int64Ne struct{ Value int64 }

// Eval implements Predicate.
func (p Int64Ne) Eval(vec *Vector, in, out *Selection) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if vals[i] != p.Value {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && vals[i] != p.Value {
			out.AppendUint16Unchecked(i)
		}
	}
}

// Int64Lt matches rows where the column value is strictly less than Value.
type Int64Lt struct{ Value int64 }

// Eval implements Predicate.
func (p Int64Lt) Eval(vec *Vector, in, out *Selection) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if vals[i] < p.Value {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && vals[i] < p.Value {
			out.AppendUint16Unchecked(i)
		}
	}
}

// Int64Le matches rows where the column value is less than or equal to Value.
type Int64Le struct{ Value int64 }

// Eval implements Predicate.
func (p Int64Le) Eval(vec *Vector, in, out *Selection) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if vals[i] <= p.Value {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && vals[i] <= p.Value {
			out.AppendUint16Unchecked(i)
		}
	}
}

// Int64Gt matches rows where the column value is strictly greater than Value.
type Int64Gt struct{ Value int64 }

// Eval implements Predicate.
func (p Int64Gt) Eval(vec *Vector, in, out *Selection) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if vals[i] > p.Value {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && vals[i] > p.Value {
			out.AppendUint16Unchecked(i)
		}
	}
}

// Int64Ge matches rows where the column value is greater than or equal to Value.
type Int64Ge struct{ Value int64 }

// Eval implements Predicate.
func (p Int64Ge) Eval(vec *Vector, in, out *Selection) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if vals[i] >= p.Value {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && vals[i] >= p.Value {
			out.AppendUint16Unchecked(i)
		}
	}
}
