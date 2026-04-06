package encoding

import (
	"encoding/binary"
	"fmt"
	"math"
)

// DictionaryEncoder encodes column data using dictionary encoding.
//
// Dictionary encoding replaces each value with an integer index into a dictionary
// of unique values. This is the most important encoding for string columns in
// columnar databases (like Parquet), achieving massive compression on low-cardinality
// data (e.g., city names, status codes, country codes).
//
// Binary format:
//
//	[4 bytes: dict_size (number of unique values)]
//	[dictionary entries...]
//	  - Int64:   8 bytes each, little-endian
//	  - Float64: 8 bytes each (IEEE 754 bits), little-endian
//	  - String:  4 bytes length + UTF-8 bytes (same as plain encoding)
//	  - Bool:    1 byte each (0 or 1)
//	[4 bytes: num_values]
//	[indices...]  // each index is uint32 (4 bytes LE)
//
// For low-cardinality data (e.g., 100 unique strings across 1M rows), the
// dictionary is tiny and the indices array uses only 4 bytes per value instead
// of the full string. This routinely achieves 10× or better compression.
type DictionaryEncoder struct{}

// NewDictionaryEncoder creates a new dictionary encoder.
func NewDictionaryEncoder() *DictionaryEncoder {
	return &DictionaryEncoder{}
}

func (e *DictionaryEncoder) Type() EncodingType { return Dictionary }

// Encode serializes a typed slice to bytes using dictionary encoding.
func (e *DictionaryEncoder) Encode(data interface{}) ([]byte, error) {
	switch v := data.(type) {
	case []int64:
		return dictEncodeInt64(v), nil
	case []float64:
		return dictEncodeFloat64(v), nil
	case []string:
		return dictEncodeString(v), nil
	case []bool:
		return dictEncodeBool(v), nil
	default:
		return nil, fmt.Errorf("dictionary encode: unsupported type %T", data)
	}
}

// Decode deserializes dictionary-encoded bytes back into a typed slice.
// Like PlainEncoder, callers should use the typed methods directly.
func (e *DictionaryEncoder) Decode(encoded []byte, count int) (interface{}, error) {
	return nil, fmt.Errorf("dictionary decode: use typed methods (DictDecodeInt64, DictDecodeFloat64, DictDecodeString, DictDecodeBool)")
}

// --- Int64 Dictionary ---

func dictEncodeInt64(data []int64) []byte {
	if len(data) == 0 {
		buf := make([]byte, 8)
		// dict_size = 0, num_values = 0
		return buf
	}

	// Build dictionary: value → index.
	dict := make(map[int64]uint32)
	var dictOrder []int64
	indices := make([]uint32, len(data))

	for i, v := range data {
		if _, ok := dict[v]; !ok {
			dict[v] = uint32(len(dictOrder))
			dictOrder = append(dictOrder, v)
		}
		indices[i] = dict[v]
	}

	dictSize := len(dictOrder)
	// 4 (dict_size) + dictSize*8 (entries) + 4 (num_values) + len(data)*4 (indices)
	total := 4 + dictSize*8 + 4 + len(data)*4
	buf := make([]byte, total)
	offset := 0

	// Write dict_size.
	binary.LittleEndian.PutUint32(buf[offset:], uint32(dictSize))
	offset += 4

	// Write dictionary entries.
	for _, v := range dictOrder {
		binary.LittleEndian.PutUint64(buf[offset:], uint64(v))
		offset += 8
	}

	// Write num_values.
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(data)))
	offset += 4

	// Write indices.
	for _, idx := range indices {
		binary.LittleEndian.PutUint32(buf[offset:], idx)
		offset += 4
	}

	return buf
}

// DictDecodeInt64 decodes dictionary-encoded bytes into count int64 values.
func DictDecodeInt64(encoded []byte, count int) ([]int64, error) {
	if count == 0 {
		return []int64{}, nil
	}

	offset := 0
	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode int64: unexpected end reading dict_size")
	}
	dictSize := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	// Read dictionary.
	dict := make([]int64, dictSize)
	for i := 0; i < dictSize; i++ {
		if offset+8 > len(encoded) {
			return nil, fmt.Errorf("dict decode int64: unexpected end reading dict entry %d", i)
		}
		dict[i] = int64(binary.LittleEndian.Uint64(encoded[offset:]))
		offset += 8
	}

	// Read num_values.
	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode int64: unexpected end reading num_values")
	}
	numValues := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	if numValues != count {
		return nil, fmt.Errorf("dict decode int64: encoded num_values=%d but requested count=%d", numValues, count)
	}

	// Read indices and resolve.
	result := make([]int64, count)
	for i := 0; i < count; i++ {
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("dict decode int64: unexpected end reading index %d", i)
		}
		idx := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4
		if idx >= dictSize {
			return nil, fmt.Errorf("dict decode int64: index %d out of range (dict_size=%d)", idx, dictSize)
		}
		result[i] = dict[idx]
	}

	return result, nil
}

// --- Float64 Dictionary ---

func dictEncodeFloat64(data []float64) []byte {
	if len(data) == 0 {
		buf := make([]byte, 8)
		return buf
	}

	// Use uint64 bits as map key for exact float comparison (handles NaN, -0).
	dict := make(map[uint64]uint32)
	var dictOrder []uint64
	indices := make([]uint32, len(data))

	for i, v := range data {
		bits := math.Float64bits(v)
		if _, ok := dict[bits]; !ok {
			dict[bits] = uint32(len(dictOrder))
			dictOrder = append(dictOrder, bits)
		}
		indices[i] = dict[bits]
	}

	dictSize := len(dictOrder)
	total := 4 + dictSize*8 + 4 + len(data)*4
	buf := make([]byte, total)
	offset := 0

	binary.LittleEndian.PutUint32(buf[offset:], uint32(dictSize))
	offset += 4

	for _, bits := range dictOrder {
		binary.LittleEndian.PutUint64(buf[offset:], bits)
		offset += 8
	}

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(data)))
	offset += 4

	for _, idx := range indices {
		binary.LittleEndian.PutUint32(buf[offset:], idx)
		offset += 4
	}

	return buf
}

// DictDecodeFloat64 decodes dictionary-encoded bytes into count float64 values.
func DictDecodeFloat64(encoded []byte, count int) ([]float64, error) {
	if count == 0 {
		return []float64{}, nil
	}

	offset := 0
	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode float64: unexpected end reading dict_size")
	}
	dictSize := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	dict := make([]float64, dictSize)
	for i := 0; i < dictSize; i++ {
		if offset+8 > len(encoded) {
			return nil, fmt.Errorf("dict decode float64: unexpected end reading dict entry %d", i)
		}
		dict[i] = math.Float64frombits(binary.LittleEndian.Uint64(encoded[offset:]))
		offset += 8
	}

	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode float64: unexpected end reading num_values")
	}
	numValues := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	if numValues != count {
		return nil, fmt.Errorf("dict decode float64: encoded num_values=%d but requested count=%d", numValues, count)
	}

	result := make([]float64, count)
	for i := 0; i < count; i++ {
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("dict decode float64: unexpected end reading index %d", i)
		}
		idx := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4
		if idx >= dictSize {
			return nil, fmt.Errorf("dict decode float64: index %d out of range (dict_size=%d)", idx, dictSize)
		}
		result[i] = dict[idx]
	}

	return result, nil
}

// --- String Dictionary ---

func dictEncodeString(data []string) []byte {
	if len(data) == 0 {
		buf := make([]byte, 8)
		return buf
	}

	// Build dictionary.
	dict := make(map[string]uint32)
	var dictOrder []string
	indices := make([]uint32, len(data))

	for i, v := range data {
		if _, ok := dict[v]; !ok {
			dict[v] = uint32(len(dictOrder))
			dictOrder = append(dictOrder, v)
		}
		indices[i] = dict[v]
	}

	// Calculate total size.
	dictSize := len(dictOrder)
	dictBytes := 0
	for _, s := range dictOrder {
		dictBytes += 4 + len(s)
	}
	total := 4 + dictBytes + 4 + len(data)*4
	buf := make([]byte, total)
	offset := 0

	// Write dict_size.
	binary.LittleEndian.PutUint32(buf[offset:], uint32(dictSize))
	offset += 4

	// Write dictionary entries (length-prefixed strings).
	for _, s := range dictOrder {
		binary.LittleEndian.PutUint32(buf[offset:], uint32(len(s)))
		offset += 4
		copy(buf[offset:], s)
		offset += len(s)
	}

	// Write num_values.
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(data)))
	offset += 4

	// Write indices.
	for _, idx := range indices {
		binary.LittleEndian.PutUint32(buf[offset:], idx)
		offset += 4
	}

	return buf
}

// DictDecodeString decodes dictionary-encoded bytes into count strings.
func DictDecodeString(encoded []byte, count int) ([]string, error) {
	if count == 0 {
		return []string{}, nil
	}

	offset := 0
	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode string: unexpected end reading dict_size")
	}
	dictSize := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	dict := make([]string, dictSize)
	for i := 0; i < dictSize; i++ {
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("dict decode string: unexpected end reading str_len for entry %d", i)
		}
		strLen := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4
		if offset+strLen > len(encoded) {
			return nil, fmt.Errorf("dict decode string: unexpected end reading string data for entry %d", i)
		}
		dict[i] = string(encoded[offset : offset+strLen])
		offset += strLen
	}

	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode string: unexpected end reading num_values")
	}
	numValues := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	if numValues != count {
		return nil, fmt.Errorf("dict decode string: encoded num_values=%d but requested count=%d", numValues, count)
	}

	result := make([]string, count)
	for i := 0; i < count; i++ {
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("dict decode string: unexpected end reading index %d", i)
		}
		idx := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4
		if idx >= dictSize {
			return nil, fmt.Errorf("dict decode string: index %d out of range (dict_size=%d)", idx, dictSize)
		}
		result[i] = dict[idx]
	}

	return result, nil
}

// --- Bool Dictionary ---

func dictEncodeBool(data []bool) []byte {
	if len(data) == 0 {
		buf := make([]byte, 8)
		return buf
	}

	// Build dictionary (at most 2 entries: false=0, true=1).
	dict := make(map[bool]uint32)
	var dictOrder []bool
	indices := make([]uint32, len(data))

	for i, v := range data {
		if _, ok := dict[v]; !ok {
			dict[v] = uint32(len(dictOrder))
			dictOrder = append(dictOrder, v)
		}
		indices[i] = dict[v]
	}

	dictSize := len(dictOrder)
	// 4 (dict_size) + dictSize*1 (entries) + 4 (num_values) + len(data)*4 (indices)
	total := 4 + dictSize + 4 + len(data)*4
	buf := make([]byte, total)
	offset := 0

	binary.LittleEndian.PutUint32(buf[offset:], uint32(dictSize))
	offset += 4

	for _, v := range dictOrder {
		if v {
			buf[offset] = 1
		} else {
			buf[offset] = 0
		}
		offset++
	}

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(data)))
	offset += 4

	for _, idx := range indices {
		binary.LittleEndian.PutUint32(buf[offset:], idx)
		offset += 4
	}

	return buf
}

// DictDecodeBool decodes dictionary-encoded bytes into count bool values.
func DictDecodeBool(encoded []byte, count int) ([]bool, error) {
	if count == 0 {
		return []bool{}, nil
	}

	offset := 0
	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode bool: unexpected end reading dict_size")
	}
	dictSize := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	dict := make([]bool, dictSize)
	for i := 0; i < dictSize; i++ {
		if offset+1 > len(encoded) {
			return nil, fmt.Errorf("dict decode bool: unexpected end reading dict entry %d", i)
		}
		dict[i] = encoded[offset] != 0
		offset++
	}

	if offset+4 > len(encoded) {
		return nil, fmt.Errorf("dict decode bool: unexpected end reading num_values")
	}
	numValues := int(binary.LittleEndian.Uint32(encoded[offset:]))
	offset += 4

	if numValues != count {
		return nil, fmt.Errorf("dict decode bool: encoded num_values=%d but requested count=%d", numValues, count)
	}

	result := make([]bool, count)
	for i := 0; i < count; i++ {
		if offset+4 > len(encoded) {
			return nil, fmt.Errorf("dict decode bool: unexpected end reading index %d", i)
		}
		idx := int(binary.LittleEndian.Uint32(encoded[offset:]))
		offset += 4
		if idx >= dictSize {
			return nil, fmt.Errorf("dict decode bool: index %d out of range (dict_size=%d)", idx, dictSize)
		}
		result[i] = dict[idx]
	}

	return result, nil
}
