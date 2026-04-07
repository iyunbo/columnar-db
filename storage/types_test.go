package storage

import (
	"testing"
)

func TestInt64Column(t *testing.T) {
	col := NewInt64Column()

	// Append 1000 values
	for i := range 1000 {
		col.Append(int64(i * 3))
	}

	if col.Len() != 1000 {
		t.Fatalf("expected len 1000, got %d", col.Len())
	}
	if col.Type() != TypeInt64 {
		t.Fatalf("expected TypeInt64, got %v", col.Type())
	}

	// Verify values
	for i := range 1000 {
		expected := int64(i * 3)
		if got := col.Get(i); got != expected {
			t.Fatalf("index %d: expected %d, got %d", i, expected, got)
		}
	}
}

func TestInt64ColumnFromSlice(t *testing.T) {
	data := []int64{10, 20, 30, 40, 50}
	col := NewInt64ColumnFromSlice(data)

	if col.Len() != 5 {
		t.Fatalf("expected len 5, got %d", col.Len())
	}

	// Modify original — should not affect column (copy semantics)
	data[0] = 999
	if col.Get(0) != 10 {
		t.Fatal("column should not be affected by modifying original slice")
	}
}

func TestFloat64Column(t *testing.T) {
	col := NewFloat64Column()
	for i := range 500 {
		col.Append(float64(i) * 1.5)
	}

	if col.Len() != 500 {
		t.Fatalf("expected len 500, got %d", col.Len())
	}
	if col.Type() != TypeFloat64 {
		t.Fatalf("expected TypeFloat64, got %v", col.Type())
	}
	if col.Get(100) != 150.0 {
		t.Fatalf("expected 150.0, got %f", col.Get(100))
	}
}

func TestStringColumn(t *testing.T) {
	col := NewStringColumn()
	words := []string{"hello", "world", "columnar", "database", "go"}
	for _, w := range words {
		col.Append(w)
	}

	if col.Len() != 5 {
		t.Fatalf("expected len 5, got %d", col.Len())
	}
	if col.Type() != TypeString {
		t.Fatalf("expected TypeString, got %v", col.Type())
	}
	if col.Get(2) != "columnar" {
		t.Fatalf("expected 'columnar', got '%s'", col.Get(2))
	}
}

func TestBoolColumn(t *testing.T) {
	col := NewBoolColumn()
	for i := range 100 {
		col.Append(i%2 == 0)
	}

	if col.Len() != 100 {
		t.Fatalf("expected len 100, got %d", col.Len())
	}
	if col.Type() != TypeBool {
		t.Fatalf("expected TypeBool, got %v", col.Type())
	}
	if col.Get(0) != true {
		t.Fatal("expected index 0 to be true")
	}
	if col.Get(1) != false {
		t.Fatal("expected index 1 to be false")
	}
}

func TestColumnTypeString(t *testing.T) {
	tests := []struct {
		t    ColumnType
		want string
	}{
		{TypeInt64, "Int64"},
		{TypeFloat64, "Float64"},
		{TypeString, "String"},
		{TypeBool, "Bool"},
		{ColumnType(99), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.t.String(); got != tt.want {
			t.Errorf("ColumnType(%d).String() = %q, want %q", tt.t, got, tt.want)
		}
	}
}

// Verify Column interface is satisfied
func TestColumnInterface(t *testing.T) {
	var _ Column = NewInt64Column()
	var _ Column = NewFloat64Column()
	var _ Column = NewStringColumn()
	var _ Column = NewBoolColumn()
}
