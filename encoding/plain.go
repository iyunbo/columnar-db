package encoding

import (
	"encoding/binary"
	"fmt"
	"math"
)

// PlainEncoder serializes column data with no transformation — the baseline
// encoding that every column type supports.
//
// Format per type:
//   - Int64:   8 bytes each, little-endian
//   - Float64: IEEE 754 bits as uint64, 8 bytes each, little-endian
//   - String:  length-prefixed (4-byte little-endian length + UTF-8 bytes)
//   - Bool:    bit-packed (1 bit per value, LSB first, same layout as NullBitmap)
//
// Plain encoding is the fallback when no column-aware encoding helps.
// It also serves as the reference point for measuring compression ratios.
type PlainEncoder struct{}

// NewPlainEncoder creates a new plain encoder.
func NewPlainEncoder() *PlainEncoder {
	return &PlainEncoder{}
}

func (e *PlainEncoder) Type() EncodingType { return Plain }

// Encode serializes a typed slice to bytes using plain encoding.
func (e *PlainEncoder) Encode(data any) ([]byte, error) {
	switch v := data.(type) {
	case []int64:
		return encodeInt64(v), nil
	case []float64:
		return encodeFloat64(v), nil
	case []string:
		return encodeString(v), nil
	case []bool:
		return encodeBool(v), nil
	default:
		return nil, fmt.Errorf("plain encode: unsupported type %T", data)
	}
}

// Decode deserializes bytes back into a typed slice.
// The caller must know the original type and pass the correct count.
// Use DecodeInt64, DecodeFloat64, etc. if you know the type at compile time.
func (e *PlainEncoder) Decode(encoded []byte, count int) (any, error) {
	// Plain decoder needs type information — but the Encoder interface
	// doesn't carry it. We try to detect based on context, but in practice
	// callers should use the typed decode methods directly.
	//
	// This generic Decode is provided for interface compliance. Since we
	// can't infer the type from raw bytes alone, callers must use the
	// typed helpers: DecodeInt64, DecodeFloat64, DecodeString, DecodeBool.
	return nil, fmt.Errorf("plain decode: use typed methods (DecodeInt64, DecodeFloat64, DecodeString, DecodeBool) — raw bytes don't carry type information")
}

// --- Typed encode/decode methods ---

// encodeInt64 writes each int64 as 8 bytes, little-endian.
func encodeInt64(data []int64) []byte {
	buf := make([]byte, len(data)*8)
	for i, v := range data {
		binary.LittleEndian.PutUint64(buf[i*8:], uint64(v))
	}
	return buf
}

// DecodeInt64 reads count int64 values from plain-encoded bytes.
func DecodeInt64(encoded []byte, count int) ([]int64, error) {
	expected := count * 8
	if len(encoded) < expected {
		return nil, fmt.Errorf("decode int64: need %d bytes for %d values, got %d", expected, count, len(encoded))
	}
	result := make([]int64, count)
	for i := range result {
		result[i] = int64(binary.LittleEndian.Uint64(encoded[i*8:]))
	}
	return result, nil
}

// encodeFloat64 converts each float64 to IEEE 754 bits and writes as 8 bytes.
func encodeFloat64(data []float64) []byte {
	buf := make([]byte, len(data)*8)
	for i, v := range data {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

// DecodeFloat64 reads count float64 values from plain-encoded bytes.
func DecodeFloat64(encoded []byte, count int) ([]float64, error) {
	expected := count * 8
	if len(encoded) < expected {
		return nil, fmt.Errorf("decode float64: need %d bytes for %d values, got %d", expected, count, len(encoded))
	}
	result := make([]float64, count)
	for i := range result {
		result[i] = math.Float64frombits(binary.LittleEndian.Uint64(encoded[i*8:]))
	}
	return result, nil
}

// encodeString writes each string as: 4-byte length (little-endian) + UTF-8 bytes.
func encodeString(data []string) []byte {
	// Pre-calculate total size for a single allocation.
	total := 0
	for _, s := range data {
		total += 4 + len(s)
	}

	buf := make([]byte, total)
	offset := 0
	for _, s := range data {
		binary.LittleEndian.PutUint32(buf[offset:], uint32(len(s)))
		offset += 4
		copy(buf[offset:], s)
		offset += len(s)
	}
	return buf
}

// DecodeString reads count strings from plain-encoded bytes.
func DecodeString(encoded []byte, count int) ([]string, error) {
	result := make([]string, 0, count)
	offset := 0

	for i := range count {
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("decode string: unexpected end of data at value %d (need length header at offset %d)", i, offset)
		}
		strLen := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4

		if offset+strLen > len(encoded) {
			return nil, fmt.Errorf("decode string: unexpected end of data at value %d (need %d bytes at offset %d, have %d)", i, strLen, offset, len(encoded)-offset)
		}
		result = append(result, string(encoded[offset:offset+strLen]))
		offset += strLen
	}
	return result, nil
}

// encodeBool bit-packs booleans: 1 bit per value, LSB first.
// Same layout as NullBitmap in storage package.
func encodeBool(data []bool) []byte {
	numBytes := (len(data) + 7) / 8
	buf := make([]byte, numBytes)
	for i, v := range data {
		if v {
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			buf[byteIdx] |= 1 << bitIdx
		}
	}
	return buf
}

// DecodeBool reads count booleans from bit-packed plain-encoded bytes.
func DecodeBool(encoded []byte, count int) ([]bool, error) {
	numBytes := (count + 7) / 8
	if len(encoded) < numBytes {
		return nil, fmt.Errorf("decode bool: need %d bytes for %d values, got %d", numBytes, count, len(encoded))
	}
	result := make([]bool, count)
	for i := range result {
		byteIdx := i / 8
		bitIdx := uint(i % 8)
		result[i] = encoded[byteIdx]&(1<<bitIdx) != 0
	}
	return result, nil
}
