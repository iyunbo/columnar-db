package storage

// NullBitmap tracks which rows in a column are NULL using bit-packing.
//
// Each bit represents one row: 1 = null, 0 = not null.
// Bit-packed: 8 rows per byte, bit 0 (LSB) = first row in the byte.
//
// Why bit-packing?
//   - 1M rows = 125 KB (bit-packed) vs 1 MB ([]bool) — 8× less memory
//   - Compact on disk: null info for a column chunk is tiny
//   - Bitwise operations for combining filters (AND/OR) are extremely fast
//
// Layout example (byte 0):
//   bit 0 (LSB) = row 0
//   bit 1       = row 1
//   ...
//   bit 7 (MSB) = row 7
type NullBitmap struct {
	data []byte // bit-packed: 1 bit per row
	len  int    // total number of rows tracked
}

// NewNullBitmap creates a bitmap for n rows, all initially non-null.
func NewNullBitmap(n int) *NullBitmap {
	numBytes := (n + 7) / 8 // ceiling division
	return &NullBitmap{
		data: make([]byte, numBytes),
		len:  n,
	}
}

// SetNull marks row i as NULL.
func (b *NullBitmap) SetNull(i int) {
	byteIdx := i / 8
	bitIdx := uint(i % 8)
	b.data[byteIdx] |= 1 << bitIdx
}

// Reset zeroes the bitmap in place, marking all tracked rows as NOT NULL.
// Retains the underlying byte slice for reuse — the allocation-free path
// for operators that recycle bitmaps across batches.
func (b *NullBitmap) Reset() {
	for i := range b.data {
		b.data[i] = 0
	}
}

// ClearNull marks row i as NOT NULL.
func (b *NullBitmap) ClearNull(i int) {
	byteIdx := i / 8
	bitIdx := uint(i % 8)
	b.data[byteIdx] &^= 1 << bitIdx // AND NOT: clear the bit
}

// IsNull returns true if row i is NULL.
func (b *NullBitmap) IsNull(i int) bool {
	byteIdx := i / 8
	bitIdx := uint(i % 8)
	return b.data[byteIdx]&(1<<bitIdx) != 0
}

// NullCount returns the total number of NULL values.
//
// Uses popcount (Hamming weight) on each byte — efficient for
// sparse and dense null patterns alike.
func (b *NullBitmap) NullCount() int {
	count := 0
	for _, byt := range b.data {
		count += popcount(byt)
	}
	return count
}

// Len returns the total number of rows tracked by this bitmap.
func (b *NullBitmap) Len() int {
	return b.len
}

// Bytes returns the raw bitmap bytes (for serialization).
func (b *NullBitmap) Bytes() []byte {
	return b.data
}

// HasNulls returns true if any value is null.
func (b *NullBitmap) HasNulls() bool {
	for _, byt := range b.data {
		if byt != 0 {
			return true
		}
	}
	return false
}

// popcount returns the number of set bits in a byte.
// (Hamming weight / Brian Kernighan's algorithm)
func popcount(x byte) int {
	count := 0
	for x != 0 {
		x &= x - 1 // clear lowest set bit
		count++
	}
	return count
}
