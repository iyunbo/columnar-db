package encoding

import (
	"fmt"
	"math"
	"testing"
)

// --- Int64 RLE round-trip tests ---

func TestRLEInt64RoundTrip(t *testing.T) {
	enc := NewRLEEncoder()

	tests := []struct {
		name string
		data []int64
	}{
		{"all_repeated", []int64{1, 1, 1, 1, 1}},
		{"all_unique", []int64{1, 2, 3, 4, 5}},
		{"mixed_runs", []int64{1, 1, 1, 2, 2, 3, 1, 1}},
		{"empty", []int64{}},
		{"single", []int64{42}},
		{"negative", []int64{-1, -1, -1, 0, 0, 1, 1, 1}},
		{"extremes", []int64{math.MaxInt64, math.MaxInt64, math.MinInt64, math.MinInt64}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := RLEDecodeInt64(encoded, len(tt.data))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			if len(decoded) != len(tt.data) {
				t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(tt.data))
			}
			for i, v := range decoded {
				if v != tt.data[i] {
					t.Errorf("index %d: got %d, want %d", i, v, tt.data[i])
				}
			}
		})
	}
}

// --- Float64 RLE round-trip tests ---

func TestRLEFloat64RoundTrip(t *testing.T) {
	enc := NewRLEEncoder()

	tests := []struct {
		name string
		data []float64
	}{
		{"all_repeated", []float64{3.14, 3.14, 3.14, 3.14}},
		{"all_unique", []float64{1.1, 2.2, 3.3, 4.4}},
		{"mixed_runs", []float64{1.0, 1.0, 2.0, 2.0, 2.0, 3.0}},
		{"empty", []float64{}},
		{"single", []float64{2.718}},
		{"special", []float64{math.Inf(1), math.Inf(1), math.Inf(-1), 0.0, 0.0}},
		{"nan", []float64{math.NaN(), math.NaN()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := RLEDecodeFloat64(encoded, len(tt.data))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			if len(decoded) != len(tt.data) {
				t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(tt.data))
			}
			for i, v := range decoded {
				if math.IsNaN(tt.data[i]) {
					if !math.IsNaN(v) {
						t.Errorf("index %d: expected NaN, got %f", i, v)
					}
				} else if v != tt.data[i] {
					t.Errorf("index %d: got %f, want %f", i, v, tt.data[i])
				}
			}
		})
	}
}

// --- String RLE round-trip tests ---

func TestRLEStringRoundTrip(t *testing.T) {
	enc := NewRLEEncoder()

	tests := []struct {
		name string
		data []string
	}{
		{"all_repeated", []string{"US", "US", "US", "US", "US"}},
		{"all_unique", []string{"a", "b", "c", "d", "e"}},
		{"mixed_runs", []string{"US", "US", "FR", "FR", "FR", "CN", "US", "US"}},
		{"empty", []string{}},
		{"single", []string{"hello"}},
		{"empty_strings", []string{"", "", "", "a", "a", ""}},
		{"unicode", []string{"hello", "hello", "nihao", "nihao"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := RLEDecodeString(encoded, len(tt.data))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			if len(decoded) != len(tt.data) {
				t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(tt.data))
			}
			for i, v := range decoded {
				if v != tt.data[i] {
					t.Errorf("index %d: got %q, want %q", i, v, tt.data[i])
				}
			}
		})
	}
}

// --- Bool RLE round-trip tests ---

func TestRLEBoolRoundTrip(t *testing.T) {
	enc := NewRLEEncoder()

	tests := []struct {
		name string
		data []bool
	}{
		{"all_repeated", []bool{true, true, true, true, true}},
		{"alternating", []bool{true, false, true, false, true}},
		{"mixed_runs", []bool{true, true, true, false, false, true}},
		{"empty", []bool{}},
		{"single_true", []bool{true}},
		{"single_false", []bool{false}},
		{"all_false", []bool{false, false, false, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := RLEDecodeBool(encoded, len(tt.data))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			if len(decoded) != len(tt.data) {
				t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(tt.data))
			}
			for i, v := range decoded {
				if v != tt.data[i] {
					t.Errorf("index %d: got %v, want %v", i, v, tt.data[i])
				}
			}
		})
	}
}

// --- Run count verification ---

func TestRLEInt64RunCounts(t *testing.T) {
	enc := NewRLEEncoder()

	// [1,1,1,2,2,3,1,1] should produce 4 runs: (1,3),(2,2),(3,1),(1,2)
	data := []int64{1, 1, 1, 2, 2, 3, 1, 1}
	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// 4 runs * 12 bytes (8 value + 4 count) = 48 bytes
	expectedSize := 4 * 12
	if len(encoded) != expectedSize {
		t.Errorf("expected %d bytes for 4 runs, got %d", expectedSize, len(encoded))
	}
}

func TestRLEInt64AllRepeatedSize(t *testing.T) {
	enc := NewRLEEncoder()

	// [1,1,1,1,1] should produce 1 run = 12 bytes
	data := []int64{1, 1, 1, 1, 1}
	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if len(encoded) != 12 {
		t.Errorf("expected 12 bytes for 1 run, got %d", len(encoded))
	}
}

// --- Error cases ---

func TestRLEEncodeUnsupportedType(t *testing.T) {
	enc := NewRLEEncoder()
	_, err := enc.Encode([]int32{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestRLEDecodeInt64TruncatedData(t *testing.T) {
	_, err := RLEDecodeInt64([]byte{1, 2, 3}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestRLEDecodeFloat64TruncatedData(t *testing.T) {
	_, err := RLEDecodeFloat64([]byte{1, 2, 3}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestRLEDecodeStringTruncatedData(t *testing.T) {
	_, err := RLEDecodeString([]byte{1, 2}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestRLEDecodeBoolTruncatedData(t *testing.T) {
	_, err := RLEDecodeBool([]byte{1, 2}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestRLEEncoderType(t *testing.T) {
	enc := NewRLEEncoder()
	if enc.Type() != RLE {
		t.Errorf("expected RLE, got %v", enc.Type())
	}
}

// --- Large data test ---

func TestRLEInt64LargeData(t *testing.T) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	const uniqueValues = 100

	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i / (n / uniqueValues)) // ~100 unique values, each repeated ~10000 times
	}

	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := RLEDecodeInt64(encoded, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Fatalf("mismatch at index %d: got %d, want %d", i, v, data[i])
		}
	}
}

// --- Compression ratio test ---

func TestRLECompressionRatioHighRepetition(t *testing.T) {
	rleEnc := NewRLEEncoder()
	plainEnc := NewPlainEncoder()
	const n = 1_000_000

	// All same value: RLE should be tiny (1 run = 12 bytes).
	data := make([]int64, n)
	for i := range data {
		data[i] = 42
	}

	rleEncoded, err := rleEnc.Encode(data)
	if err != nil {
		t.Fatalf("rle encode: %v", err)
	}
	plainEncoded, err := plainEnc.Encode(data)
	if err != nil {
		t.Fatalf("plain encode: %v", err)
	}

	rleSize := len(rleEncoded)
	plainSize := len(plainEncoded)
	ratio := float64(rleSize) / float64(plainSize)

	t.Logf("1M identical int64s: plain=%d bytes, rle=%d bytes, ratio=%.6f", plainSize, rleSize, ratio)

	// RLE should be 12 bytes vs plain's 8MB — ratio < 0.001.
	if ratio > 0.001 {
		t.Errorf("compression ratio too high for identical values: %.4f (expected < 0.001)", ratio)
	}

	// Verify round-trip.
	decoded, err := RLEDecodeInt64(rleEncoded, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, v := range decoded {
		if v != 42 {
			t.Fatalf("mismatch at index %d: got %d, want 42", i, v)
		}
	}
}

func TestRLECompressionRatioLowRepetition(t *testing.T) {
	rleEnc := NewRLEEncoder()
	plainEnc := NewPlainEncoder()

	// All unique values: RLE should be WORSE than plain.
	data := make([]int64, 1000)
	for i := range data {
		data[i] = int64(i)
	}

	rleEncoded, _ := rleEnc.Encode(data)
	plainEncoded, _ := plainEnc.Encode(data)

	rleSize := len(rleEncoded)
	plainSize := len(plainEncoded)
	ratio := float64(rleSize) / float64(plainSize)

	t.Logf("1K unique int64s: plain=%d bytes, rle=%d bytes, ratio=%.2f", plainSize, rleSize, ratio)

	// RLE should be 1.5x plain (12 vs 8 bytes per value).
	if ratio < 1.0 {
		t.Errorf("RLE should be worse than plain for unique values, but ratio=%.2f", ratio)
	}
}

func TestRLEStringCompressionRatio(t *testing.T) {
	rleEnc := NewRLEEncoder()
	plainEnc := NewPlainEncoder()
	const n = 100_000

	// Same string repeated: should compress extremely well.
	data := make([]string, n)
	for i := range data {
		data[i] = "United States"
	}

	rleEncoded, _ := rleEnc.Encode(data)
	plainEncoded, _ := plainEnc.Encode(data)

	ratio := float64(len(rleEncoded)) / float64(len(plainEncoded))
	t.Logf("100K identical strings: plain=%d bytes, rle=%d bytes, ratio=%.6f", len(plainEncoded), len(rleEncoded), ratio)

	if ratio > 0.001 {
		t.Errorf("string compression ratio too high: %.4f", ratio)
	}
}

// --- Benchmarks ---

func BenchmarkRLEEncodeInt64_1M_HighRepetition(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i / 10000) // ~100 unique runs
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkRLEDecodeInt64_1M_HighRepetition(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i / 10000)
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		RLEDecodeInt64(encoded, n)
	}
}

func BenchmarkRLEEncodeInt64_1M_LowRepetition(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i) // All unique
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkRLEDecodeInt64_1M_LowRepetition(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i)
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		RLEDecodeInt64(encoded, n)
	}
}

func BenchmarkRLEEncodeString_1M_HighRepetition(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	values := []string{"US", "FR", "CN", "JP", "DE", "GB", "BR", "IN", "KR", "AU"}
	data := make([]string, n)
	for i := range data {
		data[i] = values[i/(n/len(values))]
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkRLEDecodeString_1M_HighRepetition(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	values := []string{"US", "FR", "CN", "JP", "DE", "GB", "BR", "IN", "KR", "AU"}
	data := make([]string, n)
	for i := range data {
		data[i] = values[i/(n/len(values))]
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		RLEDecodeString(encoded, n)
	}
}

func BenchmarkRLEEncodeBool_1M(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	data := make([]bool, n)
	for i := range data {
		data[i] = i/1000%2 == 0 // Runs of 1000
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkRLEDecodeBool_1M(b *testing.B) {
	enc := NewRLEEncoder()
	const n = 1_000_000
	data := make([]bool, n)
	for i := range data {
		data[i] = i/1000%2 == 0
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		RLEDecodeBool(encoded, n)
	}
}

// --- Verify interface compliance ---

func TestRLEImplementsEncoder(t *testing.T) {
	var _ Encoder = (*RLEEncoder)(nil)
}

// --- Comparison log for PR description ---

func TestRLEVsPlainCompressionSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compression summary in short mode")
	}

	rleEnc := NewRLEEncoder()
	plainEnc := NewPlainEncoder()

	// Scenario 1: 1M identical int64
	int64Same := make([]int64, 1_000_000)
	for i := range int64Same {
		int64Same[i] = 42
	}
	rle1, _ := rleEnc.Encode(int64Same)
	plain1, _ := plainEnc.Encode(int64Same)

	// Scenario 2: 1M int64 with ~100 runs
	int64Runs := make([]int64, 1_000_000)
	for i := range int64Runs {
		int64Runs[i] = int64(i / 10000)
	}
	rle2, _ := rleEnc.Encode(int64Runs)
	plain2, _ := plainEnc.Encode(int64Runs)

	// Scenario 3: 1M unique int64 (worst case)
	int64Unique := make([]int64, 1_000_000)
	for i := range int64Unique {
		int64Unique[i] = int64(i)
	}
	rle3, _ := rleEnc.Encode(int64Unique)
	plain3, _ := plainEnc.Encode(int64Unique)

	// Scenario 4: 100K identical strings
	strSame := make([]string, 100_000)
	for i := range strSame {
		strSame[i] = "United States"
	}
	rle4, _ := rleEnc.Encode(strSame)
	plain4, _ := plainEnc.Encode(strSame)

	fmt.Println("\n=== RLE vs Plain Compression Ratios ===")
	fmt.Printf("%-35s  %10s  %10s  %8s\n", "Scenario", "Plain", "RLE", "Ratio")
	fmt.Printf("%-35s  %10s  %10s  %8s\n", "---", "---", "---", "---")
	fmt.Printf("%-35s  %10d  %10d  %7.4f%%\n", "1M identical int64", len(plain1), len(rle1), float64(len(rle1))/float64(len(plain1))*100)
	fmt.Printf("%-35s  %10d  %10d  %7.2f%%\n", "1M int64, ~100 sorted runs", len(plain2), len(rle2), float64(len(rle2))/float64(len(plain2))*100)
	fmt.Printf("%-35s  %10d  %10d  %7.2f%%\n", "1M unique int64 (worst case)", len(plain3), len(rle3), float64(len(rle3))/float64(len(plain3))*100)
	fmt.Printf("%-35s  %10d  %10d  %7.4f%%\n", "100K identical strings", len(plain4), len(rle4), float64(len(rle4))/float64(len(plain4))*100)
	fmt.Println()
}
