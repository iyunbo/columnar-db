package encoding

import (
	"encoding/binary"
	"fmt"
)

// DeltaEncoder encodes int64 column data using delta encoding.
//
// Delta encoding stores differences between consecutive values instead of
// the values themselves. This is highly effective for monotonically increasing
// data like timestamps or auto-increment IDs, where deltas are small and
// uniform.
//
// Only []int64 is supported. Float64, string, and bool will return errors.
//
// Binary format:
//
//	[8 bytes: base value (first int64, little-endian)]
//	[4 bytes: num_values (uint32, little-endian)]
//	[N x 8 bytes: delta values (int64, little-endian, can be negative)]
//
// where N = num_values - 1 (no delta for the base value itself).
// For empty input, num_values = 0 and no base/deltas are stored.
type DeltaEncoder struct{}

// NewDeltaEncoder creates a new delta encoder.
func NewDeltaEncoder() *DeltaEncoder {
	return &DeltaEncoder{}
}

func (e *DeltaEncoder) Type() EncodingType { return Delta }

// Encode serializes a typed slice to bytes using delta encoding.
// Only []int64 is supported.
func (e *DeltaEncoder) Encode(data any) ([]byte, error) {
	switch v := data.(type) {
	case []int64:
		return deltaEncodeInt64(v), nil
	case []float64:
		return nil, fmt.Errorf("delta encoding only supports []int64")
	case []string:
		return nil, fmt.Errorf("delta encoding only supports []int64")
	case []bool:
		return nil, fmt.Errorf("delta encoding only supports []int64")
	default:
		return nil, fmt.Errorf("delta encoding only supports []int64")
	}
}

// Decode deserializes delta-encoded bytes back into a typed slice.
// Like other encoders, callers should use DeltaDecodeInt64 directly.
func (e *DeltaEncoder) Decode(encoded []byte, count int) (any, error) {
	return DeltaDecodeInt64(encoded, count)
}

// deltaEncodeInt64 encodes int64 values using delta encoding.
func deltaEncodeInt64(data []int64) []byte {
	if len(data) == 0 {
		// Empty: just the count header.
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, 0)
		return buf
	}

	// Header: 8 (base) + 4 (count) + (len-1)*8 (deltas)
	numDeltas := len(data) - 1
	buf := make([]byte, 8+4+numDeltas*8)

	// Write base value.
	binary.LittleEndian.PutUint64(buf[0:8], uint64(data[0]))

	// Write count.
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(data)))

	// Write deltas.
	prev := data[0]
	for i := 1; i < len(data); i++ {
		delta := data[i] - prev
		binary.LittleEndian.PutUint64(buf[12+(i-1)*8:], uint64(delta))
		prev = data[i]
	}

	return buf
}

// DeltaDecodeInt64 decodes delta-encoded bytes into count int64 values.
func DeltaDecodeInt64(encoded []byte, count int) ([]int64, error) {
	if count == 0 {
		return []int64{}, nil
	}

	// Minimum: 8 (base) + 4 (count header) = 12 bytes.
	if len(encoded) < 12 {
		return nil, fmt.Errorf("delta decode int64: need at least 12 bytes, got %d", len(encoded))
	}

	base := int64(binary.LittleEndian.Uint64(encoded[0:8]))
	numValues := int(binary.LittleEndian.Uint32(encoded[8:12]))

	if count > numValues {
		return nil, fmt.Errorf("delta decode int64: requested %d values but data contains %d", count, numValues)
	}

	// Check we have enough delta bytes.
	numDeltas := numValues - 1
	expectedSize := 12 + numDeltas*8
	if len(encoded) < expectedSize {
		return nil, fmt.Errorf("delta decode int64: need %d bytes for %d values, got %d", expectedSize, numValues, len(encoded))
	}

	result := make([]int64, count)
	result[0] = base
	current := base
	for i := 1; i < count; i++ {
		delta := int64(binary.LittleEndian.Uint64(encoded[12+(i-1)*8:]))
		current += delta
		result[i] = current
	}

	return result, nil
}
