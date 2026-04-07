# Phase 3 — Compound predicate benchmark

**Date**: 2026-04-07
**Branch**: `phase3/polish-compound-benchmark`
**Context**: Polish follow-up #2 from `docs/phase3-benchmark-results.md`,
promoted to "most important" by the profiling write-up in
`docs/phase3-profiling.md`. The profiling work concluded that
vectorization's overhead is selection-vector materialization inside
the predicate's tight loop, and **predicted** that compound predicates
would be the regime where late materialization pays off — because the
second predicate would only walk surviving indices via the selection
vector, while the naive baseline must evaluate both predicates per row.

**This document is the write-up for #2. The prediction was wrong.**

## The numbers

1 M-row uniform `int64` age column, query variants:

| Query                   | Naive (row-at-a-time) | Vectorized drain | Naive Δ vs prev | Vec Δ vs prev |
|-------------------------|----------------------:|-----------------:|----------------:|--------------:|
| `age > 30`              |             0.60 ms   |        2.76 ms   |             —   |           —   |
| `30 < age < 60`         |             4.01 ms   |        4.82 ms   |       +3.41 ms  |     +2.06 ms  |
| `30 < age < 60 ∧ ≠ 45`  |             4.22 ms   |        5.36 ms   |       +0.21 ms  |     +0.55 ms  |

Single run, no `benchstat`, no variance bars — see "Methodology &
caveats" below for what this lets us claim and what it doesn't.

Reproduction:
```
go test -bench='PipelineNaiveCount|PipelineVectorizedDrain' \
        -run=NONE -benchtime=2s ./exec/
```

The vectorized engine **never crosses** the naive baseline on this
workload. At 3 predicates the gap has narrowed from 4.6× (1 predicate)
to 1.27× (3 predicates), but the absolute number is still on naive's
side.

## What I expected vs what happened

**Expected**: naive's per-predicate cost is roughly linear (~3 ms per
added compare), vectorized's per-predicate cost is much smaller
(~0.5 ms because the second filter only walks survivors), so the
vectorized engine catches up around 2 predicates and wins at 3+.

**Reality**: naive is **wildly nonlinear** — the jump from 1 → 2
predicates (+3.41 ms) is much bigger than 2 → 3 (+0.21 ms). My first
guess was an "auto-SIMD cliff" — that the single-predicate loop was
auto-vectorized by the Go compiler / NEON, and the second predicate
broke the vectorizable shape. **That guess was wrong**, and I was
about to write it down without checking. The reviewer caught it; I
disassembled both functions before publishing this. The real
explanation is **branch prediction**.

Single-predicate `naiveRowAtATimeCount` compiles its inner predicate
to a branchless `CSINC` (conditional set-and-increment): the compiler
sees `if a > 30 { count++ }` as "conditionally add 1 to count" and
emits no taken/not-taken branch on the data. Inner loop is purely
scalar arithmetic + bitmap-IsNull bookkeeping, ~15 instructions per
row, ~6 IPC on M4 → ~0.6 ms for 1 M rows. **This matches the
measurement exactly and has nothing to do with SIMD.** No `V*` ops,
no `.16B`/`.2D` operands, no NEON. Just CSINC and IPC.

Compound-predicate `naiveRowAtATimeCountCompound` cannot use CSINC
because short-circuit evaluation (`a > 30 && a < 60`) requires real
control flow. The compiler emits two branches (`BGE`, `BLE`). The
second branch is the killer: filtered to ages 31..89 (the survivors
of the first branch), the second predicate `< 60` is true for 30/59
≈ **51 % of inputs — the worst possible case for the branch
predictor**. Apple M4's branch misprediction penalty is ~14 cycles.

  1,000,000 rows × ~0.5 misprediction × ~14 cycles ≈ 7 M cycles
  ≈ 1.75 ms additional cost over the branchless baseline

Plus the genuine extra work of the second compare (~0.7 ms),
plus the loss of CSINC on the *first* branch (which is now part of a
short-circuit chain with the second), and we land near the +3.4 ms
observed.

The 2 → 3 step is essentially free for naive (+0.21 ms) because the
third predicate `≠ 45` over the 31..59 survivors is true for 28/29 ≈
**97 % of inputs — highly predictable**, near-zero misprediction
cost. (And the 2 % path costs nothing because the operation it
guards — `count++` — is itself near-free.)

The vectorized engine has no CSINC on its single-predicate case
(`Int64Gt.Eval` writes selected indices into an output `[]uint16`,
which is a side effect that defeats CSINC), so it never had the
branchless party trick to lose. It pays a flat ~0.5–2 ms per added
filter operator — marginal costs are *lower* than naive's 1 → 2 jump,
but its starting point is so much higher that it never catches up on
this workload.

## Per-predicate marginal cost

| Δ predicate | Naive  | Vectorized |
|-------------|-------:|-----------:|
| 1 → 2       | +3.41 ms | +2.06 ms |
| 2 → 3       | +0.21 ms | +0.55 ms |

Vectorized wins the 1 → 2 marginal cost (because no auto-SIMD to
lose). Naive wins the 2 → 3 marginal cost (because the auto-SIMD is
already gone, so adding a compare is nearly free; while vectorized
adds an entire FilterOp call with its own selection-vector pass).

**Both signs flip at the cliff.** There is no monotone "vectorization
wins as predicates grow" story for pure `int64` compares against an
in-memory column. The marginal cost difference is small in both
directions.

## Methodology & caveats

What this benchmark *does* measure:

- A 1 M-row in-memory `int64` column with no nulls.
- Survivor counting only — no row materialization downstream.
- A single physical machine (Apple M4, macOS, Go from /usr/local/go),
  not pinned to a specific Go version in this doc.
- One `go test -bench=… -benchtime=2s` run per benchmark, not five
  runs with `benchstat`. Variance is not reported.
- Numbers were stable enough across the two runs I did during the
  write-up that I'm comfortable making the **direction** of the
  conclusion (vectorized never crosses naive on this workload) load-
  bearing. I am **not** making the absolute ns/op numbers load-
  bearing.

What this benchmark does **not** prove:

- That vectorization loses on disk-backed scans (untested — Phase 4+).
- That vectorization loses on string / variadic / function-call
  predicates (untested — see polish list below).
- That vectorization loses on aggregate / join workloads (untested).
- Anything about other Go versions, other CPUs (esp. x86_64), or
  other branch-predictor implementations.

The auto-SIMD-vs-branch-prediction explanation in the previous
section is **verified by disassembling both functions** (`go test -c
-gcflags='-S'`). Both inner loops are pure scalar — no NEON
instructions. The single-predicate version uses `CSINC`; the compound
versions use `BGE`/`BLE`. The branch-prediction explanation is the
mechanism the assembly supports; the SIMD explanation is the
mechanism it falsifies.

## Why the profiling doc's prediction was wrong

The prediction in `docs/phase3-profiling.md` said:

> A 2-clause predicate (`age > 30 AND city = "Paris"`) on the
> vectorized engine is one selection build + one cheap re-filter,
> while the naive baseline pays both branches per row. That's the
> regime where the late-materialization tax pays off.

The first half is correct: a 2-clause predicate on vectorized **is**
one selection build + one cheap re-filter, and the marginal +2.06 ms
matches that mental model.

The second half — "that's the regime where it pays off" — was
inferred from the *naive baseline being linear*, which it isn't on
this workload. The naive baseline is roughly `~CSINC-fast + 3.4 + ε`,
not `0.6 + 0.6 × n`. Late materialization beats *linear* naive
scaling. It does not beat *branchless-then-cliff-then-flat* naive
scaling driven by branch-predictor behaviour on uniformly-distributed
data.

I should have benchmarked before claiming. (Same lesson as the
predicate folklore caught in PR #27.) And in this very PR I caught
myself making a *second* unverified claim on top of the first — I
initially wrote that the cliff was "auto-SIMD breaking", which the
reviewer flagged and the disassembly disproved. Two layers of
"sounds plausible" stacked into one document. The lesson holds: when
narrating a measurement, the explanatory mechanism is itself a claim
that needs evidence.

## Bounded conclusion

The Phase 3 negative result generalizes more broadly than Step 6
suggested:

> For pure in-memory `int64` predicate-only queries on Apple M4, a
> tight Go scalar loop (with or without auto-SIMD) beats this
> vectorized engine across 1, 2, and 3 predicates. The gap narrows as
> predicates grow but does not close.

The vectorized model is **still architecturally correct for the
workloads it's designed for**, which are not this benchmark:

1. **Disk-backed scans** — when decode dominates, vectorization wins
   on memory locality and amortized per-batch cost. Phase 4+.
2. **String / variable-length / function-call predicates** — where
   the per-row cost is much higher than an `int64` compare, late
   materialization saves more than the selection-vector tax. We have
   no string predicates yet to test this.
3. **Wider SIMD with native vectorization** — Go's auto-SIMD on a
   simple `int64 > c` loop is the worst possible adversary. A
   hand-written `arm64` NEON inner loop in the vectorized predicate
   would change the picture. Out of scope for Phase 3.
4. **Aggregations / joins** — late materialization avoids row
   reconstruction at the boundaries. We have no aggregates yet to
   test this either.

## Implication for the polish list

- **#1 zero-copy in-memory scan** is still worth doing as a
  ~130 µs cleanup (and an architectural simplification), but it
  cannot close the predicate-tax gap. Confirmed not a 5× unlock.
- **#2 compound-predicate benchmark** — done, this document.
  Result: negative. Documented.
- **#3 read-path benchmark (with disk I/O)** — promoted from
  "defer to Phase 4" to **the next benchmark that could plausibly
  show vectorization winning**. The disk-I/O path is the exact
  workload the architecture targets. Without it, every benchmark in
  Phase 3 is on vectorization's hardest possible adversary
  (in-memory, single-column, simple `int64` predicates, Go auto-SIMD).
- A new candidate: **string-predicate benchmark**. Add `StringEq`,
  re-run the compound test with one int64 + one string predicate.
  Per-row string compare is ~10× the cost of int64 compare and
  defeats auto-SIMD outright. This is a cheaper experiment than the
  disk path and should produce clean directional evidence.

## Honest summary

Two negative results in a row, both honestly reported. The compound
benchmark falsified the "selection vector reuse will save us"
prediction from the profiling doc — naive's branchless-then-branchy
curve (CSINC for one predicate, mispredicted branches for two) is so
non-monotone that no amount of selection-vector reuse closes the gap
on `int64` workloads with branch-predictor-friendly distributions. The architectural case for
vectorization remains intact (it is not built for this workload), but
**Phase 3 has not yet produced a benchmark in which the vectorized
engine wins**. The next step is the string-predicate benchmark
(cheap) before Phase 4's disk path (expensive).
