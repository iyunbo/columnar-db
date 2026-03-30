package storage

import (
	"math/rand"
	"testing"
)

func TestNullBitmapBasic(t *testing.T) {
	bm := NewNullBitmap(16)

	// All should be non-null initially
	for i := 0; i < 16; i++ {
		if bm.IsNull(i) {
			t.Fatalf("row %d should not be null initially", i)
		}
	}

	if bm.NullCount() != 0 {
		t.Fatalf("expected 0 nulls, got %d", bm.NullCount())
	}

	// Set some nulls
	bm.SetNull(0)
	bm.SetNull(5)
	bm.SetNull(15)

	if !bm.IsNull(0) {
		t.Fatal("row 0 should be null")
	}
	if !bm.IsNull(5) {
		t.Fatal("row 5 should be null")
	}
	if !bm.IsNull(15) {
		t.Fatal("row 15 should be null")
	}
	if bm.IsNull(1) {
		t.Fatal("row 1 should not be null")
	}

	if bm.NullCount() != 3 {
		t.Fatalf("expected 3 nulls, got %d", bm.NullCount())
	}
}

func TestNullBitmapClearNull(t *testing.T) {
	bm := NewNullBitmap(8)
	bm.SetNull(3)

	if !bm.IsNull(3) {
		t.Fatal("row 3 should be null after SetNull")
	}

	bm.ClearNull(3)
	if bm.IsNull(3) {
		t.Fatal("row 3 should not be null after ClearNull")
	}

	if bm.NullCount() != 0 {
		t.Fatalf("expected 0 nulls after clear, got %d", bm.NullCount())
	}
}

func TestNullBitmapLargeRandom(t *testing.T) {
	n := 10000
	bm := NewNullBitmap(n)

	// Set random nulls
	rng := rand.New(rand.NewSource(42))
	expected := make(map[int]bool)
	for i := 0; i < n/10; i++ { // 10% null rate
		idx := rng.Intn(n)
		bm.SetNull(idx)
		expected[idx] = true
	}

	// Verify all positions
	for i := 0; i < n; i++ {
		isNull := bm.IsNull(i)
		shouldBeNull := expected[i]
		if isNull != shouldBeNull {
			t.Fatalf("row %d: expected null=%v, got null=%v", i, shouldBeNull, isNull)
		}
	}

	// Verify null count
	if bm.NullCount() != len(expected) {
		t.Fatalf("expected %d nulls, got %d", len(expected), bm.NullCount())
	}
}

func TestNullBitmapHasNulls(t *testing.T) {
	bm := NewNullBitmap(100)
	if bm.HasNulls() {
		t.Fatal("should not have nulls initially")
	}

	bm.SetNull(50)
	if !bm.HasNulls() {
		t.Fatal("should have nulls after SetNull")
	}
}

func TestNullBitmapLen(t *testing.T) {
	bm := NewNullBitmap(1000)
	if bm.Len() != 1000 {
		t.Fatalf("expected len 1000, got %d", bm.Len())
	}
}

func TestNullBitmapBytesSize(t *testing.T) {
	// 1M rows should use 125KB (not 1MB)
	bm := NewNullBitmap(1_000_000)
	bytes := bm.Bytes()
	expectedBytes := (1_000_000 + 7) / 8 // 125000

	if len(bytes) != expectedBytes {
		t.Fatalf("expected %d bytes for 1M rows, got %d", expectedBytes, len(bytes))
	}

	// Verify: 8× less than []bool
	boolSize := 1_000_000 // 1 byte per bool
	ratio := float64(boolSize) / float64(len(bytes))
	t.Logf("Bitmap: %d bytes vs []bool: %d bytes (%.1f× smaller)", len(bytes), boolSize, ratio)

	if ratio < 7.9 {
		t.Fatalf("expected ~8× compression, got %.1f×", ratio)
	}
}

func TestNullBitmapEdgeCases(t *testing.T) {
	// Non-aligned size (not multiple of 8)
	bm := NewNullBitmap(13)
	bm.SetNull(12) // last row

	if !bm.IsNull(12) {
		t.Fatal("last row (12) should be null")
	}
	if bm.NullCount() != 1 {
		t.Fatalf("expected 1 null, got %d", bm.NullCount())
	}

	// Size 1
	bm1 := NewNullBitmap(1)
	bm1.SetNull(0)
	if !bm1.IsNull(0) {
		t.Fatal("single row should be null")
	}
}

func TestPopcount(t *testing.T) {
	tests := []struct {
		input byte
		want  int
	}{
		{0b00000000, 0},
		{0b00000001, 1},
		{0b11111111, 8},
		{0b10101010, 4},
		{0b01010101, 4},
		{0b11000011, 4},
	}
	for _, tt := range tests {
		if got := popcount(tt.input); got != tt.want {
			t.Errorf("popcount(%08b) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
