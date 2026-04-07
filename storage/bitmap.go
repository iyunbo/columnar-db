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

// HasNullsInRange reports whether any bit in [start, start+n) is set.
// O(n/8) — only touches the bytes covering the requested slice, not the
// whole bitmap. This matters on the hot path: callers like
// Vector.CopyFromChunk run once per batch (1024 rows) over chunks that
// can be much larger (65536+ rows), and null-free data is HasNulls()'s
// worst case — it must scan every byte to confirm absence. The per-batch
// scan was O(chunk_size) instead of O(batch_size); this restores the
// expected scaling. (See docs/phase3-profiling.md for the full story —
// note that walltime barely moved, because the original 87%-of-CPU
// pprof attribution turned out to be sampling artifact, not real cost.)
func (b *NullBitmap) HasNullsInRange(start, n int) bool {
	if n <= 0 {
		return false
	}
	end := start + n
	firstByte := start / 8
	lastByte := (end - 1) / 8

	// Fast path: range fits inside whole bytes — no masking needed.
	if start%8 == 0 && end%8 == 0 {
		for i := firstByte; i <= lastByte; i++ {
			if b.data[i] != 0 {
				return true
			}
		}
		return false
	}

	// First byte: mask off bits before `start`.
	firstMask := byte(0xFF) << (start % 8)
	if firstByte == lastByte {
		// Single-byte range: also mask off bits at/after `end`.
		lastMask := byte(0xFF) >> (8 - end%8)
		if end%8 == 0 {
			lastMask = 0xFF
		}
		return b.data[firstByte]&firstMask&lastMask != 0
	}
	if b.data[firstByte]&firstMask != 0 {
		return true
	}
	// Middle whole bytes.
	for i := firstByte + 1; i < lastByte; i++ {
		if b.data[i] != 0 {
			return true
		}
	}
	// Last byte: mask off bits at/after `end`.
	lastMask := byte(0xFF) >> (8 - end%8)
	if end%8 == 0 {
		lastMask = 0xFF
	}
	return b.data[lastByte]&lastMask != 0
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
