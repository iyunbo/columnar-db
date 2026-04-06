package encoding

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"
)

func TestLZ4RoundTrip(t *testing.T) {
	comp := NewLZ4Compressor()

	// Generate random bytes.
	original := make([]byte, 4096)
	if _, err := rand.Read(original); err != nil {
		t.Fatal(err)
	}

	compressed, err := comp.Compress(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	decompressed, err := comp.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4HighlyCompressible(t *testing.T) {
	comp := NewLZ4Compressor()

	// 1M zeros — should compress dramatically.
	original := make([]byte, 1_000_000)

	compressed, err := comp.Compress(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	ratio := float64(len(compressed)) / float64(len(original))
	t.Logf("1M zeros: %d → %d bytes (%.2f%%)", len(original), len(compressed), ratio*100)

	if ratio > 0.01 {
		t.Errorf("expected >100x compression for zeros, got ratio %.4f", ratio)
	}

	decompressed, err := comp.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(original, decompressed) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4Incompressible(t *testing.T) {
	comp := NewLZ4Compressor()

	// Random bytes are incompressible.
	original := make([]byte, 4096)
	if _, err := rand.Read(original); err != nil {
		t.Fatal(err)
	}

	compressed, err := comp.Compress(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	// Should fall back to uncompressed storage (8-byte header + original).
	// compressed_size field should be 0.
	t.Logf("random 4KB: %d → %d bytes", len(original), len(compressed))

	decompressed, err := comp.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(original, decompressed) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4EmptyInput(t *testing.T) {
	comp := NewLZ4Compressor()

	compressed, err := comp.Compress([]byte{})
	if err != nil {
		t.Fatalf("compress empty: %v", err)
	}

	decompressed, err := comp.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress empty: %v", err)
	}

	if len(decompressed) != 0 {
		t.Fatalf("expected empty result, got %d bytes", len(decompressed))
	}
}

func TestLZ4SmallInput(t *testing.T) {
	comp := NewLZ4Compressor()

	original := []byte("helloworld") // 10 bytes

	compressed, err := comp.Compress(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	decompressed, err := comp.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4EncodingPipeline(t *testing.T) {
	// Delta-encode sequential int64, then LZ4 compress, then reverse.
	enc := NewDeltaEncoder()
	comp := NewLZ4Compressor()

	count := 10000
	original := make([]int64, count)
	for i := range original {
		original[i] = int64(1000 + i*3) // monotonic, step=3
	}

	compressed, err := EncodeThenCompress(enc, comp, original)
	if err != nil {
		t.Fatalf("encode+compress: %v", err)
	}

	decoded, err := DecompressThenDecode(enc, comp, compressed, count)
	if err != nil {
		t.Fatalf("decompress+decode: %v", err)
	}

	result := decoded.([]int64)
	if len(result) != count {
		t.Fatalf("expected %d values, got %d", count, len(result))
	}
	for i, v := range result {
		if v != original[i] {
			t.Fatalf("mismatch at index %d: got %d, want %d", i, v, original[i])
		}
	}

	rawSize := count * 8
	t.Logf("pipeline: %d int64 values, raw=%d bytes, delta+LZ4=%d bytes (%.2f%%)",
		count, rawSize, len(compressed), float64(len(compressed))/float64(rawSize)*100)
}

func TestLZ4CompressionRatioComparison(t *testing.T) {
	comp := NewLZ4Compressor()

	// Generate test data: sequential int64 (good for delta).
	count := 100_000
	data := make([]int64, count)
	for i := range data {
		data[i] = int64(i)
	}
	rawSize := count * 8

	// Plain encoding + LZ4.
	plainEnc := NewPlainEncoder()
	plainBytes, _ := plainEnc.Encode(data)
	plainLZ4, _ := comp.Compress(plainBytes)
	t.Logf("Plain+LZ4: raw=%d, encoded=%d, compressed=%d (%.2f%%)",
		rawSize, len(plainBytes), len(plainLZ4), float64(len(plainLZ4))/float64(rawSize)*100)

	// RLE encoding + LZ4 (RLE won't help much here — no repeats).
	rleEnc := NewRLEEncoder()
	rleBytes, _ := rleEnc.Encode(data)
	rleLZ4, _ := comp.Compress(rleBytes)
	t.Logf("RLE+LZ4:   raw=%d, encoded=%d, compressed=%d (%.2f%%)",
		rawSize, len(rleBytes), len(rleLZ4), float64(len(rleLZ4))/float64(rawSize)*100)

	// Delta encoding + LZ4 (delta should be amazing here).
	deltaEnc := NewDeltaEncoder()
	deltaBytes, _ := deltaEnc.Encode(data)
	deltaLZ4, _ := comp.Compress(deltaBytes)
	t.Logf("Delta+LZ4: raw=%d, encoded=%d, compressed=%d (%.2f%%)",
		rawSize, len(deltaBytes), len(deltaLZ4), float64(len(deltaLZ4))/float64(rawSize)*100)

	// Delta+LZ4 should be the smallest.
	if len(deltaLZ4) >= len(plainLZ4) {
		t.Errorf("expected delta+LZ4 (%d) < plain+LZ4 (%d) for sequential data",
			len(deltaLZ4), len(plainLZ4))
	}
}

func TestLZ4BigTest(t *testing.T) {
	// The big test: 1M sequential int64.
	// Plain = 8MB. Delta+LZ4 should be tiny.
	comp := NewLZ4Compressor()
	enc := NewDeltaEncoder()

	count := 1_000_000
	data := make([]int64, count)
	for i := range data {
		data[i] = int64(i)
	}

	rawSize := count * 8 // 8MB

	compressed, err := EncodeThenCompress(enc, comp, data)
	if err != nil {
		t.Fatalf("encode+compress: %v", err)
	}

	ratio := float64(len(compressed)) / float64(rawSize)
	t.Logf("1M sequential int64: raw=%d bytes (%.1fMB), delta+LZ4=%d bytes (%.2f%%)",
		rawSize, float64(rawSize)/1e6, len(compressed), ratio*100)

	// Should achieve >100x compression.
	if ratio > 0.01 {
		t.Errorf("expected >100x compression for sequential int64, got %.2fx", 1/ratio)
	}

	// Verify round-trip.
	decoded, err := DecompressThenDecode(enc, comp, compressed, count)
	if err != nil {
		t.Fatalf("decompress+decode: %v", err)
	}
	result := decoded.([]int64)
	if len(result) != count {
		t.Fatalf("expected %d values, got %d", count, len(result))
	}
	if result[0] != 0 || result[count-1] != int64(count-1) {
		t.Fatal("value mismatch after round-trip")
	}
}

func TestLZ4DecompressErrors(t *testing.T) {
	comp := NewLZ4Compressor()

	// Too short.
	_, err := comp.Decompress([]byte{0, 1, 2})
	if err == nil {
		t.Error("expected error for short input")
	}

	// Truncated payload.
	buf := make([]byte, 8)
	buf[0] = 100 // original_size = 100
	buf[4] = 50  // compressed_size = 50
	_, err = comp.Decompress(buf)
	if err == nil {
		t.Error("expected error for truncated compressed payload")
	}

	// Truncated uncompressed payload.
	buf2 := make([]byte, 8)
	buf2[0] = 100 // original_size = 100
	// compressed_size = 0 (uncompressed flag), but no payload
	_, err = comp.Decompress(buf2)
	if err == nil {
		t.Error("expected error for truncated uncompressed payload")
	}
}

func TestCompressorName(t *testing.T) {
	comp := NewLZ4Compressor()
	if comp.Name() != "LZ4" {
		t.Errorf("expected name 'LZ4', got '%s'", comp.Name())
	}
}

// --- Benchmarks ---

func BenchmarkLZ4Compress(b *testing.B) {
	comp := NewLZ4Compressor()

	for _, size := range []int{1_000_000, 10_000_000} {
		data := make([]byte, size)
		// Semi-compressible: repeating pattern.
		for i := range data {
			data[i] = byte(i % 256)
		}

		b.Run(fmt.Sprintf("%dMB", size/1_000_000), func(b *testing.B) {
			b.SetBytes(int64(size))
			for b.Loop() {
				_, err := comp.Compress(data)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLZ4Decompress(b *testing.B) {
	comp := NewLZ4Compressor()

	for _, size := range []int{1_000_000, 10_000_000} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i % 256)
		}

		compressed, err := comp.Compress(data)
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("%dMB", size/1_000_000), func(b *testing.B) {
			b.SetBytes(int64(size))
			for b.Loop() {
				_, err := comp.Decompress(compressed)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
