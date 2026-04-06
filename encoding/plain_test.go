package encoding

import (
	"math"
	"testing"
)

// --- Round-trip tests: encode -> decode -> verify equality ---

func TestPlainInt64RoundTrip(t *testing.T) {
	enc := NewPlainEncoder()

	tests := []struct {
		name string
		data []int64
	}{
		{"basic", []int64{1, 2, 3, -100, 0, math.MaxInt64, math.MinInt64}},
		{"single", []int64{42}},
		{"empty", []int64{}},
		{"zeros", []int64{0, 0, 0, 0}},
		{"negative", []int64{-1, -2, -3, -math.MaxInt64}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := DecodeInt64(encoded, len(tt.data))
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

func TestPlainFloat64RoundTrip(t *testing.T) {
	enc := NewPlainEncoder()

	tests := []struct {
		name string
		data []float64
	}{
		{"basic", []float64{1.1, 2.2, 3.3, -0.5, 0.0}},
		{"single", []float64{3.14159}},
		{"empty", []float64{}},
		{"special", []float64{math.Inf(1), math.Inf(-1), math.SmallestNonzeroFloat64, math.MaxFloat64}},
		{"nan", []float64{math.NaN()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := DecodeFloat64(encoded, len(tt.data))
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

func TestPlainStringRoundTrip(t *testing.T) {
	enc := NewPlainEncoder()

	tests := []struct {
		name string
		data []string
	}{
		{"basic", []string{"hello", "world", "foo"}},
		{"single", []string{"alone"}},
		{"empty_slice", []string{}},
		{"empty_strings", []string{"", "", ""}},
		{"mixed", []string{"", "a", "hello world", ""}},
		{"unicode", []string{"hello", "nihao", "bonjour"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := DecodeString(encoded, len(tt.data))
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

func TestPlainBoolRoundTrip(t *testing.T) {
	enc := NewPlainEncoder()

	tests := []struct {
		name string
		data []bool
	}{
		{"basic", []bool{true, false, true, true, false}},
		{"single_true", []bool{true}},
		{"single_false", []bool{false}},
		{"empty", []bool{}},
		{"all_true", []bool{true, true, true, true, true, true, true, true}},
		{"all_false", []bool{false, false, false, false}},
		// 9 values: crosses byte boundary (tests bit-packing across bytes)
		{"cross_byte", []bool{true, false, true, false, true, false, true, false, true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := enc.Encode(tt.data)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := DecodeBool(encoded, len(tt.data))
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

// --- Encoding size verification ---

func TestPlainInt64Size(t *testing.T) {
	enc := NewPlainEncoder()
	data := []int64{1, 2, 3}
	encoded, _ := enc.Encode(data)
	// 3 values * 8 bytes = 24 bytes
	if len(encoded) != 24 {
		t.Errorf("expected 24 bytes, got %d", len(encoded))
	}
}

func TestPlainBoolBitPacking(t *testing.T) {
	enc := NewPlainEncoder()

	// 8 bools should fit in 1 byte
	data8 := make([]bool, 8)
	encoded8, _ := enc.Encode(data8)
	if len(encoded8) != 1 {
		t.Errorf("8 bools: expected 1 byte, got %d", len(encoded8))
	}

	// 9 bools need 2 bytes
	data9 := make([]bool, 9)
	encoded9, _ := enc.Encode(data9)
	if len(encoded9) != 2 {
		t.Errorf("9 bools: expected 2 bytes, got %d", len(encoded9))
	}
}

func TestPlainStringSize(t *testing.T) {
	enc := NewPlainEncoder()
	data := []string{"ab", "cde"}
	encoded, _ := enc.Encode(data)
	// "ab": 4 (len) + 2 (data) = 6
	// "cde": 4 (len) + 3 (data) = 7
	// total = 13
	if len(encoded) != 13 {
		t.Errorf("expected 13 bytes, got %d", len(encoded))
	}
}

// --- Error cases ---

func TestPlainEncodeUnsupportedType(t *testing.T) {
	enc := NewPlainEncoder()
	_, err := enc.Encode([]int32{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestPlainDecodeInt64TooShort(t *testing.T) {
	_, err := DecodeInt64([]byte{1, 2, 3}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestPlainDecodeFloat64TooShort(t *testing.T) {
	_, err := DecodeFloat64([]byte{1, 2, 3}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestPlainDecodeStringTruncatedHeader(t *testing.T) {
	_, err := DecodeString([]byte{1, 2}, 1)
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestPlainDecodeStringTruncatedBody(t *testing.T) {
	// Length header says 100 bytes, but only 2 bytes of data follow.
	buf := []byte{100, 0, 0, 0, 'a', 'b'}
	_, err := DecodeString(buf, 1)
	if err == nil {
		t.Fatal("expected error for truncated body")
	}
}

func TestPlainDecodeBoolTooShort(t *testing.T) {
	_, err := DecodeBool([]byte{}, 1)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestPlainEncoderType(t *testing.T) {
	enc := NewPlainEncoder()
	if enc.Type() != Plain {
		t.Errorf("expected Plain, got %v", enc.Type())
	}
}

func TestEncodingTypeString(t *testing.T) {
	tests := []struct {
		t    EncodingType
		want string
	}{
		{Plain, "Plain"},
		{RLE, "RLE"},
		{Dictionary, "Dictionary"},
		{Delta, "Delta"},
		{EncodingType(99), "Unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.t.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", int(tt.t), got, tt.want)
		}
	}
}

// --- Benchmarks ---

func BenchmarkPlainEncodeInt64_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]int64, 1_000_000)
	for i := range data {
		data[i] = int64(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkPlainDecodeInt64_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]int64, 1_000_000)
	for i := range data {
		data[i] = int64(i)
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		DecodeInt64(encoded, 1_000_000)
	}
}

func BenchmarkPlainEncodeFloat64_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]float64, 1_000_000)
	for i := range data {
		data[i] = float64(i) * 1.1
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkPlainDecodeFloat64_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]float64, 1_000_000)
	for i := range data {
		data[i] = float64(i) * 1.1
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		DecodeFloat64(encoded, 1_000_000)
	}
}

func BenchmarkPlainEncodeString_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]string, 1_000_000)
	for i := range data {
		data[i] = "hello_world"
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkPlainDecodeString_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]string, 1_000_000)
	for i := range data {
		data[i] = "hello_world"
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		DecodeString(encoded, 1_000_000)
	}
}

func BenchmarkPlainEncodeBool_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]bool, 1_000_000)
	for i := range data {
		data[i] = i%2 == 0
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		enc.Encode(data)
	}
}

func BenchmarkPlainDecodeBool_1M(b *testing.B) {
	enc := NewPlainEncoder()
	data := make([]bool, 1_000_000)
	for i := range data {
		data[i] = i%2 == 0
	}
	encoded, _ := enc.Encode(data)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		DecodeBool(encoded, 1_000_000)
	}
}
