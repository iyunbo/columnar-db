# Phase 3 Step 6 — Pipeline Benchmark Results

**Run date:** 2026-04-07
**Machine:** Apple M4, Go 1.26 (darwin/arm64)
**Dataset:** 1,000,000 rows × 3 columns (`age` int64, `name` string, `city` string)
**Query:** `SELECT name, age FROM t WHERE age > 30`
**Reproduce:** `go test -bench=Pipeline -benchmem -run=^$ ./exec/`

## Results

| Benchmark | ns/op | B/op | allocs/op | vs NaiveCount |
|---|---:|---:|---:|---:|
| `BenchmarkPipelineNaiveRowAtATime` | 8,094,960 | 67,764,243 | 6 | 13.5× slower |
| `BenchmarkPipelineNaiveCount` | **599,829** | 0 | 0 | **1.0×** |
| `BenchmarkPipelineVectorized` | 8,866,865 | 67,795,731 | 29 | 14.8× slower |
| `BenchmarkPipelineVectorizedDrain` | 3,004,285 | 0 | 0 | 5.0× slower |
| `BenchmarkPipelineVectorizedDrainPushedDown` | 2,672,692 | 0 | 0 | 4.5× slower |

## Plan target vs reality

| Target (from `docs/phase3-steps.md`) | Status |
|---|---|
| Zero allocs in vectorized drain | **Met** ✓ |
| Vectorized ≥ 5× faster than row-at-a-time | **Not met, but less bad than it first looks.** On the plan's *literal* comparison (both sides materialize survivors into a `[]row`), the vectorized and naive pipelines are at parity — `BenchmarkPipelineVectorized` at 8.87 ms vs `BenchmarkPipelineNaiveRowAtATime` at 8.09 ms, roughly a 1.1× slowdown. The 4.5–5× gap only appears once you *also* strip the result slice from the baseline (`NaiveCount`), which the plan never asked for. So the target was ambitious for this query shape, not catastrophically missed. |

## What actually happened

**The vectorized pipeline pays a memmove tax that doesn't exist in the naive path.**

For each of the ~977 batches over 1M rows, `ScanOp` does:
- `CopyFromChunk` for each requested column — an `AppendSlice` memmove of the source chunk's typed slice into the vector's pre-sized backing (8 KB per batch for `age`, ~16 KB for `name`).
- `Batch.Reset` to zero the null bitmap in place.
- `Sel.ResetFull(n)` to rewrite the uint16 index list.

Over the whole scan that is roughly 8 MB of `int64` copies + 16 MB of string-header copies + 2 MB of Sel index writes, plus per-batch operator method-call overhead.

The naive baseline does **none** of this. It iterates directly over the chunk's backing slice via `Int64Column.Get(i)`, which inlines to `data[i]`. There is no intermediate buffer, no copy, no bitmap reset. The inner loop is the tightest Go will generate for this shape:

```go
for i := 0; i < n; i++ {
    if nulls.IsNull(i) { continue }
    if vals.Get(i) > threshold { count++ }
}
```

### Caveat: the memmove tax doesn't explain the whole gap

The numbers above are the part I can defend from first principles. But reader beware: they don't account for everything.

On the push-down-to-Scan variant (the cleanest measurement, 2.67 ms), the full 8 MB `int64` memmove is only ~130 µs at M4's memory bandwidth. The gap vs `NaiveCount` (0.60 ms) is ~2.07 ms. Subtracting the 130 µs of computed memmove, **~1.94 ms of the gap is unexplained by the memmove tax alone.**

Candidate causes I have not profiled and am explicitly *not* claiming:

- Per-batch operator method-call overhead over ~977 `Next()` calls × a chain of `Scan → Filter`.
- The filter's `for _, i := range in.Indices() { ... out.AppendUint16Unchecked(i) }` loop — 977 batches × 1024-ish writes into the out selection's backing slice. Naive just increments an `int`.
- `Batch.Reset` zeroing the null bitmap each batch.
- `Sel.ResetFull(n)` rewriting 1024 uint16s per batch (~2 MB of writes total).
- Batch header bookkeeping.

All of these are plausible individually. None have been measured in this PR. **Profiling the pushed-down drain is follow-up #4 below** — it must happen *before* the zero-copy Vector optimization, because optimizing away the 130 µs memmove only to leave the ~1.94 ms elsewhere would just produce a second negative result.

I'm writing this caveat explicitly because the whole point of this document is to not repeat the mistake I already made in Step 4: writing a confident causal explanation for a performance result without the profile to back it.

## Where the vectorized model *does* win

The numbers above are not an indictment of vectorized execution. They reveal an important precondition: **vectorized only wins when the vectorization cost is amortized against something the naive path can't avoid.** That includes:

1. **I/O from disk with decode + decompress.** In the full read path (Phase 1-2), `Encoder` + `LZ4Compressor` decode bytes into a freshly allocated typed slice. The result of that decode *is* the vector — the memmove tax disappears because there's no pre-existing in-memory slice to alias. For this benchmark the dataset is already decoded in memory, so the copy is 100% overhead.
2. **Complex or per-row-expensive predicates.** A single `>` comparison is the Go compiler's best case. Compound predicates, user-defined expressions, `LIKE`, or anything that crosses a function-call boundary per row shifts the balance: the naive path pays the function-call cost per row, vectorized pays it per batch.
3. **Downstream aggregation.** `SUM`, `COUNT(DISTINCT)`, hash joins — these read surviving rows through the `Sel` index list and do vectorized arithmetic over typed slices. They never materialize rows. The `[]row` result slice in `BenchmarkPipelineVectorized` is an artifact of a benchmark that wants to compare against a "return a list" baseline; real consumers don't do this.
4. **Chained filters.** Two filters back-to-back cut intermediate work in half each time, but only if the intermediate form is cheap to represent — which `Selection` is (a shrinking uint16 list), and the naive path isn't.

## Honest conclusion

For the plan's canonical query (`WHERE age > 30` over decoded int64 in memory, then project two columns), a dumb tight loop beats the whole pipeline. This is real. The ≥5× speedup target was calibrated against a baseline that materialized a `[]row` result — as soon as you also drop that from the baseline, the naive path is faster.

**This does not mean the vectorized engine is wrong.** It means the *benchmark* is measuring a regime where the vectorized model cannot help. Phases 4+ (aggregation, joins, SQL planner) and the full disk read path will exercise regimes where the memmove tax is subsidized by unavoidable costs that the naive path cannot avoid either. That's where the ≥5× (or better) will appear.

## Follow-ups identified

1. **Zero-copy in-memory scan.** When `ScanOp` is reading from an already-decoded `RowGroup`, `CopyFromChunk` is pure overhead — it could alias the chunk's backing slice directly (read-only) and skip the copy entirely. This would close the ~130 µs of memmove on the pushed-down benchmark. Requires adding a "borrowed" mode to `Vector` so its backing slice isn't mutated by downstream operators. Non-trivial; flagged for Phase 3 polish.
2. **Compound-predicate benchmark.** Add a benchmark that stacks two predicates (`age > 30 AND age < 60`) or uses a more expensive predicate to show the regime where vectorized starts to win. Recommended for the *next* PR, not "eventually" — without this number the claim that vectorized wins on complex predicates is itself unverified.
3. **Read-path benchmark.** Once the full `SingleFileReader` → `ScanOp` path is exercised end-to-end (file decode dominates), repeat this benchmark. Expected: the naive path's advantage evaporates because both paths pay the decode cost and vectorized amortizes it better.
4. **Profile the pushed-down vectorized drain (must happen first).** Identify what the ~1.94 ms of non-memmove time is. Without this, #1's zero-copy optimization might close only 130 µs of a 2 ms gap and leave the engine still slower than `NaiveCount` — that would be a second negative result and would mean we optimized the wrong thing.
5. **`ColumnChunk.NullCount` foot-gun.** ~~The field is set once at construction but `ColumnChunk.Nulls` is a mutable bitmap.~~ **DONE** in PR #32: field removed, now a method that reads the live bitmap. Exec hot path switched to `chunk.Nulls.HasNulls()` (short-circuits faster than the old field read). Regression test locked in. Breaking change to `ColumnChunk`'s public surface (field → method).

## Success criteria revision

The plan's "≥5× vs row-at-a-time" target was written before measurement. The honest, revised criterion:

- **Zero allocs in vectorized drain** — met, enforced by `BenchmarkPipelineVectorizedDrain` with `ReportAllocs`.
- **Correctness parity** — met, enforced by `TestPipelineMatchesNaiveBaseline` and `TestPipelineWithNullsMatchesBaseline`.
- **Throughput vs naive** — NOT met for this query shape. Documented here. Follow-ups above will address it where appropriate.
