# gopus autoresearch

This repo now uses an `autoresearch`-style loop for codec work.

## Goal

Improve `gopus` with a libopus-parity-first mixed quality+feature loop while keeping the judge fixed.

The default target is:

- improve quality first, using libopus 1.6.1 parity as the reference
- close explicit libopus capability gaps when they are high-value, testable, and backed by a pinned judge
- close the fair `gopus` vs `libopus` speech encode throughput gap when the change is performance-facing
- preserve zero-allocation hot paths

## Management Lanes

Coordinate all researcher work under three top-level lanes:

- `performance`: measurable throughput, latency, or allocation improvements
- `libopus parity`: closer behavioral, quality, and supported-capability alignment with libopus 1.6.1
- `code quality / maintainability`: simpler structure, stronger tests, lower maintenance risk, and clearer ownership

The existing `autoresearch.sh` focus flags remain useful judge surfaces, but
claiming, queueing, and merge coordination should use these three lanes.

For the lane-to-`FOCUS` mapping:

- `performance` lane usually uses `FOCUS=performance`
- `libopus parity` lane usually uses `FOCUS=quality`; use `FOCUS=mixed` only when the slice closes an explicit libopus capability gap with a pinned judge
- `code quality / maintainability` lane should stay manual or tightly scoped unless the user provides a measurable target

## Setup

Start each run on a fresh branch such as `autoresearch/<tag>`.

Then:

1. Read `program.md`, `AGENTS.md`, and `README.md`.
2. Run `make autoresearch-init`.
3. Run `make autoresearch-preflight`.
4. Open the shared draft PR claim before any editable code change:

```bash
./tools/prepare_claim_pr.sh --lane performance --surface encoder --tag perf-try-1 --hypothesis "State the current idea here." --push --create-draft
```

If GitHub requires branch history before opening the draft PR, the helper creates a single empty claim commit so other workers can see the branch before editable work starts.

5. Run the baseline exactly once:

```bash
make autoresearch-eval DESCRIPTION=baseline
```

The baseline row becomes the first successful row in the focus-specific results ledger.

To let Codex drive repeated iterations automatically, run:

```bash
make autoresearch-loop MAX_ITERATIONS=5
```

Omit `MAX_ITERATIONS` to keep looping until interrupted.

When useful, split the loop into two read-only scout agents:

- one quality lane to inspect parity, compliance, and ratchet evidence
- one feature lane to inspect allowlisted unimplemented work with an explicit judge

Keep the main loop on one editable surface at a time.

## Parallel Research Workflow

When more than one researcher is active, use a shared claim surface before
editing. The preferred claim surface is an open draft PR.

Rules:

1. Every editable branch must have exactly one active claim.
2. The claim must name the lane, editable surface, owner, and current
   hypothesis.
3. Only one active editable claim may own a given `(lane, editable surface)`
   pair at a time.
4. If that pair is already claimed, switch to read-only scouting, review, or a
   different pair instead of starting overlapping edits.
5. Keep one researcher to one editable branch at a time.
6. If no shared claim surface exists, fall back to one editable researcher at a
   time.

Use draft PRs as the coordination queue:

- create the draft PR before any editable code change; use an empty claim commit if the branch needs visible history first
- keep the title generic and change-focused
- record the current blocker, next action, and latest attempt/results in the PR body
- close or retarget stale claims quickly so the queue stays trustworthy

## Editable Surface

Unlike the original `autoresearch` repo, `gopus` is not a single-file project.
Keep each run to one editable surface:

- `celt/`
- `encoder/`
- `silk/`
- `container/ogg/`
- one narrowly-scoped root wrapper/control surface directly supporting that area
- one narrowly-scoped benchmark or test helper directly supporting that surface

Do not spread one experiment across multiple subsystems unless the change is structurally required.

## Fixed Judge

Do not edit these during normal experiments:

- `program.md`
- `tools/autoresearch.sh`
- `tools/benchguard/main.go`
- `tools/bench_guardrails.json`
- `testvectors/testdata/`
- `tmp_check/`

Each experiment is judged in this order:

1. Focused quality/parity for `quality`, `mixed`, and `unimplemented`:

```bash
make test-quality
make test-compat
```

For `performance`, keep using:

```bash
GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
go test ./testvectors -run 'TestEncoderComplianceSummary' -count=1
```

2. Hot-path guardrails for every lane:

```bash
make bench-guard
```

3. Allowlisted unimplemented-feature checks for `mixed` and `unimplemented`:

```bash
GOWORK=off go test ./container/ogg -count=1
```

The current safe seed is `mix-arrivals-f32wav`.

4. Fair throughput comparison for `performance`:

```bash
GOWORK=off go run ./examples/bench-encode -in reports/autoresearch/speech.ogg -iters 2 -warmup 1 -mode both -bitrate 64000 -complexity 10
```

Use `make verify-production` only before proposing merge-ready changes, not on every loop.

## Results Ledger

The results ledgers are intentionally local and untracked.

- `results.tsv`: performance lane
- `results.quality.tsv`: quality lane
- `results.unimplemented.tsv`: allowlisted unimplemented-feature lane
- `results.mixed.tsv`: mixed lane

Performance header:

```text
commit	parity	benchguard	gopus_avg_rt	libopus_avg_rt	rt_ratio	status	description
```

Quality-like header:

```text
commit	quality	benchguard	quality_mean_gap_q	quality_min_gap_q	score	status	description
```

Status values:

- `baseline`
- `keep`
- `discard`
- `crash`

`rt_ratio` is `gopus_avg_rt / libopus_avg_rt` for the performance lane.
For the quality-like lanes, higher `score` is better.
The quality-like score combines the encoder compliance opus_compare gap summary
with the minimum `Hybrid->CELT` transition SNR emitted by `make test-compat`.

For the top-level management lanes:

- `performance` should prefer hard metrics and ledger rows
- `libopus parity` should prefer explicit target tests, fixtures, or
  side-by-side evidence against libopus
- `code quality / maintainability` may use qualitative evidence, but the PR must still name the
  concrete simplification, risk reduction, or test improvement being claimed

## Experiment Loop

Loop forever until the human stops you:

1. Inspect active claims and choose an unclaimed `(lane, editable surface)` pair.
2. Look at the current branch, HEAD commit, and the best successful row with `make autoresearch-best`.
3. Refresh the draft PR claim with the current blocker and next action before editing.
4. Make one idea-sized change inside the chosen surface.
5. Commit the experiment before evaluation.
6. Run:

```bash
make autoresearch-eval DESCRIPTION='short experiment note'
```

7. Read the appended row in the focus-specific results ledger.
8. Update the draft PR claim with the attempt description, latest result row, blocker, and next action.
9. If the status is `keep`, continue from that commit.
10. If the status is `discard`, rewind to the prior successful commit and try the next idea.
11. If the status is `crash`, fix only obvious mechanical mistakes; otherwise abandon the idea and move on.

If you want the repository to drive Codex directly instead of relying on a human-operated agent session, use:

```bash
make autoresearch-loop
```

## Decision Rule

Keep a change only when all of these are true:

- the lane's required quality/parity checks pass
- `bench-guard` passes
- the lane's score improves:
  - `rt_ratio` for `performance`
  - quality score for `quality`
  - allowlisted feature score for `unimplemented`
  - mixed score for `mixed`

If results are effectively flat, prefer the simpler change.

## Merge Coordination

Use a single merge steward or an explicit sequential merge queue.

Rules:

1. Merge only one green experimental slice at a time.
2. Prefer the oldest unblocked green PR unless a later PR is explicitly
   dependent on an earlier foundational slice.
3. Before merge, rebase onto the current queue head and rerun the lane's named
   evidence:
   - `performance`: the relevant benchmark or ledger-backed judge
   - `libopus parity`: the targeted parity, capability, or compatibility checks against libopus
   - `code quality / maintainability`: the targeted tests plus the structural evidence named in
     the PR
4. Run `make bench-guard` before merge when the change touches a hot path.
5. After any merge, every open PR touching the same surface or shared helper
   must rebase and revalidate before it can merge.
6. Do not batch-merge multiple experiments together.

## Notes

- libopus 1.6.1 in `tmp_check/opus-1.6.1/` remains the source of truth.
- Avoid heuristic codec tuning before source alignment.
- Do not treat raw `ErrUnimplemented` stubs as loop targets unless a pinned judge exists.
- Prefer short loops over large refactors.
