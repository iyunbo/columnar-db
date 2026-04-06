package encoding

import (
	"encoding/binary"
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// Compressor compresses/decompresses raw bytes.
// Applied after encoding, before writing to disk.
// This is separate from Encoder — you encode first (plain/rle/dict/delta),
// then compress with a Compressor.
type Compressor interface {
	// Compress compresses raw bytes.
	Compress(data []byte) ([]byte, error)

	// Decompress decompresses previously compressed bytes.
	Decompress(compressed []byte) ([]byte, error)

	// Name returns the compressor name.
	Name() string
}

// LZ4Compressor implements Compressor using LZ4 block compression.
//
// Binary format:
//
//	[4 bytes: original_size (uint32 LE)]
//	[4 bytes: compressed_size (uint32 LE)] — 0 means stored uncompressed
//	[data...]
//
// If compressed size >= original size, the data is stored uncompressed
// and compressed_size is set to 0 as a flag.
//
// Thread-safe: no shared mutable state.
type LZ4Compressor struct{}

// NewLZ4Compressor creates a new LZ4 compressor.
func NewLZ4Compressor() *LZ4Compressor {
	return &LZ4Compressor{}
}

func (c *LZ4Compressor) Name() string { return "LZ4" }

// Compress compresses data using LZ4 block compression.
func (c *LZ4Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		// Empty input: header only, both sizes zero.
		header := make([]byte, 8)
		return header, nil
	}

	// Allocate destination buffer for LZ4 compression.
	maxDst := lz4.CompressBlockBound(len(data))
	compressed := make([]byte, maxDst)

	n, err := lz4.CompressBlock(data, compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}

	// If compression didn't help (n == 0 means incompressible, or n >= original),
	// store uncompressed with compressed_size = 0 as flag.
	if n == 0 || n >= len(data) {
		buf := make([]byte, 8+len(data))
		binary.LittleEndian.PutUint32(buf[0:4], uint32(len(data)))
		binary.LittleEndian.PutUint32(buf[4:8], 0) // flag: uncompressed
		copy(buf[8:], data)
		return buf, nil
	}

	// Compressed successfully.
	buf := make([]byte, 8+n)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(data)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(n))
	copy(buf[8:], compressed[:n])
	return buf, nil
}

// Decompress decompresses LZ4-compressed data.
func (c *LZ4Compressor) Decompress(compressed []byte) ([]byte, error) {
	if len(compressed) < 8 {
		return nil, fmt.Errorf("lz4 decompress: data too short (need at least 8 bytes header, got %d)", len(compressed))
	}

	originalSize := binary.LittleEndian.Uint32(compressed[0:4])
	compressedSize := binary.LittleEndian.Uint32(compressed[4:8])

	// Empty input case.
	if originalSize == 0 && compressedSize == 0 {
		return []byte{}, nil
	}

	payload := compressed[8:]

	// Uncompressed flag: compressed_size == 0.
	if compressedSize == 0 {
		if uint32(len(payload)) < originalSize {
			return nil, fmt.Errorf("lz4 decompress: expected %d uncompressed bytes, got %d", originalSize, len(payload))
		}
		result := make([]byte, originalSize)
		copy(result, payload[:originalSize])
		return result, nil
	}

	// Compressed data.
	if uint32(len(payload)) < compressedSize {
		return nil, fmt.Errorf("lz4 decompress: expected %d compressed bytes, got %d", compressedSize, len(payload))
	}

	dst := make([]byte, originalSize)
	n, err := lz4.UncompressBlock(payload[:compressedSize], dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}
	if uint32(n) != originalSize {
		return nil, fmt.Errorf("lz4 decompress: decompressed %d bytes, expected %d", n, originalSize)
	}

	return dst, nil
}
