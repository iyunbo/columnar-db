# Phase 3 — String predicate benchmark

**Date**: 2026-04-07
**Branch**: `phase3/polish-string-predicate`
**Context**: The compound benchmark write-up
(`docs/phase3-compound-benchmark.md`) ended by promoting "add a
string predicate, retry the compound test" as the cheapest experiment
that might tilt the result toward vectorization. The argument was:
int64 predicates are vectorization's worst possible adversary because
naive's scalar inner loop compiles to `CSINC` and runs at ~6 IPC.
String equality is more expensive per row (length compare + memcmp),
so the relative weight of selection-vector materialization should
shrink, and vectorization should close the gap or even win.

**This document is the write-up. The hypothesis was wrong again, but
the negative result is more informative than the previous two.**

## The numbers

1 M-row in-memory fixture, query `age > 30 AND city = "Paris"`,
selectivity ~67 % × 20 % ≈ 13 %:

| Query                                                       | Naive    | Vectorized | gap   |
|-------------------------------------------------------------|---------:|-----------:|------:|
| `age > 30`                  (1 int)                         | 0.60 ms  | 2.76 ms    | 4.6×  |
| `30 < age < 60`             (2 int)                         | 4.07 ms  | 4.74 ms    | 1.18× |
| `30 < age < 60 ∧ ≠ 45`      (3 int)                         | 4.20 ms  | 5.29 ms    | 1.27× |
| **`age > 30 ∧ city = "Paris"` (1 int + 1 string)**          | **3.01 ms** | **4.26 ms** | **1.42×** |

Reproduction:
```
go test -bench='PipelineNaiveCount|PipelineVectorizedDrain' \
        -run=NONE -benchtime=2s ./exec/
```
Single run, Apple M4. See "Methodology & caveats" in the compound doc.

## What I expected vs what happened

**Expected**: string equality is ~10× the per-row cost of an `int64`
compare (length check + memcmp). The naive loop's bottleneck shifts
from "branch prediction tax" to "actual work in the predicate body";
the vectorized engine's selection-vector tax stays the same in
absolute terms. Relative gap closes; vectorization could plausibly
win.

**Reality**: naive's *string* compound query is **faster** (3.01 ms)
than its *int64* compound query (4.07 ms), not slower. The
hypothesis was the wrong sign.

The mechanism, once again, is branch prediction:

- `30 < age < 60` over uniform ages 0..89 → second branch (`a < 60`
  conditional on `a > 30`) is true for ~30/59 ≈ 51 % of inputs.
  **Worst case for the branch predictor.**
- `city = "Paris"` over a 5-element pool → second branch is true for
  ~1/5 = 20 % of inputs, with the overwhelmingly common answer being
  "no". **Highly predictable, near-zero misprediction cost.**

Plus, the actual string compare turns out to be cheap on this
fixture: the city pool is `{"Paris", "Lyon", "Kunming", "Shanghai",
"Beijing"}` and only one entry shares its first byte with `"Paris"`.
`memcmp` rejects after 1 byte for ~80 % of inputs. So the "10× more
expensive per row" assumption was also wrong — string equality is
~1× int64 equality on this distribution.

Vectorized did improve, but less than naive:

| Implementation | int64 compound (2-pred) | string compound (1 int + 1 str) | Δ        |
|----------------|------------------------:|---------------------------------:|---------:|
| Naive          | 4.07 ms                 | 3.01 ms                          | **−1.06 ms** |
| Vectorized     | 4.74 ms                 | 4.26 ms                          | **−0.48 ms** |

Naive improved by 1.06 ms because the string predicate's better
branch predictability removed most of the misprediction tax.
Vectorized improved by only 0.48 ms because **its bottleneck is
selection-vector materialization, which is unaffected by branch
prediction**. The string predicate helped both, but it helped naive
more, so the gap *widened* (1.18× → 1.42×).

## The pattern across all four benchmarks

Looking at the four benchmarks together, a clear pattern emerges:

> **Naive's performance is dominated by branch predictability.**
> Vectorized's performance is dominated by selection-vector
> materialization. Anything that improves branch predictability
> helps naive disproportionately. Anything that increases
> per-predicate fixed cost helps vectorized disproportionately —
> but in this benchmark family, no such workload exists yet.

The corollary: **the variable we thought would tilt toward
vectorization (predicate cost) is the wrong variable**. The actual
relevant variable is whether naive can get its inner loop into a
branch-predictor sweet spot. On this fixture, every query we tested
either uses `CSINC` (1 pred) or has a friendly second predicate
(string compound) or hits the predictor cliff (int64 compound).
Vectorization never wins because its baseline is ~2.7 ms regardless.

## What workloads would actually break this pattern

The benchmark family has now ruled out three plausible "vectorization
wins" hypotheses on Apple M4 in-memory `int64` + short string data:

1. ✗ Single int64 predicate (Phase 3 Step 6 — vec is 4.6× slower)
2. ✗ 2- and 3-predicate int64 chains (compound — vec narrows but
   never wins)
3. ✗ Mixed int64 + string predicate (this doc — gap *widens*
   compared to int64 compound, opposite of the prediction)

What remains:

1. **Per-row work that is genuinely expensive AND the result is hard
   to predict.** A regex predicate (`city LIKE 'P%n%'`), a JSON-path
   extraction, a function call. These can't be CSINC, can't be
   first-byte-rejected, can't fit in the M4 branch predictor's
   history. Untested — would need new predicate types.
2. **Disk-backed scans where decode dominates.** This is the core
   workload the architecture targets. Vectorization gets to amortize
   per-batch decode setup over 1024 rows; naive pays it per row.
   Expected to be the first benchmark where vectorization wins —
   but this is Phase 4 work and remains a bet, not a proof.
3. **Aggregations / joins.** Late materialization avoids row
   reconstruction at operator boundaries. Phase 5+.
4. **Hand-written SIMD inner loops in the predicate.** Would replace
   the entire `Int64Gt.Eval` body with NEON intrinsics. The cleanest
   experiment, the most invasive change. Out of scope for Phase 3.

The string-predicate benchmark was supposed to be the cheapest test
that could give a positive signal without changing predicate
internals or moving to disk. **It produced a clear negative signal**:
even when we hand-pick a workload that should favor vectorization
on its supposed strengths, vectorization loses, and it loses for a
reason that points to a deeper diagnosis (branch prediction is the
real variable, not predicate cost).

## Implication for Phase 3 / Phase 4

The bet on Phase 4 just got a bit riskier. The string-predicate
benchmark was meant to be a leading indicator — a cheap "yes" or
"no" before committing to disk-backed scans. We got "no". That
doesn't *disprove* the Phase 4 thesis (decode dominance is a
genuinely different regime, and vectorization is the standard
solution there), but it does mean **the Phase 3 evidence base for
"the architecture is going to win on the workloads it's designed
for" is purely theoretical at this point**. There is no Phase 3
benchmark in which the vectorized engine wins. Three negative
results, increasing in nuance.

Concrete recommendation:

- **Stop trying to make Phase 3 produce a positive number.** Every
  follow-up has narrowed the gap and produced a sharper diagnosis,
  but none has flipped the sign. Diminishing returns.
- **Move to Phase 4 with eyes open.** The Phase 4 design should
  *explicitly* identify the metric that would falsify the
  vectorization bet at the disk-I/O boundary, and commit to
  reporting it honestly even if it's negative.
- **Keep the architecture.** Even with three negative benchmarks,
  the operator-batch-selection-vector model is the right shape for
  what the database is trying to become. The cost of the model is
  bounded (~2.7 ms baseline overhead at 1 M rows in memory), and
  the upside in the target regime (disk + decode + aggregates)
  is real per the published literature. Reverting to row-at-a-time
  would forfeit the upside without saving the engineering already
  done.

## Honest summary

Three negative benchmarks in a row, each more carefully reasoned
than the last. The string-predicate benchmark was the cheapest
remaining experiment that could plausibly produce a positive signal
on Apple M4 in-memory data, and it failed. The mechanism turned out
to be the same one the compound benchmark uncovered — branch
prediction, not predicate cost — and the new evidence sharpens the
diagnosis without changing the bottom line.

The vectorized model's case in this codebase now rests entirely on
Phase 4+ workloads. Phase 3 has produced a clean architecture, a
zero-allocation hot path, an honest negative-result paper trail,
and exactly zero benchmarks in which the engine wins. Phase 4
either validates the bet or it doesn't.
