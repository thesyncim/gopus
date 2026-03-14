# gopus autoresearch

This repo now uses an `autoresearch`-style loop for codec work.

## Goal

Improve `gopus` while keeping the judge fixed.

The default target is:

- close the fair `gopus` vs `libopus` speech encode throughput gap
- preserve libopus 1.6.1 parity and quality
- preserve zero-allocation hot paths

## Setup

Start each run on a fresh branch such as `autoresearch/<tag>`.

Then:

1. Read `program.md`, `AGENTS.md`, and `README.md`.
2. Run `make autoresearch-init`.
3. Run `make autoresearch-preflight`.
4. Run the baseline exactly once:

```bash
make autoresearch-eval DESCRIPTION=baseline
```

The baseline row becomes the first successful row in `results.tsv`.

To let Codex drive repeated iterations automatically, run:

```bash
make autoresearch-loop MAX_ITERATIONS=5
```

Omit `MAX_ITERATIONS` to keep looping until interrupted.

## Editable Surface

Unlike the original `autoresearch` repo, `gopus` is not a single-file project.
Keep each run to one editable surface:

- `celt/`
- `encoder/`
- `silk/`
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

1. Focused parity:

```bash
GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
go test ./testvectors -run 'TestSILKParamTraceAgainstLibopus|TestEncoderComplianceSummary' -count=1
```

2. Hot-path guardrails:

```bash
make bench-guard
```

3. Fair throughput comparison:

```bash
GOWORK=off go run ./examples/bench-encode -in reports/autoresearch/speech.ogg -iters 2 -warmup 1 -mode both -bitrate 64000 -complexity 10
```

Use `make verify-production` only before proposing merge-ready changes, not on every loop.

## Results Ledger

`results.tsv` is intentionally local and untracked.

Header:

```text
commit	parity	benchguard	gopus_avg_rt	libopus_avg_rt	rt_ratio	status	description
```

Status values:

- `baseline`
- `keep`
- `discard`
- `crash`

`rt_ratio` is `gopus_avg_rt / libopus_avg_rt`.
Higher is better.

## Experiment Loop

Loop forever until the human stops you:

1. Look at the current branch, HEAD commit, and the best successful row with `make autoresearch-best`.
2. Make one idea-sized change inside the chosen surface.
3. Commit the experiment before evaluation.
4. Run:

```bash
make autoresearch-eval DESCRIPTION='short experiment note'
```

5. Read the appended row in `results.tsv`.
6. If the status is `keep`, continue from that commit.
7. If the status is `discard`, rewind to the prior successful commit and try the next idea.
8. If the status is `crash`, fix only obvious mechanical mistakes; otherwise abandon the idea and move on.

If you want the repository to drive Codex directly instead of relying on a human-operated agent session, use:

```bash
make autoresearch-loop
```

## Decision Rule

Keep a change only when all of these are true:

- focused parity passes
- `bench-guard` passes
- the throughput ratio improves

If results are effectively flat, prefer the simpler change.

## Notes

- libopus 1.6.1 in `tmp_check/opus-1.6.1/` remains the source of truth.
- Avoid heuristic codec tuning before source alignment.
- Prefer short loops over large refactors.
