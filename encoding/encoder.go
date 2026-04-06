// Package encoding implements column-aware encoding schemes for columnar data.
//
// In a columnar database, encoding happens per-column and is type-aware.
// Different columns benefit from different encodings:
//   - Plain: raw serialization (baseline, always works)
//   - RLE: consecutive repeated values (e.g., sorted status codes)
//   - Dictionary: low-cardinality strings (e.g., city names)
//   - Delta: monotonic integers (e.g., timestamps, auto-increment IDs)
//
// Encoding reduces data size before optional general-purpose compression
// (like LZ4) is applied on top. The key insight: column-aware encoding
// exploits data patterns that generic compression cannot.
package encoding

import "fmt"

// EncodingType identifies the encoding scheme used for a column chunk.
type EncodingType int

const (
	Plain      EncodingType = iota // Raw values, no transformation
	RLE                            // Run-length encoding
	Dictionary                     // Dictionary encoding
	Delta                          // Delta encoding for integers
)

// String returns the human-readable name of an encoding type.
func (t EncodingType) String() string {
	switch t {
	case Plain:
		return "Plain"
	case RLE:
		return "RLE"
	case Dictionary:
		return "Dictionary"
	case Delta:
		return "Delta"
	default:
		return fmt.Sprintf("Unknown(%d)", int(t))
	}
}

// Encoder encodes typed column data to bytes and decodes it back.
//
// Each encoder handles one encoding scheme. The Encode/Decode methods
// accept concrete typed slices ([]int64, []float64, []string, []bool)
// passed as interface{}. This avoids generics complexity while keeping
// the interface simple for Phase 2's four encoding types.
type Encoder interface {
	// Encode serializes a typed slice to bytes.
	// data must be one of: []int64, []float64, []string, []bool.
	Encode(data interface{}) ([]byte, error)

	// Decode deserializes bytes back into a typed slice.
	// count is the number of values to decode.
	// Returns the same concrete type that was passed to Encode.
	Decode(encoded []byte, count int) (interface{}, error)

	// Type returns which encoding scheme this encoder implements.
	Type() EncodingType
}
