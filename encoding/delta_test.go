package encoding

import (
	"reflect"
	"testing"
)

func TestDeltaEncoder_MonotonicIncreasing(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{1, 2, 3, 4, 5}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := DeltaDecodeInt64(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

func TestDeltaEncoder_EqualSpacing(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{100, 200, 300, 400}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := DeltaDecodeInt64(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

func TestDeltaEncoder_Decreasing(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{100, 90, 80, 70}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := DeltaDecodeInt64(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

func TestDeltaEncoder_RandomMixed(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{5, 3, 8, 1, 9}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := DeltaDecodeInt64(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

func TestDeltaEncoder_AllSame(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{42, 42, 42}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := DeltaDecodeInt64(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

func TestDeltaEncoder_SingleValue(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{7}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Single value: 8 (base) + 4 (count) = 12 bytes, no deltas.
	if len(encoded) != 12 {
		t.Errorf("expected 12 bytes for single value, got %d", len(encoded))
	}

	decoded, err := DeltaDecodeInt64(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

func TestDeltaEncoder_Empty(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Empty: just 4 bytes for count=0.
	if len(encoded) != 4 {
		t.Errorf("expected 4 bytes for empty, got %d", len(encoded))
	}

	decoded, err := DeltaDecodeInt64(encoded, 0)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if len(decoded) != 0 {
		t.Errorf("expected empty slice, got %v", decoded)
	}
}

func TestDeltaEncoder_LargeMonotonic(t *testing.T) {
	enc := NewDeltaEncoder()
	n := 1_000_000
	input := make([]int64, n)
	for i := range input {
		input[i] = int64(i)
	}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Compare size vs plain encoding.
	plainEnc := NewPlainEncoder()
	plainEncoded, err := plainEnc.Encode(input)
	if err != nil {
		t.Fatalf("Plain encode failed: %v", err)
	}

	t.Logf("Delta encoded size: %d bytes", len(encoded))
	t.Logf("Plain encoded size: %d bytes", len(plainEncoded))
	t.Logf("Delta/Plain ratio: %.2f%%", float64(len(encoded))/float64(len(plainEncoded))*100)

	// Delta should be roughly the same size for sequential data (deltas are 8 bytes each),
	// but the format overhead is small. The real win is when combined with compression.
	decoded, err := DeltaDecodeInt64(encoded, n)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Error("decoded data doesn't match input for large monotonic sequence")
	}
}

func TestDeltaEncoder_Timestamps(t *testing.T) {
	enc := NewDeltaEncoder()
	// Simulate realistic timestamps: base ~1700000000, increment ~1000 each.
	n := 10000
	input := make([]int64, n)
	input[0] = 1700000000
	for i := 1; i < n; i++ {
		input[i] = input[i-1] + 1000
	}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	plainEnc := NewPlainEncoder()
	plainEncoded, err := plainEnc.Encode(input)
	if err != nil {
		t.Fatalf("Plain encode failed: %v", err)
	}

	t.Logf("Timestamps: delta=%d bytes, plain=%d bytes, ratio=%.2f%%",
		len(encoded), len(plainEncoded), float64(len(encoded))/float64(len(plainEncoded))*100)

	decoded, err := DeltaDecodeInt64(encoded, n)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Error("decoded timestamps don't match input")
	}
}

func TestDeltaEncoder_WrongTypeErrors(t *testing.T) {
	enc := NewDeltaEncoder()

	tests := []struct {
		name string
		data interface{}
	}{
		{"float64", []float64{1.0, 2.0}},
		{"string", []string{"a", "b"}},
		{"bool", []bool{true, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enc.Encode(tt.data)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			expected := "delta encoding only supports []int64"
			if err.Error() != expected {
				t.Errorf("got error %q, want %q", err.Error(), expected)
			}
		})
	}
}

func TestDeltaEncoder_Interface(t *testing.T) {
	// Verify DeltaEncoder implements Encoder interface.
	var _ Encoder = NewDeltaEncoder()

	enc := NewDeltaEncoder()
	if enc.Type() != Delta {
		t.Errorf("Type() = %v, want Delta", enc.Type())
	}
}

func TestDeltaEncoder_DecodeViaInterface(t *testing.T) {
	enc := NewDeltaEncoder()
	input := []int64{10, 20, 30, 40, 50}

	encoded, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Test the generic Decode method (via Encoder interface).
	result, err := enc.Decode(encoded, len(input))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	decoded, ok := result.([]int64)
	if !ok {
		t.Fatalf("Decode returned %T, want []int64", result)
	}

	if !reflect.DeepEqual(decoded, input) {
		t.Errorf("got %v, want %v", decoded, input)
	}
}

// --- Benchmarks ---

func BenchmarkDeltaEncode_1M(b *testing.B) {
	n := 1_000_000
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i)
	}
	enc := NewDeltaEncoder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encode(data)
	}
}

func BenchmarkDeltaDecode_1M(b *testing.B) {
	n := 1_000_000
	data := make([]int64, n)
	for i := range data {
		data[i] = int64(i)
	}
	enc := NewDeltaEncoder()
	encoded, _ := enc.Encode(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DeltaDecodeInt64(encoded, n)
	}
}
