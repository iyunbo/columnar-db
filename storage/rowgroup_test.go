package storage

import (
	"testing"
)

func makeTestRowGroup(t *testing.T, n int) *RowGroup {
	t.Helper()

	ids := NewInt64Column()
	prices := NewFloat64Column()
	cities := NewStringColumn()
	active := NewBoolColumn()

	for i := range n {
		ids.Append(int64(i + 1))
		prices.Append(float64(i) * 9.99)
		cities.Append([]string{"Paris", "London", "Tokyo", "Berlin"}[i%4])
		active.Append(i%3 != 0)
	}

	// Some nulls in prices
	priceNulls := NewNullBitmap(n)
	for i := 0; i < n; i += 7 {
		priceNulls.SetNull(i)
	}

	idChunk := NewColumnChunkNoNulls("id", ids)
	priceChunk, _ := NewColumnChunk("price", prices, priceNulls)
	cityChunk := NewColumnChunkNoNulls("city", cities)
	activeChunk := NewColumnChunkNoNulls("active", active)

	rg, err := NewRowGroup(idChunk, priceChunk, cityChunk, activeChunk)
	if err != nil {
		t.Fatal(err)
	}
	return rg
}

func TestRowGroupBasic(t *testing.T) {
	rg := makeTestRowGroup(t, 10000)

	if rg.RowCount != 10000 {
		t.Fatalf("expected 10000 rows, got %d", rg.RowCount)
	}
	if rg.NumColumns() != 4 {
		t.Fatalf("expected 4 columns, got %d", rg.NumColumns())
	}
}

func TestRowGroupColumnAccess(t *testing.T) {
	rg := makeTestRowGroup(t, 100)

	// By index
	col0 := rg.Column(0)
	if col0.Name != "id" {
		t.Fatalf("expected column 0 = 'id', got '%s'", col0.Name)
	}

	// By name
	city := rg.ColumnByName("city")
	if city == nil {
		t.Fatal("expected to find 'city' column")
	}
	if city.ColType != TypeString {
		t.Fatalf("expected String type, got %v", city.ColType)
	}

	// Missing column
	missing := rg.ColumnByName("nonexistent")
	if missing != nil {
		t.Fatal("expected nil for missing column")
	}
}

func TestRowGroupSchema(t *testing.T) {
	rg := makeTestRowGroup(t, 10)
	schema := rg.Schema()

	expectedNames := []string{"id", "price", "city", "active"}
	expectedTypes := []ColumnType{TypeInt64, TypeFloat64, TypeString, TypeBool}

	for i, name := range expectedNames {
		if schema.Names[i] != name {
			t.Fatalf("column %d: expected name '%s', got '%s'", i, name, schema.Names[i])
		}
		if schema.Types[i] != expectedTypes[i] {
			t.Fatalf("column %d: expected type %v, got %v", i, expectedTypes[i], schema.Types[i])
		}
	}
}

func TestRowGroupTotalNulls(t *testing.T) {
	rg := makeTestRowGroup(t, 100)

	// Only price column has nulls (every 7th row)
	totalNulls := rg.TotalNulls()
	expectedNulls := (100 + 6) / 7 // ceil(100/7) = 15
	if totalNulls != expectedNulls {
		t.Fatalf("expected %d total nulls, got %d", expectedNulls, totalNulls)
	}
}

func TestRowGroupMismatchedLengths(t *testing.T) {
	col1 := NewColumnChunkNoNulls("a", NewInt64ColumnFromSlice([]int64{1, 2, 3}))
	col2 := NewColumnChunkNoNulls("b", NewInt64ColumnFromSlice([]int64{1, 2})) // different length!

	_, err := NewRowGroup(col1, col2)
	if err == nil {
		t.Fatal("expected error for mismatched column lengths")
	}
}

func TestRowGroupEmpty(t *testing.T) {
	_, err := NewRowGroup()
	if err == nil {
		t.Fatal("expected error for empty row group")
	}
}
