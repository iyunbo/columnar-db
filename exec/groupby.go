package exec

import (
	"encoding/binary"
	"fmt"

	"github.com/iyunbo/columnar-db/storage"
)


// GroupByOp is the single-key hash GROUP BY operator. It pulls batches
// from a child operator, hashes each surviving row's key into a group
// ordinal, feeds (batch, sel, groupIDs) into every configured
// Aggregator, then emits a single output Batch whose columns are
//
//	[ keyVec, aggVec_0, aggVec_1, ..., aggVec_{N-1} ]
//
// One row per distinct group. Subsequent Next() calls return
// (nil, false) until Reset.
//
// Canonical query:
//
//	SELECT city, COUNT(*), AVG(price) FROM t GROUP BY city
//
// Pipeline shape:
//
//	Scan → Filter → GroupByOp → consumer
//
// # Key types
//
// Step 4 supports a single key column, of type Int64 or String. Nulls
// in the key column form their own group (standard SQL: NULL is a
// distinct group key, not filtered out). The null group's key value
// in the output Vector is AppendNull(), not a zero-value.
//
// # Group ID assignment
//
// Groups are assigned dense int32 ordinals in first-seen order as the
// hash table grows. Per batch:
//
//  1. Walk sel.Indices(), look up each key in the hash table. Cache
//     miss → assign a new ordinal and remember that the aggregator
//     state needs to be grown.
//  2. After the key-probe loop, call Grow(numGroups) on every
//     aggregator exactly once.
//  3. Call Update(vec, sel, groupIDs) on every aggregator. groupIDs[k]
//     is the group for sel.Indices()[k].
//
// Growing once per batch (instead of per row) keeps the grow cost
// amortized and matches the aggregator contract's "Grow may allocate"
// expectation.
//
// # Multi-batch emission (Step 5.5)
//
// The result Vector is capped at VectorSize (1024) per batch, but
// the total group count is unbounded. The first Next() call drains
// the child and builds the full hash table + aggregator state;
// subsequent Next() calls reset the output batch and emit the next
// VectorSize-wide window of groups (Finalize each aggregator over
// [emitCursor, emitCursor+VectorSize)). Once emitCursor reaches
// numGroups, Next() returns (nil, false). Phase 4 Step 6's
// benchmark needs ~10k groups, which this lift makes possible.
//
// # Multi-key support (Step 5)
//
// Multi-key composite GROUP BY is the sibling constructor
// NewGroupByOpMulti. Internally the operator branches on the
// `multiKey` flag: single-key mode keeps the Step 4 fast path
// (map[int64]int32 / map[string]int32 — no per-row allocation for
// existing groups), while multi-key mode encodes each row's key
// tuple into a scratch byte buffer and uses map[string]int32.
//
// The composite-key encoding is length-prefixed and null-tagged:
// for each key column, one byte (0x00 = null, 0x01 = value),
// followed for non-null values by 8 bytes little-endian for int64
// or 4-byte length + raw bytes for string. This makes (NULL, "Paris")
// and ("Lyon", NULL) distinguishable, and ("Paris", 10) and
// ("Paris", 20) distinguishable, without relying on a separator
// character that might collide with string content.
//
// Per-group original key values are stored in typed per-column
// slices so emission can materialize the key columns of the output
// batch without reverse-decoding the composite byte key.
//
// Step 5 scope note: the string-concat approach allocates a new
// string on every *new-group* insertion (map key copy). Existing-
// group lookups use Go's compiler optimization for m[string(buf)]
// and are alloc-free. A custom open-addressing table with inline
// binary keys is a post-Step-6 follow-up if benchmarking shows the
// map is the bottleneck.
//
// # Reset
//
// Reset rewinds the operator: aggregators zero their per-group state
// in place (Aggregator.Reset), the hash table is cleared in place
// (Go's clear() builtin — no re-allocation), the output batch is
// reset, and done is cleared. The child is also Reset.
type GroupByOp struct {
	child Operator

	// multiKey is true iff this operator was built via
	// NewGroupByOpMulti with a composite byte key. When false, the
	// operator uses the Step 4 single-key fast path (typed maps, no
	// encoding).
	multiKey bool

	// Single-key fields (Step 4; still the fast path):
	// keyColIndex is the column in every child batch that we group by.
	keyColIndex int
	// keyType is the type of the key column. Step 4 supports Int64 and
	// String only; the constructor rejects anything else.
	keyType storage.ColumnType

	// Multi-key fields (Step 5):
	// keyColIndexes lists the child-batch column indexes that form
	// the composite group key, left-to-right. keyTypes is parallel.
	keyColIndexes []int
	keyTypes      []storage.ColumnType
	// htMulti is the multi-key hash table: composite byte key
	// (string-cast) → dense int32 group ordinal.
	htMulti map[string]int32
	// Per key column, per group ordinal, the original decoded value
	// and null flag. Only the slice matching the column's type is
	// populated; the other is nil. numGroups-long in steady state.
	multiKeyInt64s  [][]int64
	multiKeyStrings [][]string
	multiKeyNulls   [][]bool
	// compositeBuf is the scratch byte buffer used to encode each
	// row's composite key during ingestBatchMulti. Reused across
	// rows; never re-allocated in steady state.
	compositeBuf []byte
	// keyVecsScratch / keyVecInt64sScratch / keyVecStringsScratch
	// are operator-level scratch slices resized in place at the top
	// of each ingestBatchMulti call, so the per-batch ingest path
	// does not allocate. Lengths == numKeyCols; the int64s/strings
	// scratches hold pre-hoisted typed views of each key column's
	// Values (fetching them once per batch instead of once per row).
	keyVecsScratch       []*Vector
	keyVecInt64sScratch  [][]int64
	keyVecStringsScratch [][]string
	// hasNullsScratch[i] is the hoisted keyVecs[i].Nulls.HasNulls()
	// value, set once per batch so the per-row null probe matches
	// the single-key path's hoisting discipline.
	hasNullsScratch []bool
	// rowNullsScratch carries the isNull flag per key column for the
	// current row being processed, so the new-group recording pass
	// reuses the encode pass's result instead of re-probing the
	// null bitmap.
	rowNullsScratch []bool

	specs []AggregateSpec

	// Hash tables. Exactly one is non-nil after construction, depending
	// on keyType. Using separate maps (rather than map[any]int32) avoids
	// the allocation and hashing cost of interface boxing on the hot
	// path.
	htInt64  map[int64]int32
	htString map[string]int32
	// hasNullGroup tracks whether a null key has already been seen in
	// this query iteration; the null group's ordinal is stored in
	// nullGroupID.
	hasNullGroup bool
	nullGroupID  int32

	// keysInt64 / keysString hold the distinct key values in first-
	// seen order: keysInt64[gid] is the key whose hash-table lookup
	// assigned group ordinal gid. Maintained alongside the hash
	// table during ingestBatch so emitKeys can emit them in ordinal
	// order without allocating a reverse-index scratch slice at
	// emission time. The null group stores the type zero value
	// (0 / "") — consumers consult hasNullGroup+nullGroupID, not the
	// slice entry, to decide whether to AppendNull.
	keysInt64  []int64
	keysString []string

	// numGroups is the count of distinct groups seen so far.
	// Unbounded (Step 5.5 lifted the VectorSize cap); emitted in
	// VectorSize-wide windows.
	numGroups int

	// groupIDs is a reusable per-batch scratch buffer: groupIDs[k] is
	// the group ordinal for the k-th selected row in the batch.
	// Preallocated to VectorSize; never re-grown at steady state.
	groupIDs []int32

	// out is the result Batch. Its key Vector is at index 0, aggregate
	// Vectors follow. Allocated once at construction and reused by
	// Reset+Finalize between emission windows (Step 5.5: multi-batch
	// emission). Its backing Vectors are VectorSize-sized; groups are
	// emitted in [emitCursor, emitCursor+VectorSize) windows.
	out *Batch
	// ingested is true once the first Next() has drained the child
	// and built the full hash table + aggregator state. Subsequent
	// Next() calls just emit the next window of groups without
	// re-draining. Cleared by Reset().
	ingested bool
	// emitCursor is the next group ordinal to emit. Advances by
	// (up to VectorSize) on every Next() that returns a batch; once
	// emitCursor >= numGroups, Next() returns (nil, false).
	emitCursor int
	// err is a sticky error set by ingestBatch when it cannot proceed
	// (currently only: child key column type mismatch). Once set,
	// Next() returns (nil, false) and the operator is poisoned until
	// Reset() clears it. Callers inspect via Err().
	err error
}

// NewGroupByOp constructs a hash-GROUP-BY operator over child, grouping
// by the single column at keyColIndex (which must be Int64 or String in
// Step 4), computing each spec in specs. At least one spec is required.
// Aggregator-instance reuse across specs is rejected for the same
// reason as AggregateOp.
func NewGroupByOp(child Operator, keyColIndex int, keyType storage.ColumnType, specs []AggregateSpec) (*GroupByOp, error) {
	if child == nil {
		return nil, fmt.Errorf("exec: GroupByOp requires a non-nil child operator")
	}
	if keyColIndex < 0 {
		return nil, fmt.Errorf("exec: GroupByOp keyColIndex %d must be non-negative", keyColIndex)
	}
	if keyType != storage.TypeInt64 && keyType != storage.TypeString {
		return nil, fmt.Errorf("exec: GroupByOp Step 4 supports only Int64/String keys, got %s", keyType)
	}
	if err := validateAggregateSpecs("GroupByOp", specs); err != nil {
		return nil, err
	}

	out := &Batch{
		Vectors: make([]*Vector, 1+len(specs)),
		Sel:     NewSelection(),
	}
	out.Vectors[0] = NewVector(keyType)
	for i, s := range specs {
		out.Vectors[1+i] = NewVector(s.Agg.OutputType())
	}

	op := &GroupByOp{
		child:       child,
		keyColIndex: keyColIndex,
		keyType:     keyType,
		specs:       specs,
		groupIDs:    make([]int32, 0, VectorSize),
		out:         out,
	}
	switch keyType {
	case storage.TypeInt64:
		op.htInt64 = make(map[int64]int32)
		op.keysInt64 = make([]int64, 0, VectorSize)
	case storage.TypeString:
		op.htString = make(map[string]int32)
		op.keysString = make([]string, 0, VectorSize)
	}
	return op, nil
}

// NewGroupByOpMulti constructs a multi-key hash GROUP BY over child,
// grouping by the composite key formed from the columns at
// keyColIndexes (one entry per key column, same length as keyTypes).
// Each key column's type must be Int64 or String. At least one key
// column and one spec are required. For a single key column, this
// constructor is equivalent to NewGroupByOp but uses the slower
// composite-key encoding path — prefer NewGroupByOp for the
// single-key fast path.
//
// Canonical query:
//
//	SELECT city, age_bucket, COUNT(*), AVG(price)
//	FROM t
//	GROUP BY city, age_bucket
//
// Output batch layout:
//
//	[ keyVec_0, keyVec_1, ..., keyVec_{K-1},
//	  aggVec_0, aggVec_1, ..., aggVec_{A-1} ]
//
// one row per distinct composite key in first-seen order,
// emitted across as many VectorSize-wide output batches as needed
// (Step 5.5 — no group-count cap).
func NewGroupByOpMulti(child Operator, keyColIndexes []int, keyTypes []storage.ColumnType, specs []AggregateSpec) (*GroupByOp, error) {
	if child == nil {
		return nil, fmt.Errorf("exec: GroupByOp requires a non-nil child operator")
	}
	if len(keyColIndexes) == 0 {
		return nil, fmt.Errorf("exec: GroupByOpMulti requires at least one key column")
	}
	if len(keyColIndexes) != len(keyTypes) {
		return nil, fmt.Errorf("exec: GroupByOpMulti keyColIndexes (len %d) and keyTypes (len %d) must be the same length", len(keyColIndexes), len(keyTypes))
	}
	for i, ci := range keyColIndexes {
		if ci < 0 {
			return nil, fmt.Errorf("exec: GroupByOpMulti keyColIndexes[%d] %d must be non-negative", i, ci)
		}
		if keyTypes[i] != storage.TypeInt64 && keyTypes[i] != storage.TypeString {
			return nil, fmt.Errorf("exec: GroupByOpMulti supports only Int64/String keys, keyTypes[%d] = %s", i, keyTypes[i])
		}
	}
	if err := validateAggregateSpecs("GroupByOpMulti", specs); err != nil {
		return nil, err
	}

	out := &Batch{
		Vectors: make([]*Vector, len(keyColIndexes)+len(specs)),
		Sel:     NewSelection(),
	}
	for i, kt := range keyTypes {
		out.Vectors[i] = NewVector(kt)
	}
	for i, s := range specs {
		out.Vectors[len(keyColIndexes)+i] = NewVector(s.Agg.OutputType())
	}

	// Copy the key spec slices so the caller can safely mutate theirs.
	kci := append([]int(nil), keyColIndexes...)
	kts := append([]storage.ColumnType(nil), keyTypes...)

	// Per-key-column state slices. Only the slice matching each
	// column's type is non-nil; the other stays nil so type switches
	// are direct index lookups, not runtime type assertions.
	mkInt64s := make([][]int64, len(kci))
	mkStrings := make([][]string, len(kci))
	mkNulls := make([][]bool, len(kci))
	for i, kt := range kts {
		switch kt {
		case storage.TypeInt64:
			mkInt64s[i] = make([]int64, 0, VectorSize)
		case storage.TypeString:
			mkStrings[i] = make([]string, 0, VectorSize)
		}
		mkNulls[i] = make([]bool, 0, VectorSize)
	}

	op := &GroupByOp{
		child:           child,
		multiKey:        true,
		keyColIndexes:   kci,
		keyTypes:        kts,
		specs:           specs,
		groupIDs:        make([]int32, 0, VectorSize),
		out:             out,
		htMulti:         make(map[string]int32),
		multiKeyInt64s:  mkInt64s,
		multiKeyStrings: mkStrings,
		multiKeyNulls:   mkNulls,
		// 64-byte initial capacity for the composite key scratch
		// buffer — enough for 4-6 mixed-type columns without a
		// re-alloc on the hot path. Grows as needed on first wide
		// row, then steady-state reuse.
		compositeBuf:         make([]byte, 0, 64),
		keyVecsScratch:       make([]*Vector, len(kci)),
		keyVecInt64sScratch:  make([][]int64, len(kci)),
		keyVecStringsScratch: make([][]string, len(kci)),
		hasNullsScratch:      make([]bool, len(kci)),
		rowNullsScratch:      make([]bool, len(kci)),
	}
	return op, nil
}

// Err returns the sticky error set by the most recent ingest, or nil.
// Callers should consult Err() after Next() returns (nil, false) when
// the query was not supposed to be empty, to distinguish EOF from
// failure. Cleared by Reset.
//
// The Operator interface does not expose Err() yet; Step 5/Phase 5 is
// the right time to fold error propagation into the core interface.
// Until then, callers that need error detection must type-assert to
// *GroupByOp (or use the sibling AggregateOp, which has no
// recoverable error yet).
func (g *GroupByOp) Err() error { return g.err }

// Next: on the first call, drains the child and builds the full
// hash table + aggregator state. Every call (including the first)
// then emits the next window of up to VectorSize groups and
// returns the reused output Batch. When the cursor reaches
// numGroups, Next returns (nil, false). GROUP BY over empty input
// yields zero result rows (distinct from scalar AggregateOp).
// If ingestBatch sets the sticky error (e.g. key column type
// mismatch), Next returns (nil, false) and Err() surfaces it.
func (g *GroupByOp) Next() (*Batch, bool) {
	if g.err != nil {
		return nil, false
	}
	if !g.ingested {
		for {
			b, ok := g.child.Next()
			if !ok {
				break
			}
			if err := g.ingestBatch(b); err != nil {
				g.err = err
				return nil, false
			}
		}
		g.ingested = true
	}
	if g.emitCursor >= g.numGroups {
		return nil, false
	}

	start := g.emitCursor
	end := start + VectorSize
	if end > g.numGroups {
		end = g.numGroups
	}

	// Reset all output vectors + selection so Finalize's append
	// lands at row 0 of this emission window.
	g.out.Reset()

	g.emitKeys(start, end)
	numKeyCols := 1
	if g.multiKey {
		numKeyCols = len(g.keyColIndexes)
	}
	for i, s := range g.specs {
		outVec := g.out.Vectors[numKeyCols+i]
		for gid := start; gid < end; gid++ {
			s.Agg.Finalize(gid, outVec)
		}
	}

	n := end - start
	for i := 0; i < n; i++ {
		g.out.Sel.Add(i)
	}
	g.emitCursor = end
	return g.out, true
}

// ingestBatch processes one child batch: probe hash table, grow
// aggregator state, call Update on every aggregator. Dispatches to
// the multi-key path when len(keyColIndexes) > 0.
func (g *GroupByOp) ingestBatch(b *Batch) error {
	if g.multiKey {
		return g.ingestBatchMulti(b)
	}
	sel := b.Sel
	indices := sel.Indices()
	if len(indices) == 0 {
		return nil
	}
	keyVec := b.Vectors[g.keyColIndex]
	if keyVec.Type != g.keyType {
		return fmt.Errorf("exec: GroupByOp key column type mismatch: expected %s, got %s", g.keyType, keyVec.Type)
	}

	// Reuse the per-batch groupIDs buffer. The buffer is preallocated
	// to VectorSize at construction and indices is always ≤ VectorSize
	// (upstream batches never exceed VectorSize), so no re-grow is
	// possible in steady state.
	g.groupIDs = g.groupIDs[:0]

	switch g.keyType {
	case storage.TypeInt64:
		vals := keyVec.Int64s()
		nulls := keyVec.Nulls
		ht := g.htInt64
		if !nulls.HasNulls() {
			for _, i := range indices {
				k := vals[i]
				gid, ok := ht[k]
				if !ok {
					gid = int32(g.numGroups)
					ht[k] = gid
					g.keysInt64 = append(g.keysInt64, k)
					g.numGroups++
				}
				g.groupIDs = append(g.groupIDs, gid)
			}
		} else {
			for _, i := range indices {
				if nulls.IsNull(int(i)) {
					if !g.hasNullGroup {
						g.nullGroupID = int32(g.numGroups)
						g.hasNullGroup = true
						g.keysInt64 = append(g.keysInt64, 0)
						g.numGroups++
					}
					g.groupIDs = append(g.groupIDs, g.nullGroupID)
					continue
				}
				k := vals[i]
				gid, ok := ht[k]
				if !ok {
					gid = int32(g.numGroups)
					ht[k] = gid
					g.keysInt64 = append(g.keysInt64, k)
					g.numGroups++
				}
				g.groupIDs = append(g.groupIDs, gid)
			}
		}
	case storage.TypeString:
		vals := keyVec.Strings()
		nulls := keyVec.Nulls
		ht := g.htString
		if !nulls.HasNulls() {
			for _, i := range indices {
				k := vals[i]
				gid, ok := ht[k]
				if !ok {
					gid = int32(g.numGroups)
					ht[k] = gid
					g.keysString = append(g.keysString, k)
					g.numGroups++
				}
				g.groupIDs = append(g.groupIDs, gid)
			}
		} else {
			for _, i := range indices {
				if nulls.IsNull(int(i)) {
					if !g.hasNullGroup {
						g.nullGroupID = int32(g.numGroups)
						g.hasNullGroup = true
						g.keysString = append(g.keysString, "")
						g.numGroups++
					}
					g.groupIDs = append(g.groupIDs, g.nullGroupID)
					continue
				}
				k := vals[i]
				gid, ok := ht[k]
				if !ok {
					gid = int32(g.numGroups)
					ht[k] = gid
					g.keysString = append(g.keysString, k)
					g.numGroups++
				}
				g.groupIDs = append(g.groupIDs, gid)
			}
		}
	}

	for _, s := range g.specs {
		s.Agg.Grow(g.numGroups)
		s.Agg.Update(b.Vectors[s.ColIndex], sel, g.groupIDs)
	}
	return nil
}

// emitKeys fills the key column(s) of the output batch with one
// entry per group in ordinal order. Keys were recorded into
// keysInt64/keysString (single-key) or multiKeyInt64s/
// multiKeyStrings (multi-key) during ingestBatch in first-seen
// order, so this is a straight walk — no allocation, no map
// iteration. Dispatches to the multi-key path when multiKey is set.
//
// The null-group branch is hoisted out of the per-row loop: we emit
// [0, nullGroupID), one AppendNull, then (nullGroupID, numGroups),
// so the tight loop body is a single typed append with no per-row
// branch on hasNullGroup.
func (g *GroupByOp) emitKeys(start, end int) {
	if g.multiKey {
		g.emitKeysMulti(start, end)
		return
	}
	keyOut := g.out.Vectors[0]
	// Null-group split adapted to the [start, end) window. When the
	// null group is outside the window, nullG == end and both tight
	// loops degenerate to a single straight walk.
	nullG := end
	if g.hasNullGroup {
		if n := int(g.nullGroupID); n >= start && n < end {
			nullG = n
		}
	}
	switch g.keyType {
	case storage.TypeInt64:
		for gid := start; gid < nullG; gid++ {
			_ = keyOut.AppendInt64(g.keysInt64[gid])
		}
		if nullG < end {
			_ = keyOut.AppendNull()
		}
		for gid := nullG + 1; gid < end; gid++ {
			_ = keyOut.AppendInt64(g.keysInt64[gid])
		}
	case storage.TypeString:
		for gid := start; gid < nullG; gid++ {
			_ = keyOut.AppendString(g.keysString[gid])
		}
		if nullG < end {
			_ = keyOut.AppendNull()
		}
		for gid := nullG + 1; gid < end; gid++ {
			_ = keyOut.AppendString(g.keysString[gid])
		}
	}
}

// ingestBatchMulti is the multi-key composite-key ingest path. Per
// surviving row it encodes the row's key tuple into compositeBuf,
// probes htMulti, records the decoded per-column key values into
// multiKeyInt64s/multiKeyStrings/multiKeyNulls on first sight, and
// appends the group ordinal to groupIDs. Once the probe loop is done,
// Grow+Update run once per aggregator (same shape as the single-key
// path).
//
// The group-count cap check lives inside the assignment branch, so a
// pathological batch of thousands of distinct keys cannot balloon
// numGroups past VectorSize — same contract as the single-key path.
func (g *GroupByOp) ingestBatchMulti(b *Batch) error {
	sel := b.Sel
	indices := sel.Indices()
	if len(indices) == 0 {
		return nil
	}
	// Hoist per-batch: Vector pointer, typed slice view, and
	// Nulls.HasNulls() for each key column. Matches the single-key
	// path's hoisting discipline so the per-row encode loop does
	// one null-bitmap probe per column (not two) and no typed-view
	// fetch.
	keyVecs := g.keyVecsScratch
	for i, ci := range g.keyColIndexes {
		if ci >= len(b.Vectors) {
			return fmt.Errorf("exec: GroupByOpMulti keyColIndex[%d]=%d out of range for batch (len %d)", i, ci, len(b.Vectors))
		}
		v := b.Vectors[ci]
		if v.Type != g.keyTypes[i] {
			return fmt.Errorf("exec: GroupByOpMulti key column %d type mismatch: expected %s, got %s", i, g.keyTypes[i], v.Type)
		}
		keyVecs[i] = v
		g.hasNullsScratch[i] = v.Nulls.HasNulls()
		switch g.keyTypes[i] {
		case storage.TypeInt64:
			g.keyVecInt64sScratch[i] = v.Int64s()
			g.keyVecStringsScratch[i] = nil
		case storage.TypeString:
			g.keyVecStringsScratch[i] = v.Strings()
			g.keyVecInt64sScratch[i] = nil
		}
	}
	keyInt64s := g.keyVecInt64sScratch
	keyStrings := g.keyVecStringsScratch
	hasNulls := g.hasNullsScratch
	rowNulls := g.rowNullsScratch

	g.groupIDs = g.groupIDs[:0]

	for _, rowIdx := range indices {
		g.compositeBuf = g.compositeBuf[:0]
		for i, kv := range keyVecs {
			isNull := hasNulls[i] && kv.Nulls.IsNull(int(rowIdx))
			rowNulls[i] = isNull
			if isNull {
				g.compositeBuf = append(g.compositeBuf, 0x00)
				continue
			}
			g.compositeBuf = append(g.compositeBuf, 0x01)
			switch g.keyTypes[i] {
			case storage.TypeInt64:
				// Extend the buffer in place and write the int64 LE
				// directly — avoids the stack-temp + slice-header
				// dance of `var tmp [8]byte; append(buf, tmp[:]...)`.
				n := len(g.compositeBuf)
				g.compositeBuf = append(g.compositeBuf, 0, 0, 0, 0, 0, 0, 0, 0)
				binary.LittleEndian.PutUint64(g.compositeBuf[n:], uint64(keyInt64s[i][rowIdx]))
			case storage.TypeString:
				s := keyStrings[i][rowIdx]
				n := len(g.compositeBuf)
				g.compositeBuf = append(g.compositeBuf, 0, 0, 0, 0)
				binary.LittleEndian.PutUint32(g.compositeBuf[n:], uint32(len(s)))
				g.compositeBuf = append(g.compositeBuf, s...)
			}
		}

		// m[string(buf)] for map[string]X is the Go compiler
		// optimization that avoids the string allocation on a pure
		// lookup. The insertion path below pays one string copy.
		gid, ok := g.htMulti[string(g.compositeBuf)]
		if !ok {
			gid = int32(g.numGroups)
			g.htMulti[string(g.compositeBuf)] = gid

			// Record the original per-column values for emission,
			// reusing the isNull flags the encode pass computed.
			for i := range keyVecs {
				isNull := rowNulls[i]
				g.multiKeyNulls[i] = append(g.multiKeyNulls[i], isNull)
				switch g.keyTypes[i] {
				case storage.TypeInt64:
					if isNull {
						g.multiKeyInt64s[i] = append(g.multiKeyInt64s[i], 0)
					} else {
						g.multiKeyInt64s[i] = append(g.multiKeyInt64s[i], keyInt64s[i][rowIdx])
					}
				case storage.TypeString:
					if isNull {
						g.multiKeyStrings[i] = append(g.multiKeyStrings[i], "")
					} else {
						g.multiKeyStrings[i] = append(g.multiKeyStrings[i], keyStrings[i][rowIdx])
					}
				}
			}
			g.numGroups++
		}
		g.groupIDs = append(g.groupIDs, gid)
	}

	for _, s := range g.specs {
		s.Agg.Grow(g.numGroups)
		s.Agg.Update(b.Vectors[s.ColIndex], sel, g.groupIDs)
	}
	return nil
}

// emitKeysMulti fills the K key columns of the output batch with one
// row per group in ordinal order. Null flags are consulted per
// (keyCol, group) from multiKeyNulls. Unlike the single-key fast
// path there is no hoisted null-cut loop split: the null-aware
// branch lives inside the inner loop. When Step 6's benchmark tells
// us multi-key GROUP BY is on a hot path, we can revisit.
func (g *GroupByOp) emitKeysMulti(start, end int) {
	for i, kt := range g.keyTypes {
		outVec := g.out.Vectors[i]
		nulls := g.multiKeyNulls[i]
		switch kt {
		case storage.TypeInt64:
			vals := g.multiKeyInt64s[i]
			for gid := start; gid < end; gid++ {
				if nulls[gid] {
					_ = outVec.AppendNull()
				} else {
					_ = outVec.AppendInt64(vals[gid])
				}
			}
		case storage.TypeString:
			vals := g.multiKeyStrings[i]
			for gid := start; gid < end; gid++ {
				if nulls[gid] {
					_ = outVec.AppendNull()
				} else {
					_ = outVec.AppendString(vals[gid])
				}
			}
		}
	}
}

// Reset rewinds the operator. Aggregators zero state in place, the
// hash table(s) are cleared via the clear() builtin (no re-
// allocation), per-group key slices truncate to length zero (keeping
// backing cap), and the output batch is reset. Child operator is
// also Reset.
func (g *GroupByOp) Reset() {
	g.child.Reset()
	g.out.Reset()
	for _, s := range g.specs {
		s.Agg.Reset()
	}
	if g.htInt64 != nil {
		clear(g.htInt64)
	}
	if g.htString != nil {
		clear(g.htString)
	}
	if g.htMulti != nil {
		clear(g.htMulti)
	}
	g.hasNullGroup = false
	g.nullGroupID = 0
	g.numGroups = 0
	g.groupIDs = g.groupIDs[:0]
	g.keysInt64 = g.keysInt64[:0]
	g.keysString = g.keysString[:0]
	for i := range g.multiKeyInt64s {
		if g.multiKeyInt64s[i] != nil {
			g.multiKeyInt64s[i] = g.multiKeyInt64s[i][:0]
		}
		if g.multiKeyStrings[i] != nil {
			g.multiKeyStrings[i] = g.multiKeyStrings[i][:0]
		}
		if g.multiKeyNulls[i] != nil {
			g.multiKeyNulls[i] = g.multiKeyNulls[i][:0]
		}
	}
	g.compositeBuf = g.compositeBuf[:0]
	g.ingested = false
	g.emitCursor = 0
	g.err = nil
}
