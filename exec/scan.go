package exec

import (
	"fmt"

	"github.com/iyunbo/columnar-db/storage"
)

// ScanOp is the source operator of the vectorized engine: it reads a
// RowGroup and yields a stream of Batches, each containing up to
// VectorSize rows of the requested columns.
//
// Design:
//   - The output Batch is allocated once at construction (one Vector per
//     requested column, each pre-sized to VectorSize) and reused across
//     every Next() call. Zero allocations inside the scan loop.
//   - On each Next() we Reset the batch, bulk-copy the next chunk of rows
//     from each source ColumnChunk into the matching Vector, and return
//     the same Batch pointer. Callers must finish consuming a batch
//     before calling Next() again.
//   - Scan does not copy whole chunks — it slices into the existing
//     decoded values in memory. The Vector holds its own backing slice
//     (it owns the copy) so downstream operators can mutate freely.
//
// Column projection: NewScanOp takes the list of column names to read.
// Only those columns are materialized into the output batch; unrequested
// columns are never touched, which is the first half of the Step 5
// projection push-down.
type ScanOp struct {
	rg *storage.RowGroup

	// srcChunks are the source chunks we read from, one per requested
	// column, aligned with batch.Vectors.
	srcChunks []*storage.ColumnChunk

	// batch is the output batch, reused across Next() calls.
	batch *Batch

	// offset is the next row index in the row group to emit.
	offset int
}

// NewScanOp builds a scan over the given row group, emitting the named
// columns in the order provided. Returns an error if any requested column
// is missing.
func NewScanOp(rg *storage.RowGroup, columns []string) (*ScanOp, error) {
	if rg == nil {
		return nil, fmt.Errorf("exec: NewScanOp: nil row group")
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("exec: NewScanOp: no columns requested")
	}

	srcChunks := make([]*storage.ColumnChunk, len(columns))
	vectors := make([]*Vector, len(columns))
	for i, name := range columns {
		chunk := rg.ColumnByName(name)
		if chunk == nil {
			return nil, fmt.Errorf("exec: NewScanOp: column %q not found in row group", name)
		}
		srcChunks[i] = chunk
		vectors[i] = NewVector(chunk.ColType)
	}

	return &ScanOp{
		rg:        rg,
		srcChunks: srcChunks,
		batch:     &Batch{Vectors: vectors},
		offset:    0,
	}, nil
}

// Next returns the next batch of up to VectorSize rows, or (nil, false)
// when the row group is exhausted. The returned *Batch pointer is the
// same across calls — consume or copy its contents before the next Next().
func (s *ScanOp) Next() (*Batch, bool) {
	remaining := s.rg.RowCount - s.offset
	if remaining <= 0 {
		return nil, false
	}
	n := remaining
	if n > VectorSize {
		n = VectorSize
	}

	s.batch.Reset()
	for i, chunk := range s.srcChunks {
		if err := s.batch.Vectors[i].CopyFromChunk(chunk, s.offset, n); err != nil {
			// CopyFromChunk can only fail on type/range mismatches, and both
			// are impossible by construction here (types are fixed at
			// NewScanOp, range is clamped against remaining). Panic so a
			// regression surfaces loudly instead of returning a half-filled
			// batch that callers would silently misinterpret.
			panic(fmt.Sprintf("exec: ScanOp.Next: CopyFromChunk: %v", err))
		}
	}
	s.offset += n
	return s.batch, true
}

// Reset rewinds the scan to the beginning of the row group. The output
// batch is cleared; no allocation.
func (s *ScanOp) Reset() {
	s.offset = 0
	s.batch.Reset()
}

// RowsEmitted returns the number of rows produced so far. Useful in tests.
func (s *ScanOp) RowsEmitted() int {
	return s.offset
}
