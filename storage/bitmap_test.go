package storage

import (
	"math/rand"
	"testing"
)

func TestNullBitmapBasic(t *testing.T) {
	bm := NewNullBitmap(16)

	// All should be non-null initially
	for i := range 16 {
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
	for i := range n {
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

func TestHasNullsInRange(t *testing.T) {
	// Build a 100-row bitmap with nulls at known positions, then query
	// every interesting [start, start+n) range. The masking edges
	// (start mid-byte, end mid-byte, single-byte range, range fully
	// inside one byte, range exactly aligned) all need coverage —
	// this is a hot-path bit-twiddler and "looks right" is not enough.
	bm := NewNullBitmap(100)
	nullPositions := []int{3, 17, 64, 65, 99}
	for _, p := range nullPositions {
		bm.SetNull(p)
	}

	cases := []struct {
		name  string
		start int
		n     int
		want  bool
	}{
		{"empty range n=0", 0, 0, false},
		{"n=0 mid-byte", 5, 0, false},

		// Aligned fast path (start%8==0, end%8==0)
		{"aligned [0,8) hits null at 3", 0, 8, true},
		{"aligned [8,16) clean", 8, 8, false},
		{"aligned [16,24) hits 17", 16, 8, true},
		{"aligned [24,64) clean", 24, 40, false},
		{"aligned [64,72) hits 64,65", 64, 8, true},

		// Single-byte range, both edges mid-byte
		{"single byte [0,3) excludes 3", 0, 3, false},
		{"single byte [0,4) includes 3", 0, 4, true},
		{"single byte [3,4) is exactly 3", 3, 1, true},
		{"single byte [4,8) excludes 3", 4, 4, false},
		{"single byte [17,18) is exactly 17", 17, 1, true},
		{"single byte [16,17) excludes 17", 16, 1, false},

		// Multi-byte, mid-byte start, byte-aligned end
		{"[3,16) starts on null", 3, 13, true},
		{"[4,16) excludes null at 3", 4, 12, false},
		{"[4,17) excludes both", 4, 13, false},
		{"[4,18) catches 17", 4, 14, true},

		// Multi-byte, byte-aligned start, mid-byte end
		{"[0,17) excludes 17, hits 3", 0, 17, true},
		{"[8,17) clean", 8, 9, false},
		{"[8,18) catches 17", 8, 10, true},

		// Multi-byte, both edges mid-byte, null only in middle byte
		{"[5,70) catches 17,64,65", 5, 65, true},
		{"[18,64) clean (gap between 17 and 64)", 18, 46, false},
		{"[18,65) catches 64", 18, 47, true},

		// Boundaries
		{"null exactly at start (mid-byte)", 17, 1, true},
		{"null exactly at end-1 (mid-byte)", 0, 18, true},
		{"null at end (excluded)", 0, 17, true},  // 3 is in [0,17), 17 is not
		{"position past last null", 18, 46, false},
		{"last bit, null at 99", 99, 1, true},
		{"last bit, [98,99) clean", 98, 1, false},
		{"last byte exactly aligned [96,100)", 96, 4, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bm.HasNullsInRange(tc.start, tc.n); got != tc.want {
				t.Errorf("HasNullsInRange(%d, %d) = %v, want %v",
					tc.start, tc.n, got, tc.want)
			}
		})
	}
}

func TestHasNullsInRangeAgreesWithIsNull(t *testing.T) {
	// Differential test: for every [start, start+n) range over a
	// pseudo-random bitmap, HasNullsInRange must agree with the slow
	// reference (loop over IsNull). This catches anything the table
	// above missed — particularly stride-of-1 boundary errors.
	rng := rand.New(rand.NewSource(1))
	const n = 200
	bm := NewNullBitmap(n)
	for i := range n {
		if rng.Intn(5) == 0 {
			bm.SetNull(i)
		}
	}
	reference := func(start, k int) bool {
		for i := 0; i < k; i++ {
			if bm.IsNull(start + i) {
				return true
			}
		}
		return false
	}
	for start := 0; start <= n; start++ {
		for k := 0; start+k <= n; k++ {
			want := reference(start, k)
			got := bm.HasNullsInRange(start, k)
			if got != want {
				t.Fatalf("HasNullsInRange(%d, %d) = %v, want %v",
					start, k, got, want)
			}
		}
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
