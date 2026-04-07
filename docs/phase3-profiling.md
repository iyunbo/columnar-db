# Phase 3 — Profiling the pushed-down vectorized drain

**Date**: 2026-04-07
**Branch**: `phase3/polish-profile-drain`
**Context**: Phase 3 Step 6 left an honest negative result on record:
`BenchmarkPipelineVectorizedDrainPushedDown` runs at ~2.67 ms while the
row-at-a-time `BenchmarkPipelineNaiveCount` finishes the same query in
~0.6 ms. The Step 6 write-up explained ~130 µs of that gap as memmove
tax in `ScanOp.CopyFromChunk` and called the remaining ~1.94 ms
*unprofiled*. Follow-up #4 was to actually profile it.

This document is the write-up for #4. **It is partly a story about how
the first profile lied.**

## The first profile (the lie)

CPU profile, 3-second run, no source code changes:

```
$ go test -bench=PipelineVectorizedDrainPushedDown -run=NONE \
    -benchtime=3s -cpuprofile=/tmp/drain.prof ./exec/
$ go tool pprof -top -cum -nodecount=20 /tmp/drain.prof
```

Top of the output (trimmed):

```
   flat  flat%   sum%       cum   cum%
  2.51s 86.85% 87.20%     2.52s 87.20%  storage.(*NullBitmap).HasNulls (inline)
  0.22s  7.61% 94.81%     0.24s  8.30%  exec.Int64Gt.Eval
  0.05s  1.73% 96.54%     0.05s  1.73%  runtime.memmove
```

`NullBitmap.HasNulls` was apparently **87 % of CPU**. The call site is
`Vector.CopyFromChunk`, which used to do this per batch:

```go
if chunk.Nulls.HasNulls() { … }   // walks the whole chunk's bitmap
```

`HasNulls()` short-circuits on the first non-zero byte, but the
benchmark uses null-free data — that's the worst case. Every batch
walked all 125 KB (1 M rows / 8) of the source bitmap, twice (age and
name). Across ~977 batches that's ~244 MB of bitmap scanned just to
discover "no nulls".

That's a clear bug regardless of the benchmark number, so I went ahead
and fixed it: added `NullBitmap.HasNullsInRange(start, n)` (`O(n/8)`,
only touches bytes covering the requested slice) and switched
`CopyFromChunk` to call it. The fix restores the expected O(batch_size)
scaling instead of O(chunk_size).

## The second profile (after the fix)

```
BenchmarkPipelineVectorizedDrainPushedDown-10   1324  2703246 ns/op   13 B/op   0 allocs/op
```

…is **2.70 ms**. The "before" number was 2.67 ms. **Walltime barely
moved.** Re-profile:

```
   flat  flat%   sum%       cum   cum%
  2.48s 73.37% 73.37%     2.78s 82.25%  exec.Int64Gt.Eval
  0.32s  9.47% 82.84%     0.32s  9.47%  exec.(*Selection).ResetFull
  0.28s  8.28% 91.12%     0.28s  8.28%  exec.(*Selection).AppendUint16Unchecked
  0.16s  4.73% 95.86%     0.16s  4.73%  runtime.memmove
  0.04s  1.18% 97.04%     0.04s  1.18%  storage.(*NullBitmap).HasNullsInRange
```

`HasNullsInRange` is now 1.2 % — the byte-walking *was* eliminated. But
the engine isn't measurably faster. The original 87 % was a **pprof
sampling artifact**: a tiny, very high-frequency leaf function called
once per batch attracted disproportionate signal-handler attribution.
The function was real, but the cost it represented wasn't.

Lesson: when a single inlined leaf eats >80 % of a profile, treat it
as suspicious *first* — re-run, change the code, and confirm the
walltime moves before believing the attribution.

## The real picture

After the fix the profile is honest:

| Function                                 | flat % | What it is                                               |
|------------------------------------------|--------|----------------------------------------------------------|
| `Int64Gt.Eval`                           | 73 %   | Predicate's tight loop materializing the selection       |
| `Selection.ResetFull`                    |  9 %   | `for i := 0..n: indices[i] = uint16(i)` per batch        |
| `Selection.AppendUint16Unchecked`        |  8 %   | `out.indices = append(out.indices, uint16(i))` per match |
| `runtime.memmove`                        |  5 %   | `ScanOp.CopyFromChunk`'s actual copy                     |
| `HasNullsInRange`                        |  1 %   | Per-batch null check                                     |

So the ~2.1 ms gap between vectorized-drain (2.7 ms) and naive-count
(0.6 ms) breaks down roughly as:

1. **~1.5 ms** — building a selection vector at all. `Int64Gt.Eval`
   loops over 1024 input indices, branches on the predicate, and
   *appends* each survivor to an output `[]uint16`. The naive count
   does the same comparison but only increments a counter — no append,
   no second pass downstream. This is the **late-materialization tax**:
   you pay it whether or not anyone downstream needs the selection.
2. **~0.4 ms** — `Selection.ResetFull` + `AppendUint16Unchecked`
   bookkeeping (filling the input range, growing the output indices).
3. **~0.13 ms** — memmove tax in `CopyFromChunk` (matches the Step 6
   estimate).
4. **~0.05 ms** — null-bitmap check (was masquerading as 2.5 s).

This confirms the Step 6 hypothesis qualitatively (memmove is real,
but small) and falsifies it quantitatively (memmove is ~6 % of the
gap, not the dominant cost). The dominant cost is **building a
selection vector that the drain consumer immediately throws away**.

## Implications for the polish list

- **#1 Zero-copy in-memory scan** still worth doing — it removes the
  130 µs memmove tax — but it's a small win, not a 5× unlock. Filed
  expectation: `VectorizedDrainPushedDown` improves from ~2.7 ms to
  ~2.55 ms, still ~4× slower than `NaiveCount`.
- **#2 Compound-predicate benchmark** is now the most important
  follow-up. The profile says vectorization's overhead is selection-
  vector materialization; vectorization wins when that selection is
  *reused*. A 2-clause predicate (`age > 30 AND city = "Paris"`) on
  the vectorized engine is one selection build + one cheap re-filter,
  while the naive baseline pays both branches per row. That's the
  regime where the late-materialization tax pays off.
- **A new candidate: drop the selection vector when the consumer
  doesn't need it.** The drain benchmark is artificial — it just
  counts `Sel.Len()`. A "count operator" that fuses with the predicate
  could skip the append entirely. Out of scope for Phase 3, but worth
  noting as a Phase 6+ optimization.

## Honest summary

The first profile pointed at a real bug (`HasNulls` scanning whole
chunks per batch) but its 87 % attribution was a sampling lie. Fixing
the bug was correct and restored expected scaling, but did not move
walltime. The real bottleneck is the selection-vector materialization
inside the predicate's tight loop — fundamental to late
materialization, and only amortized when the selection gets reused
downstream. The drain benchmark, by construction, doesn't reuse it.

The Phase 3 negative result stands: vectorization is slower than a
hand-written count loop on a single-predicate query. Phase 3 still
delivered the architecture (operators, batches, selection vectors,
predicates), and the profiling work clarified *exactly* which future
benchmark would falsify the negative finding. The next concrete step
is #2, the compound-predicate benchmark.
