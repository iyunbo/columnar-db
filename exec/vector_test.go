package exec

import (
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

func TestVectorSizeConstant(t *testing.T) {
	if VectorSize != 1024 {
		t.Errorf("VectorSize = %d, want 1024", VectorSize)
	}
}

func TestNewVectorAllTypes(t *testing.T) {
	cases := []struct {
		name string
		typ  storage.ColumnType
	}{
		{"int64", storage.TypeInt64},
		{"float64", storage.TypeFloat64},
		{"string", storage.TypeString},
		{"bool", storage.TypeBool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVector(tc.typ)
			if v.Type != tc.typ {
				t.Errorf("Type = %s, want %s", v.Type, tc.typ)
			}
			if v.Len() != 0 {
				t.Errorf("fresh vector Len = %d, want 0", v.Len())
			}
			if v.Values == nil || v.Nulls == nil {
				t.Error("Values or Nulls is nil")
			}
		})
	}
}

func TestVectorInt64AppendAndRead(t *testing.T) {
	v := NewVector(storage.TypeInt64)
	for i := range VectorSize {
		if err := v.AppendInt64(int64(i * 2)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if v.Len() != VectorSize {
		t.Fatalf("Len = %d, want %d", v.Len(), VectorSize)
	}

	// Overflow must fail.
	if err := v.AppendInt64(0); err == nil {
		t.Error("expected overflow error, got nil")
	}

	col := v.Values.(*storage.Int64Column)
	for i := range VectorSize {
		if got := col.Get(i); got != int64(i*2) {
			t.Fatalf("row %d: got %d, want %d", i, got, i*2)
		}
		if v.IsNull(i) {
			t.Fatalf("row %d reported null", i)
		}
	}
}

func TestVectorFloat64AppendAndRead(t *testing.T) {
	v := NewVector(storage.TypeFloat64)
	for i := range 500 {
		if err := v.AppendFloat64(float64(i) + 0.5); err != nil {
			t.Fatal(err)
		}
	}
	if v.Len() != 500 {
		t.Fatalf("Len = %d, want 500", v.Len())
	}
	col := v.Values.(*storage.Float64Column)
	if got := col.Get(42); got != 42.5 {
		t.Errorf("Get(42) = %v, want 42.5", got)
	}
}

func TestVectorStringAppendAndRead(t *testing.T) {
	v := NewVector(storage.TypeString)
	values := []string{"alpha", "", "beta", "γ", "delta"}
	for _, s := range values {
		if err := v.AppendString(s); err != nil {
			t.Fatal(err)
		}
	}
	col := v.Values.(*storage.StringColumn)
	for i, want := range values {
		if got := col.Get(i); got != want {
			t.Errorf("row %d: got %q, want %q", i, got, want)
		}
	}
}

func TestVectorBoolAppendAndRead(t *testing.T) {
	v := NewVector(storage.TypeBool)
	pattern := []bool{true, false, true, true, false}
	for _, b := range pattern {
		if err := v.AppendBool(b); err != nil {
			t.Fatal(err)
		}
	}
	col := v.Values.(*storage.BoolColumn)
	for i, want := range pattern {
		if got := col.Get(i); got != want {
			t.Errorf("row %d: got %v, want %v", i, got, want)
		}
	}
}

func TestVectorWrongTypeAppendErrors(t *testing.T) {
	v := NewVector(storage.TypeInt64)
	if err := v.AppendFloat64(1.5); err == nil {
		t.Error("AppendFloat64 on Int64 vector should error")
	}
	if err := v.AppendString("x"); err == nil {
		t.Error("AppendString on Int64 vector should error")
	}
	if err := v.AppendBool(true); err == nil {
		t.Error("AppendBool on Int64 vector should error")
	}
}

func TestVectorNulls(t *testing.T) {
	v := NewVector(storage.TypeInt64)
	// Pattern: value, null, value, null, ...
	for i := range 10 {
		if i%2 == 0 {
			if err := v.AppendInt64(int64(i)); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := v.AppendNull(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if v.Len() != 10 {
		t.Fatalf("Len = %d, want 10", v.Len())
	}
	for i := range 10 {
		wantNull := i%2 == 1
		if got := v.IsNull(i); got != wantNull {
			t.Errorf("row %d: IsNull = %v, want %v", i, got, wantNull)
		}
	}
	if v.Nulls.NullCount() != 5 {
		t.Errorf("NullCount = %d, want 5", v.Nulls.NullCount())
	}
}

func TestVectorNullsAllTypes(t *testing.T) {
	// AppendNull must keep Values and Nulls aligned for every type.
	for _, typ := range []storage.ColumnType{
		storage.TypeInt64, storage.TypeFloat64, storage.TypeString, storage.TypeBool,
	} {
		v := NewVector(typ)
		if err := v.AppendNull(); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if v.Len() != 1 {
			t.Errorf("%s: Len = %d, want 1", typ, v.Len())
		}
		if !v.IsNull(0) {
			t.Errorf("%s: row 0 not null", typ)
		}
		if v.Values.Len() != 1 {
			t.Errorf("%s: Values.Len = %d, want 1 (alignment broken)", typ, v.Values.Len())
		}
	}
}

func TestVectorReset(t *testing.T) {
	v := NewVector(storage.TypeInt64)
	for i := range 100 {
		_ = v.AppendInt64(int64(i))
	}
	_ = v.AppendNull()
	v.Reset()
	if v.Len() != 0 {
		t.Errorf("after Reset Len = %d, want 0", v.Len())
	}
	if v.Nulls.NullCount() != 0 {
		t.Errorf("after Reset NullCount = %d, want 0", v.Nulls.NullCount())
	}
	// Still usable.
	if err := v.AppendInt64(42); err != nil {
		t.Fatalf("append after Reset: %v", err)
	}
	if v.Values.(*storage.Int64Column).Get(0) != 42 {
		t.Error("value after Reset wrong")
	}
}

func TestVectorIsNullOutOfRangePanics(t *testing.T) {
	cases := []struct {
		name string
		idx  int
	}{
		{"above length", 5},
		{"negative", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVector(storage.TypeInt64)
			_ = v.AppendInt64(1)
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic on IsNull(%d)", tc.idx)
				}
			}()
			v.IsNull(tc.idx)
		})
	}
}

func TestVectorAppendNullOverflow(t *testing.T) {
	v := NewVector(storage.TypeInt64)
	for i := range VectorSize {
		if err := v.AppendNull(); err != nil {
			t.Fatalf("null %d: %v", i, err)
		}
	}
	if err := v.AppendNull(); err == nil {
		t.Error("expected overflow error on AppendNull past VectorSize")
	}
}

func TestVectorTypedAccessors(t *testing.T) {
	// Int64
	vi := NewVector(storage.TypeInt64)
	for i := range 100 {
		_ = vi.AppendInt64(int64(i * 3))
	}
	ints := vi.Int64s()
	if len(ints) != 100 {
		t.Fatalf("Int64s len = %d, want 100", len(ints))
	}
	for i, got := range ints {
		if got != int64(i*3) {
			t.Fatalf("Int64s[%d] = %d, want %d", i, got, i*3)
		}
	}

	// Float64
	vf := NewVector(storage.TypeFloat64)
	_ = vf.AppendFloat64(1.5)
	_ = vf.AppendFloat64(2.5)
	if got := vf.Float64s(); len(got) != 2 || got[0] != 1.5 || got[1] != 2.5 {
		t.Errorf("Float64s = %v", got)
	}

	// String
	vs := NewVector(storage.TypeString)
	_ = vs.AppendString("a")
	_ = vs.AppendString("b")
	if got := vs.Strings(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Strings = %v", got)
	}

	// Bool
	vb := NewVector(storage.TypeBool)
	_ = vb.AppendBool(true)
	_ = vb.AppendBool(false)
	if got := vb.Bools(); len(got) != 2 || !got[0] || got[1] {
		t.Errorf("Bools = %v", got)
	}
}

func TestVectorTypedAccessorLengthMatchesLen(t *testing.T) {
	// Accessors must return a slice of exactly Len() — not the full backing
	// capacity of VectorSize. Filter relies on this.
	v := NewVector(storage.TypeInt64)
	for i := range 50 {
		_ = v.AppendInt64(int64(i))
	}
	if got := len(v.Int64s()); got != 50 {
		t.Errorf("len(Int64s()) = %d, want 50 (not %d backing capacity)", got, VectorSize)
	}
}

func TestVectorResetNoGhosts(t *testing.T) {
	// After Reset, a fresh pattern of nulls must not see leftover nulls
	// from the previous fill.
	v := NewVector(storage.TypeInt64)
	for range 50 {
		_ = v.AppendNull()
	}
	if v.Nulls.NullCount() != 50 {
		t.Fatalf("setup: NullCount = %d, want 50", v.Nulls.NullCount())
	}

	v.Reset()

	// Fill with non-null values. Nothing should report null.
	for i := range 50 {
		_ = v.AppendInt64(int64(i))
	}
	if v.Nulls.NullCount() != 0 {
		t.Errorf("after Reset+refill: NullCount = %d, want 0 (ghost nulls)", v.Nulls.NullCount())
	}
	for i := range 50 {
		if v.IsNull(i) {
			t.Errorf("row %d reported null after Reset", i)
		}
	}
}

func TestVectorResetZeroAllocs(t *testing.T) {
	// Reset is supposed to be the allocation-free recycle path. If this
	// ever regresses, Step 6's benchmark target will fail — catch it now.
	v := NewVector(storage.TypeInt64)
	// Prime: fill once so backing capacities exist.
	for i := range VectorSize {
		_ = v.AppendInt64(int64(i))
	}
	v.Reset()

	allocs := testing.AllocsPerRun(100, func() {
		v.Reset()
		for i := range VectorSize {
			_ = v.AppendInt64(int64(i))
		}
	})
	if allocs != 0 {
		t.Errorf("Reset+refill allocs/op = %v, want 0", allocs)
	}
}
