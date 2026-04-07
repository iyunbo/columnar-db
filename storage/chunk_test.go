package storage

import (
	"testing"
)

func TestColumnChunkInt64(t *testing.T) {
	col := NewInt64Column()
	for i := range 1000 {
		col.Append(int64(i * 2))
	}

	nulls := NewNullBitmap(1000)
	nulls.SetNull(0)
	nulls.SetNull(500)
	nulls.SetNull(999)

	chunk, err := NewColumnChunk("age", col, nulls)
	if err != nil {
		t.Fatal(err)
	}

	if chunk.Name != "age" {
		t.Fatalf("expected name 'age', got '%s'", chunk.Name)
	}
	if chunk.ColType != TypeInt64 {
		t.Fatalf("expected TypeInt64, got %v", chunk.ColType)
	}
	if chunk.Count != 1000 {
		t.Fatalf("expected count 1000, got %d", chunk.Count)
	}
	if chunk.NullCount() != 3 {
		t.Fatalf("expected 3 nulls, got %d", chunk.NullCount())
	}
}

func TestColumnChunkStats(t *testing.T) {
	col := NewInt64ColumnFromSlice([]int64{10, 3, 50, 7, 25})
	nulls := NewNullBitmap(5)
	nulls.SetNull(2) // 50 is null

	chunk, _ := NewColumnChunk("score", col, nulls)
	stats := chunk.Stats()

	if stats.Min.(int64) != 3 {
		t.Fatalf("expected min 3, got %v", stats.Min)
	}
	if stats.Max.(int64) != 25 {
		t.Fatalf("expected max 25 (50 is null), got %v", stats.Max)
	}
	if stats.NullCount != 1 {
		t.Fatalf("expected 1 null, got %d", stats.NullCount)
	}
}

func TestColumnChunkFloat64Stats(t *testing.T) {
	col := NewFloat64ColumnFromSlice([]float64{1.5, 3.7, 2.1, 9.9})
	chunk := NewColumnChunkNoNulls("price", col)
	stats := chunk.Stats()

	if stats.Min.(float64) != 1.5 {
		t.Fatalf("expected min 1.5, got %v", stats.Min)
	}
	if stats.Max.(float64) != 9.9 {
		t.Fatalf("expected max 9.9, got %v", stats.Max)
	}
}

func TestColumnChunkStringStats(t *testing.T) {
	col := NewStringColumnFromSlice([]string{"banana", "apple", "cherry"})
	chunk := NewColumnChunkNoNulls("fruit", col)
	stats := chunk.Stats()

	if stats.Min.(string) != "apple" {
		t.Fatalf("expected min 'apple', got %v", stats.Min)
	}
	if stats.Max.(string) != "cherry" {
		t.Fatalf("expected max 'cherry', got %v", stats.Max)
	}
}

func TestColumnChunkNoNulls(t *testing.T) {
	col := NewBoolColumnFromSlice([]bool{true, false, true})
	chunk := NewColumnChunkNoNulls("active", col)

	if chunk.NullCount() != 0 {
		t.Fatalf("expected 0 nulls, got %d", chunk.NullCount())
	}
	if chunk.Count != 3 {
		t.Fatalf("expected count 3, got %d", chunk.Count)
	}
}

func TestColumnChunkNullCountTracksBitmapMutation(t *testing.T) {
	// Regression guard for the NullCount foot-gun: earlier versions
	// cached NullCount as a field set at construction, which silently
	// drifted if a caller mutated the bitmap afterwards. NullCount is
	// now a method that reads the live bitmap, so post-construction
	// SetNull must be reflected.
	col := NewInt64ColumnFromSlice([]int64{1, 2, 3, 4, 5})
	nulls := NewNullBitmap(5)
	chunk, err := NewColumnChunk("x", col, nulls)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.NullCount() != 0 {
		t.Fatalf("initial NullCount = %d, want 0", chunk.NullCount())
	}

	// Mutate the bitmap through the chunk's field.
	chunk.Nulls.SetNull(1)
	chunk.Nulls.SetNull(3)

	if chunk.NullCount() != 2 {
		t.Errorf("after SetNull×2: NullCount = %d, want 2", chunk.NullCount())
	}

	// Stats() should also see the updated count.
	if chunk.Stats().NullCount != 2 {
		t.Errorf("Stats().NullCount = %d, want 2", chunk.Stats().NullCount)
	}
}

func TestColumnChunkLengthMismatch(t *testing.T) {
	col := NewInt64Column()
	col.Append(1)
	col.Append(2)

	nulls := NewNullBitmap(5) // mismatch!

	_, err := NewColumnChunk("bad", col, nulls)
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
}

func TestColumnChunkAllNullStats(t *testing.T) {
	col := NewInt64ColumnFromSlice([]int64{10, 20, 30})
	nulls := NewNullBitmap(3)
	nulls.SetNull(0)
	nulls.SetNull(1)
	nulls.SetNull(2)

	chunk, _ := NewColumnChunk("empty", col, nulls)
	stats := chunk.Stats()

	if stats.Min != nil || stats.Max != nil {
		t.Fatalf("expected nil min/max for all-null column, got %v/%v", stats.Min, stats.Max)
	}
}
