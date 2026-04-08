package exec

import (
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
// # Output sizing
//
// The result Vector has VectorSize (1024) capacity. Step 4 enforces
// numGroups ≤ VectorSize. If the hash table exceeds that, Next()
// returns an error via a deferred state check — Step 5 will extend
// GroupByOp to emit multiple output batches or grow Vector capacity.
// This is the explicit Step 4 limitation called out in the Step 1
// design note.
//
// # Reset
//
// Reset rewinds the operator: aggregators zero their per-group state
// in place (Aggregator.Reset), the hash table is cleared in place
// (Go's clear() builtin — no re-allocation), the output batch is
// reset, and done is cleared. The child is also Reset.
type GroupByOp struct {
	child Operator

	// keyColIndex is the column in every child batch that we group by.
	keyColIndex int
	// keyType is the type of the key column. Step 4 supports Int64 and
	// String only; the constructor rejects anything else.
	keyType storage.ColumnType

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

	// numGroups is the count of distinct groups seen so far. Growing
	// this past VectorSize triggers an error on Next (see "Output
	// sizing" above).
	numGroups int

	// groupIDs is a reusable per-batch scratch buffer: groupIDs[k] is
	// the group ordinal for the k-th selected row in the batch.
	// Preallocated to VectorSize; never re-grown at steady state.
	groupIDs []int32

	// out is the result Batch. Its key Vector is at index 0, aggregate
	// Vectors follow. Allocated once at construction.
	out *Batch
	// done is true after the first Next() emits the result batch.
	done bool
	// err is a sticky error set by ingestBatch when it cannot proceed
	// (currently only: Step 4 group-count cap exceeded). Once set,
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
	if len(specs) == 0 {
		return nil, fmt.Errorf("exec: GroupByOp requires at least one aggregator spec")
	}

	seen := make(map[Aggregator]struct{}, len(specs))
	out := &Batch{
		Vectors: make([]*Vector, 1+len(specs)),
		Sel:     NewSelection(),
	}
	// Column 0 is the key.
	out.Vectors[0] = NewVector(keyType)
	for i, s := range specs {
		if s.Agg == nil {
			return nil, fmt.Errorf("exec: GroupByOp AggregateSpec[%d] (%q) has nil Aggregator", i, s.Name)
		}
		if s.ColIndex < 0 {
			return nil, fmt.Errorf("exec: GroupByOp AggregateSpec[%d] (%q) has negative ColIndex %d", i, s.Name, s.ColIndex)
		}
		if _, dup := seen[s.Agg]; dup {
			return nil, fmt.Errorf("exec: GroupByOp AggregateSpec[%d] (%q) reuses an Aggregator instance already used by an earlier spec; each spec needs its own Aggregator", i, s.Name)
		}
		seen[s.Agg] = struct{}{}
		out.Vectors[1+i] = NewVector(s.Agg.OutputType())
		// No Init here — numGroups starts at 0, every aggregator gets
		// a first Grow(numGroups) per ingested batch before its first
		// Update call. The Aggregator contract allows a nil state slice
		// at this point; Grow's `make+copy` path handles it.
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

// Next drains the child, builds the hash table + aggregator state,
// then emits the result batch. Subsequent calls return (nil, false)
// until Reset. If ingestBatch sets a sticky error (Step 4 group-cap
// overflow), Next returns (nil, false) immediately and Err() returns
// that error.
func (g *GroupByOp) Next() (*Batch, bool) {
	if g.done || g.err != nil {
		return nil, false
	}

	for {
		b, ok := g.child.Next()
		if !ok {
			break
		}
		if err := g.ingestBatch(b); err != nil {
			g.err = err
			g.done = true
			return nil, false
		}
	}

	// Empty input: GROUP BY over zero rows yields zero result rows
	// (distinct from scalar AggregateOp, which yields one). Skip the
	// emission step and signal EOF immediately.
	if g.numGroups == 0 {
		g.done = true
		return nil, false
	}

	// Emit one row per group, in first-seen ordinal order. Finalize
	// for every aggregator is an append to the corresponding out
	// Vector, and the key column is built by scanning the hash table
	// back to (value → ordinal) — since ordinals are dense, a single
	// reverse-index pass is enough.
	g.emitKeys()
	for i, s := range g.specs {
		outVec := g.out.Vectors[1+i]
		for gid := 0; gid < g.numGroups; gid++ {
			s.Agg.Finalize(gid, outVec)
		}
	}

	// Mark all emitted rows live.
	g.out.Sel.Reset()
	for i := 0; i < g.numGroups; i++ {
		g.out.Sel.Add(i)
	}
	g.done = true
	return g.out, true
}

// ingestBatch processes one child batch: probe hash table, grow
// aggregator state, call Update on every aggregator.
func (g *GroupByOp) ingestBatch(b *Batch) error {
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

	// Step 4 group-count cap: the result Vector's backing slice is
	// VectorSize (1024), so we refuse to assign a (VectorSize+1)-th
	// ordinal. Checked INSIDE the probe loop, before the assignment —
	// checking it only at batch end would let a pathological batch
	// balloon numGroups and keysInt64 far past the cap before we
	// noticed. On overflow we set the sticky error, stop ingesting
	// (the partially-built groupIDs buffer is safe to abandon because
	// we never called Update), and the caller picks it up via Err().
	capErr := fmt.Errorf("exec: GroupByOp exceeded Step 4 group-count cap of %d; Step 5 will lift this", VectorSize)

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
					if g.numGroups >= VectorSize {
						return capErr
					}
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
						if g.numGroups >= VectorSize {
							return capErr
						}
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
					if g.numGroups >= VectorSize {
						return capErr
					}
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
					if g.numGroups >= VectorSize {
						return capErr
					}
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
						if g.numGroups >= VectorSize {
							return capErr
						}
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
					if g.numGroups >= VectorSize {
						return capErr
					}
					gid = int32(g.numGroups)
					ht[k] = gid
					g.keysString = append(g.keysString, k)
					g.numGroups++
				}
				g.groupIDs = append(g.groupIDs, gid)
			}
		}
	}

	// Grow every aggregator's per-group state once for this batch,
	// then Update.
	for _, s := range g.specs {
		s.Agg.Grow(g.numGroups)
		s.Agg.Update(b.Vectors[s.ColIndex], sel, g.groupIDs)
	}
	return nil
}

// emitKeys fills the key column of the output batch with one entry
// per group in ordinal order. Keys were recorded into keysInt64 /
// keysString during ingestBatch in first-seen order, so this is a
// straight walk — no allocation, no map iteration.
//
// The null-group branch is hoisted out of the per-row loop: we emit
// [0, nullGroupID), one AppendNull, then (nullGroupID, numGroups),
// so the tight loop body is a single typed append with no per-row
// branch on hasNullGroup.
func (g *GroupByOp) emitKeys() {
	keyOut := g.out.Vectors[0]
	nullCut := g.numGroups // sentinel "no split"
	if g.hasNullGroup {
		nullCut = int(g.nullGroupID)
	}
	switch g.keyType {
	case storage.TypeInt64:
		for gid := 0; gid < nullCut; gid++ {
			_ = keyOut.AppendInt64(g.keysInt64[gid])
		}
		if g.hasNullGroup {
			_ = keyOut.AppendNull()
		}
		for gid := nullCut + 1; gid < g.numGroups; gid++ {
			_ = keyOut.AppendInt64(g.keysInt64[gid])
		}
	case storage.TypeString:
		for gid := 0; gid < nullCut; gid++ {
			_ = keyOut.AppendString(g.keysString[gid])
		}
		if g.hasNullGroup {
			_ = keyOut.AppendNull()
		}
		for gid := nullCut + 1; gid < g.numGroups; gid++ {
			_ = keyOut.AppendString(g.keysString[gid])
		}
	}
}

// Reset rewinds the operator. Aggregators zero state in place, the
// hash table is cleared via the clear() builtin (no re-allocation),
// and the output batch is reset. Child operator is also Reset.
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
	g.hasNullGroup = false
	g.nullGroupID = 0
	g.numGroups = 0
	g.groupIDs = g.groupIDs[:0]
	g.keysInt64 = g.keysInt64[:0]
	g.keysString = g.keysString[:0]
	g.done = false
	g.err = nil
}
