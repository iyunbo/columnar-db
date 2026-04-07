package exec

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// This benchmark file exists to justify the hand-written 12 predicate
// loops over less-verbose alternatives. It measures three shapes of
// the "int64 > value" filter hot path:
//
//  1. handRolled: the current predicate.go shape — per-operator
//     function with an inline literal comparison.
//  2. funcValue:  a single polymorphic function that takes the
//     comparison as a `func(a, b int64) bool` closure.
//  3. generic:    a type-parameterized function with a method-based
//     comparator (Go 1.18+ generics with method constraint).
//
// All three are functionally identical. If the compiler does its job,
// funcValue and generic should compile down to the same code as
// handRolled. If it doesn't, we'll see the cost in ns/op and allocs.
//
// Run: go test ./exec/ -bench=Predicate -benchmem -run=^$

// --- Alternative 1: function value -----------------------------------

func evalInt64FuncValue(vec *Vector, in, out *Selection, val int64, op func(a, b int64) bool) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if op(vals[i], val) {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && op(vals[i], val) {
			out.AppendUint16Unchecked(i)
		}
	}
}

// --- Alternative 2: generic with method-based comparator --------------

type int64Cmp interface {
	compare(a, b int64) bool
}

type int64CmpGt struct{}

func (int64CmpGt) compare(a, b int64) bool { return a > b }

func evalInt64Generic[C int64Cmp](vec *Vector, in, out *Selection, val int64, cmp C) {
	vals := vec.Int64s()
	nulls := vec.Nulls
	out.Reset()
	if !nulls.HasNulls() {
		for _, i := range in.Indices() {
			if cmp.compare(vals[i], val) {
				out.AppendUint16Unchecked(i)
			}
		}
		return
	}
	for _, i := range in.Indices() {
		if !nulls.IsNull(int(i)) && cmp.compare(vals[i], val) {
			out.AppendUint16Unchecked(i)
		}
	}
}

// --- Benchmark fixtures ----------------------------------------------

func benchSetup(withNulls bool) (*Vector, *Selection, *Selection) {
	vec := NewVector(storage.TypeInt64)
	for i := 0; i < VectorSize; i++ {
		_ = vec.AppendInt64(int64(i))
	}
	if withNulls {
		// Sprinkle nulls at 4 positions to trigger the null-aware path
		// without dominating the numbers.
		for _, idx := range []int{7, 500, 999, 1023} {
			vec.Nulls.SetNull(idx)
		}
	}
	in := NewFullSelection(VectorSize)
	out := NewSelection()
	return vec, in, out
}

// --- Benchmarks ------------------------------------------------------

func BenchmarkPredicateHandRolled_NullFree(b *testing.B) {
	vec, in, out := benchSetup(false)
	pred := Int64Gt{Value: 500}
	b.ReportAllocs()
	for b.Loop() {
		pred.Eval(vec, in, out)
	}
}

func BenchmarkPredicateFuncValue_NullFree(b *testing.B) {
	vec, in, out := benchSetup(false)
	op := func(a, c int64) bool { return a > c }
	b.ReportAllocs()
	for b.Loop() {
		evalInt64FuncValue(vec, in, out, 500, op)
	}
}

func BenchmarkPredicateGeneric_NullFree(b *testing.B) {
	vec, in, out := benchSetup(false)
	cmp := int64CmpGt{}
	b.ReportAllocs()
	for b.Loop() {
		evalInt64Generic(vec, in, out, 500, cmp)
	}
}

func BenchmarkPredicateHandRolled_NullAware(b *testing.B) {
	vec, in, out := benchSetup(true)
	pred := Int64Gt{Value: 500}
	b.ReportAllocs()
	for b.Loop() {
		pred.Eval(vec, in, out)
	}
}

func BenchmarkPredicateFuncValue_NullAware(b *testing.B) {
	vec, in, out := benchSetup(true)
	op := func(a, c int64) bool { return a > c }
	b.ReportAllocs()
	for b.Loop() {
		evalInt64FuncValue(vec, in, out, 500, op)
	}
}

func BenchmarkPredicateGeneric_NullAware(b *testing.B) {
	vec, in, out := benchSetup(true)
	cmp := int64CmpGt{}
	b.ReportAllocs()
	for b.Loop() {
		evalInt64Generic(vec, in, out, 500, cmp)
	}
}
