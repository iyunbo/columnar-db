package encoding

import (
	"encoding/binary"
	"fmt"
	"math"
)

// RLEEncoder encodes column data using run-length encoding.
//
// RLE replaces consecutive runs of identical values with (value, count) pairs.
// This is highly effective for sorted or low-cardinality columns where the same
// value appears many times in a row (e.g., sorted status codes, country codes).
//
// Format per type:
//   - Int64:   [8 bytes value][4 bytes run_length] per run
//   - Float64: [8 bytes value (IEEE 754)][4 bytes run_length] per run
//   - String:  [4 bytes str_len][str_data][4 bytes run_length] per run
//   - Bool:    [1 byte value (0/1)][4 bytes run_length] per run
//
// If data has no consecutive repeats, RLE is worse than plain encoding due to
// the per-value run_length overhead. The auto-selector (Phase 2 later) will
// pick the right encoding based on data characteristics.
type RLEEncoder struct{}

// NewRLEEncoder creates a new RLE encoder.
func NewRLEEncoder() *RLEEncoder {
	return &RLEEncoder{}
}

func (e *RLEEncoder) Type() EncodingType { return RLE }

// Encode serializes a typed slice to bytes using run-length encoding.
func (e *RLEEncoder) Encode(data any) ([]byte, error) {
	switch v := data.(type) {
	case []int64:
		return rleEncodeInt64(v), nil
	case []float64:
		return rleEncodeFloat64(v), nil
	case []string:
		return rleEncodeString(v), nil
	case []bool:
		return rleEncodeBool(v), nil
	default:
		return nil, fmt.Errorf("rle encode: unsupported type %T", data)
	}
}

// Decode deserializes RLE-encoded bytes back into a typed slice.
// Like PlainEncoder, callers should use the typed methods directly.
func (e *RLEEncoder) Decode(encoded []byte, count int) (any, error) {
	return nil, fmt.Errorf("rle decode: use typed methods (RLEDecodeInt64, RLEDecodeFloat64, RLEDecodeString, RLEDecodeBool)")
}

// --- Int64 RLE ---

func rleEncodeInt64(data []int64) []byte {
	if len(data) == 0 {
		return nil
	}

	// Worst case: every value is unique → len(data) runs of 12 bytes each.
	buf := make([]byte, 0, len(data)*12)
	tmp := make([]byte, 12)

	runVal := data[0]
	runCount := uint32(1)

	for i := 1; i < len(data); i++ {
		if data[i] == runVal {
			runCount++
		} else {
			binary.LittleEndian.PutUint64(tmp[0:8], uint64(runVal))
			binary.LittleEndian.PutUint32(tmp[8:12], runCount)
			buf = append(buf, tmp...)
			runVal = data[i]
			runCount = 1
		}
	}
	// Flush last run.
	binary.LittleEndian.PutUint64(tmp[0:8], uint64(runVal))
	binary.LittleEndian.PutUint32(tmp[8:12], runCount)
	buf = append(buf, tmp...)

	return buf
}

// RLEDecodeInt64 decodes RLE-encoded bytes into count int64 values.
func RLEDecodeInt64(encoded []byte, count int) ([]int64, error) {
	if count == 0 {
		return []int64{}, nil
	}
	result := make([]int64, 0, count)
	offset := 0

	for len(result) < count {
		if offset+12 > len(encoded) {
			return nil, fmt.Errorf("rle decode int64: unexpected end of data at offset %d (decoded %d of %d values)", offset, len(result), count)
		}
		val := int64(binary.LittleEndian.Uint64(encoded[offset:]))
		runLen := int(binary.LittleEndian.Uint32(encoded[offset+8:]))
		offset += 12

		for j := 0; j < runLen && len(result) < count; j++ {
			result = append(result, val)
		}
	}
	return result, nil
}

// --- Float64 RLE ---

func rleEncodeFloat64(data []float64) []byte {
	if len(data) == 0 {
		return nil
	}

	buf := make([]byte, 0, len(data)*12)
	tmp := make([]byte, 12)

	runBits := math.Float64bits(data[0])
	runCount := uint32(1)

	for i := 1; i < len(data); i++ {
		bits := math.Float64bits(data[i])
		if bits == runBits {
			runCount++
		} else {
			binary.LittleEndian.PutUint64(tmp[0:8], runBits)
			binary.LittleEndian.PutUint32(tmp[8:12], runCount)
			buf = append(buf, tmp...)
			runBits = bits
			runCount = 1
		}
	}
	binary.LittleEndian.PutUint64(tmp[0:8], runBits)
	binary.LittleEndian.PutUint32(tmp[8:12], runCount)
	buf = append(buf, tmp...)

	return buf
}

// RLEDecodeFloat64 decodes RLE-encoded bytes into count float64 values.
func RLEDecodeFloat64(encoded []byte, count int) ([]float64, error) {
	if count == 0 {
		return []float64{}, nil
	}
	result := make([]float64, 0, count)
	offset := 0

	for len(result) < count {
		if offset+12 > len(encoded) {
			return nil, fmt.Errorf("rle decode float64: unexpected end of data at offset %d (decoded %d of %d values)", offset, len(result), count)
		}
		val := math.Float64frombits(binary.LittleEndian.Uint64(encoded[offset:]))
		runLen := int(binary.LittleEndian.Uint32(encoded[offset+8:]))
		offset += 12

		for j := 0; j < runLen && len(result) < count; j++ {
			result = append(result, val)
		}
	}
	return result, nil
}

// --- String RLE ---

func rleEncodeString(data []string) []byte {
	if len(data) == 0 {
		return nil
	}

	// Estimate capacity: each run needs 4 (str_len) + len(str) + 4 (run_count).
	total := 0
	for _, s := range data {
		total += 4 + len(s) + 4
	}
	buf := make([]byte, 0, total)

	runVal := data[0]
	runCount := uint32(1)

	for i := 1; i < len(data); i++ {
		if data[i] == runVal {
			runCount++
		} else {
			buf = appendStringRun(buf, runVal, runCount)
			runVal = data[i]
			runCount = 1
		}
	}
	buf = appendStringRun(buf, runVal, runCount)

	return buf
}

func appendStringRun(buf []byte, s string, count uint32) []byte {
	// [4 bytes str_len][str_data][4 bytes run_count]
	tmp := make([]byte, 4)
	binary.LittleEndian.PutUint32(tmp, uint32(len(s)))
	buf = append(buf, tmp...)
	buf = append(buf, s...)
	binary.LittleEndian.PutUint32(tmp, count)
	buf = append(buf, tmp...)
	return buf
}

// RLEDecodeString decodes RLE-encoded bytes into count strings.
func RLEDecodeString(encoded []byte, count int) ([]string, error) {
	if count == 0 {
		return []string{}, nil
	}
	result := make([]string, 0, count)
	offset := 0

	for len(result) < count {
		// Read string length.
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("rle decode string: unexpected end at offset %d reading str_len (decoded %d of %d)", offset, len(result), count)
		}
		strLen := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4

		// Read string data.
		if offset+strLen > len(encoded) {
			return nil, fmt.Errorf("rle decode string: unexpected end at offset %d reading %d-byte string (decoded %d of %d)", offset, strLen, len(result), count)
		}
		val := string(encoded[offset : offset+strLen])
		offset += strLen

		// Read run count.
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("rle decode string: unexpected end at offset %d reading run_count (decoded %d of %d)", offset, len(result), count)
		}
		runLen := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4

		for j := 0; j < runLen && len(result) < count; j++ {
			result = append(result, val)
		}
	}
	return result, nil
}

// --- Bool RLE ---

func rleEncodeBool(data []bool) []byte {
	if len(data) == 0 {
		return nil
	}

	// Each run: 1 byte value + 4 bytes count = 5 bytes.
	buf := make([]byte, 0, len(data)*5)
	tmp := make([]byte, 5)

	runVal := data[0]
	runCount := uint32(1)

	for i := 1; i < len(data); i++ {
		if data[i] == runVal {
			runCount++
		} else {
			if runVal {
				tmp[0] = 1
			} else {
				tmp[0] = 0
			}
			binary.LittleEndian.PutUint32(tmp[1:5], runCount)
			buf = append(buf, tmp...)
			runVal = data[i]
			runCount = 1
		}
	}
	if runVal {
		tmp[0] = 1
	} else {
		tmp[0] = 0
	}
	binary.LittleEndian.PutUint32(tmp[1:5], runCount)
	buf = append(buf, tmp...)

	return buf
}

// RLEDecodeBool decodes RLE-encoded bytes into count bool values.
func RLEDecodeBool(encoded []byte, count int) ([]bool, error) {
	if count == 0 {
		return []bool{}, nil
	}
	result := make([]bool, 0, count)
	offset := 0

	for len(result) < count {
		if offset+5 > len(encoded) {
			return nil, fmt.Errorf("rle decode bool: unexpected end at offset %d (decoded %d of %d)", offset, len(result), count)
		}
		val := encoded[offset] != 0
		runLen := int(binary.LittleEndian.Uint32(encoded[offset+1:]))
		offset += 5

		for j := 0; j < runLen && len(result) < count; j++ {
			result = append(result, val)
		}
	}
	return result, nil
}
