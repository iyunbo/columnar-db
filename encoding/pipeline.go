package encoding

import "fmt"

// EncodeThenCompress encodes data with the given encoder, then compresses
// the encoded bytes with the given compressor.
//
// This represents the full column write path:
//
//	typed data → column-aware encoding → general-purpose compression → bytes
func EncodeThenCompress(enc Encoder, comp Compressor, data any) ([]byte, error) {
	encoded, err := enc.Encode(data)
	if err != nil {
		return nil, fmt.Errorf("encode (%s): %w", enc.Type(), err)
	}

	compressed, err := comp.Compress(encoded)
	if err != nil {
		return nil, fmt.Errorf("compress (%s): %w", comp.Name(), err)
	}

	return compressed, nil
}

// DecompressThenDecode decompresses with the given compressor, then decodes
// with the given encoder.
//
// This represents the full column read path:
//
//	bytes → general-purpose decompression → column-aware decoding → typed data
func DecompressThenDecode(enc Encoder, comp Compressor, compressed []byte, count int) (any, error) {
	encoded, err := comp.Decompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("decompress (%s): %w", comp.Name(), err)
	}

	decoded, err := enc.Decode(encoded, count)
	if err != nil {
		return nil, fmt.Errorf("decode (%s): %w", enc.Type(), err)
	}

	return decoded, nil
}
