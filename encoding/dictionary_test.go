package encoding

import (
	"fmt"
	"math/rand"
	"testing"
)

// --- Low cardinality strings ---

func TestDictEncode_LowCardinalityStrings(t *testing.T) {
	data := []string{"Paris", "Tokyo", "Paris", "London", "Tokyo", "Paris"}
	enc := NewDictionaryEncoder()

	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeString(encoded, len(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Errorf("index %d: got %q, want %q", i, v, data[i])
		}
	}

	// Verify compression: 3 unique values should mean small dict.
	plainEnc := NewPlainEncoder()
	plainBytes, _ := plainEnc.Encode(data)
	t.Logf("plain=%d bytes, dict=%d bytes, ratio=%.2f", len(plainBytes), len(encoded), float64(len(plainBytes))/float64(len(encoded)))
}

// --- All same value ---

func TestDictEncode_AllSameValue(t *testing.T) {
	n := 1_000_000
	data := make([]string, n)
	for i := range data {
		data[i] = "hello"
	}

	enc := NewDictionaryEncoder()
	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeString(encoded, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i := range 10 {
		if decoded[i] != "hello" {
			t.Errorf("index %d: got %q, want %q", i, decoded[i], "hello")
		}
	}
	if decoded[n-1] != "hello" {
		t.Errorf("last: got %q, want %q", decoded[n-1], "hello")
	}

	// Dict should have exactly 1 entry.
	// 4 (dict_size) + 4+5 (one string entry) + 4 (num_values) + n*4 (indices)
	expectedSize := 4 + 9 + 4 + n*4
	if len(encoded) != expectedSize {
		t.Errorf("encoded size: got %d, want %d", len(encoded), expectedSize)
	}
}

// --- All unique (worst case) ---

func TestDictEncode_AllUnique(t *testing.T) {
	n := 1000
	data := make([]string, n)
	for i := range data {
		data[i] = fmt.Sprintf("unique_%d", i)
	}

	enc := NewDictionaryEncoder()
	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeString(encoded, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Errorf("index %d: got %q, want %q", i, v, data[i])
		}
	}

	// Worst case: dict encoding adds overhead (dict_size + num_values + indices).
	plainEnc := NewPlainEncoder()
	plainBytes, _ := plainEnc.Encode(data)
	t.Logf("all-unique: plain=%d bytes, dict=%d bytes (dict is larger, as expected)", len(plainBytes), len(encoded))
	if len(encoded) <= len(plainBytes) {
		t.Logf("warning: dict unexpectedly smaller than plain for all-unique data")
	}
}

// --- Empty slice ---

func TestDictEncode_Empty(t *testing.T) {
	tests := []struct {
		name string
		data any
	}{
		{"int64", []int64{}},
		{"float64", []float64{}},
		{"string", []string{}},
		{"bool", []bool{}},
	}

	enc := NewDictionaryEncoder()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if encoded == nil {
				t.Fatal("expected non-nil encoded bytes for empty slice")
			}
		})
	}
}

// --- All 4 types round-trip ---

func TestDictEncode_Int64RoundTrip(t *testing.T) {
	data := []int64{10, 20, 10, 30, 20, 10, 30, 30}
	enc := NewDictionaryEncoder()

	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeInt64(encoded, len(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Errorf("index %d: got %d, want %d", i, v, data[i])
		}
	}
}

func TestDictEncode_Float64RoundTrip(t *testing.T) {
	data := []float64{1.1, 2.2, 1.1, 3.3, 2.2, 1.1}
	enc := NewDictionaryEncoder()

	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeFloat64(encoded, len(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Errorf("index %d: got %f, want %f", i, v, data[i])
		}
	}
}

func TestDictEncode_StringRoundTrip(t *testing.T) {
	data := []string{"alpha", "beta", "gamma", "alpha", "beta"}
	enc := NewDictionaryEncoder()

	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeString(encoded, len(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Errorf("index %d: got %q, want %q", i, v, data[i])
		}
	}
}

func TestDictEncode_BoolRoundTrip(t *testing.T) {
	data := []bool{true, false, true, true, false, false, true}
	enc := NewDictionaryEncoder()

	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DictDecodeBool(encoded, len(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i, v := range decoded {
		if v != data[i] {
			t.Errorf("index %d: got %v, want %v", i, v, data[i])
		}
	}
}

// --- Large data: 1M values, 100 unique strings → ~10× compression ---

func TestDictEncode_LargeData_CompressionRatio(t *testing.T) {
	n := 1_000_000
	cardinality := 100

	// Generate 100 unique city-like strings.
	uniqueValues := make([]string, cardinality)
	for i := range cardinality {
		uniqueValues[i] = fmt.Sprintf("city_%03d_name", i)
	}

	rng := rand.New(rand.NewSource(42))
	data := make([]string, n)
	for i := range data {
		data[i] = uniqueValues[rng.Intn(cardinality)]
	}

	// Encode with both plain and dictionary.
	plainEnc := NewPlainEncoder()
	plainBytes, err := plainEnc.Encode(data)
	if err != nil {
		t.Fatalf("plain encode: %v", err)
	}

	dictEnc := NewDictionaryEncoder()
	dictBytes, err := dictEnc.Encode(data)
	if err != nil {
		t.Fatalf("dict encode: %v", err)
	}

	ratio := float64(len(plainBytes)) / float64(len(dictBytes))
	t.Logf("1M values, 100 unique strings:")
	t.Logf("  plain: %d bytes (%.1f MB)", len(plainBytes), float64(len(plainBytes))/1e6)
	t.Logf("  dict:  %d bytes (%.1f MB)", len(dictBytes), float64(len(dictBytes))/1e6)
	t.Logf("  compression ratio: %.1f×", ratio)

	// Expect at least 3× compression (conservative; typical is ~4× for 13-char strings).
	// Plain: n*(4+13) = 17M. Dict: 100*(4+13) + n*4 = ~4M. Ratio ~4.2×.
	if ratio < 3.0 {
		t.Errorf("expected at least 3× compression, got %.1f×", ratio)
	}

	// Verify round-trip correctness.
	decoded, err := DictDecodeString(dictBytes, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range 100 {
		if decoded[i] != data[i] {
			t.Errorf("index %d: got %q, want %q", i, decoded[i], data[i])
		}
	}
	if decoded[n-1] != data[n-1] {
		t.Errorf("last value mismatch")
	}
}

// --- Compression ratio comparison: dict vs plain vs RLE ---

func TestDictEncode_ComparisonWithOtherEncodings(t *testing.T) {
	// Scenario: sorted low-cardinality strings (best case for both RLE and dict).
	n := 100_000
	categories := []string{"active", "deleted", "inactive", "pending", "suspended"}
	data := make([]string, 0, n)
	for _, cat := range categories {
		for j := 0; j < n/len(categories); j++ {
			data = append(data, cat)
		}
	}

	plainEnc := NewPlainEncoder()
	rleEnc := NewRLEEncoder()
	dictEnc := NewDictionaryEncoder()

	plainBytes, _ := plainEnc.Encode(data)
	rleBytes, _ := rleEnc.Encode(data)
	dictBytes, _ := dictEnc.Encode(data)

	t.Logf("Sorted low-cardinality (%d values, %d unique):", len(data), len(categories))
	t.Logf("  plain: %d bytes", len(plainBytes))
	t.Logf("  RLE:   %d bytes (%.1f× vs plain)", len(rleBytes), float64(len(plainBytes))/float64(len(rleBytes)))
	t.Logf("  dict:  %d bytes (%.1f× vs plain)", len(dictBytes), float64(len(plainBytes))/float64(len(dictBytes)))

	// Scenario 2: random (unsorted) low-cardinality — dict wins, RLE doesn't help.
	rng := rand.New(rand.NewSource(42))
	shuffled := make([]string, len(data))
	copy(shuffled, data)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	plainBytes2, _ := plainEnc.Encode(shuffled)
	rleBytes2, _ := rleEnc.Encode(shuffled)
	dictBytes2, _ := dictEnc.Encode(shuffled)

	t.Logf("Shuffled low-cardinality (%d values, %d unique):", len(shuffled), len(categories))
	t.Logf("  plain: %d bytes", len(plainBytes2))
	t.Logf("  RLE:   %d bytes (%.1f× vs plain)", len(rleBytes2), float64(len(plainBytes2))/float64(len(rleBytes2)))
	t.Logf("  dict:  %d bytes (%.1f× vs plain)", len(dictBytes2), float64(len(plainBytes2))/float64(len(dictBytes2)))

	// Dict should compress well in both sorted and shuffled scenarios.
	dictRatioSorted := float64(len(plainBytes)) / float64(len(dictBytes))
	dictRatioShuffled := float64(len(plainBytes2)) / float64(len(dictBytes2))

	if dictRatioSorted < 2.0 {
		t.Errorf("dict sorted compression: got %.1f×, expected >= 2×", dictRatioSorted)
	}
	if dictRatioShuffled < 2.0 {
		t.Errorf("dict shuffled compression: got %.1f×, expected >= 2×", dictRatioShuffled)
	}

	// RLE should be much worse on shuffled data.
	rleRatioShuffled := float64(len(plainBytes2)) / float64(len(rleBytes2))
	if rleRatioShuffled > 1.0 {
		t.Logf("note: RLE expanded shuffled data (ratio=%.2f×) — expected for random order", rleRatioShuffled)
	}
}

// --- Unsupported type ---

func TestDictEncode_UnsupportedType(t *testing.T) {
	enc := NewDictionaryEncoder()
	_, err := enc.Encode([]uint32{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// --- Encoder interface compliance ---

func TestDictEncoder_Interface(t *testing.T) {
	var _ Encoder = NewDictionaryEncoder()

	enc := NewDictionaryEncoder()
	if enc.Type() != Dictionary {
		t.Errorf("Type(): got %v, want Dictionary", enc.Type())
	}
}

// --- Benchmarks ---

func BenchmarkDictEncode_1M_Strings(b *testing.B) {
	n := 1_000_000
	cardinality := 100
	uniqueValues := make([]string, cardinality)
	for i := range cardinality {
		uniqueValues[i] = fmt.Sprintf("value_%03d", i)
	}
	rng := rand.New(rand.NewSource(42))
	data := make([]string, n)
	for i := range data {
		data[i] = uniqueValues[rng.Intn(cardinality)]
	}

	enc := NewDictionaryEncoder()

	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkDictDecode_1M_Strings(b *testing.B) {
	n := 1_000_000
	cardinality := 100
	uniqueValues := make([]string, cardinality)
	for i := range cardinality {
		uniqueValues[i] = fmt.Sprintf("value_%03d", i)
	}
	rng := rand.New(rand.NewSource(42))
	data := make([]string, n)
	for i := range data {
		data[i] = uniqueValues[rng.Intn(cardinality)]
	}

	enc := NewDictionaryEncoder()
	encoded, _ := enc.Encode(data)

	for b.Loop() {
		DictDecodeString(encoded, n)
	}
}

func BenchmarkDictEncode_1M_Int64(b *testing.B) {
	n := 1_000_000
	rng := rand.New(rand.NewSource(42))
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(rng.Intn(50))
	}

	enc := NewDictionaryEncoder()

	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkDictDecode_1M_Int64(b *testing.B) {
	n := 1_000_000
	rng := rand.New(rand.NewSource(42))
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(rng.Intn(50))
	}

	enc := NewDictionaryEncoder()
	encoded, _ := enc.Encode(data)

	for b.Loop() {
		DictDecodeInt64(encoded, n)
	}
}
