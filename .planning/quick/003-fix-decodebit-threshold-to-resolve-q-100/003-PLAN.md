---
quick: 003
type: execute
title: Fix DecodeBit threshold to resolve Q=-100
files_modified:
  - internal/rangecoding/decoder.go
  - internal/rangecoding/decoder_test.go
autonomous: true

must_haves:
  truths:
    - "DecodeBit returns 0 for silence flag on non-silent frames"
    - "CELT decoder produces non-zero audio output"
    - "Encoder-decoder bit round-trip still works"
  artifacts:
    - path: "internal/rangecoding/decoder.go"
      provides: "Fixed DecodeBit() with correct threshold"
      contains: "d.rng - r"
  key_links:
    - from: "internal/rangecoding/decoder.go"
      to: "internal/celt/decoder.go"
      via: "DecodeBit(15) for silence flag"
      pattern: "DecodeBit\\(15\\)"
---

<objective>
Fix the DecodeBit() threshold comparison bug in the range decoder that causes all CELT frames to be incorrectly identified as silence.

Purpose: Root cause of Q=-100 is that DecodeBit() uses `val >= r` when it should use `val >= (rng - r)`. The '1' probability region is at the TOP of the range per RFC 6716, not at the bottom.

Output: Working CELT decoder that produces actual audio output instead of silence.
</objective>

<execution_context>
@~/.claude/get-shit-done/workflows/execute-plan.md
@~/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/phases/15-celt-decoder-quality/15-11-SUMMARY.md

Reference files:
@internal/rangecoding/decoder.go (lines 148-165 - DecodeBit function)
@internal/rangecoding/roundtrip_test.go (encoder-decoder tests)
</context>

<tasks>

<task type="auto">
  <name>Task 1: Fix DecodeBit threshold calculation</name>
  <files>internal/rangecoding/decoder.go</files>
  <action>
  Modify the DecodeBit() function (lines 152-165) to fix the threshold comparison:

  Current (WRONG):
  ```go
  func (d *Decoder) DecodeBit(logp uint) int {
      r := d.rng >> logp
      if d.val >= r {
          // Bit is 1
          d.val -= r
          d.rng -= r
          d.normalize()
          return 1
      }
      // Bit is 0
      d.rng = r
      d.normalize()
      return 0
  }
  ```

  Correct:
  ```go
  func (d *Decoder) DecodeBit(logp uint) int {
      r := d.rng >> logp
      threshold := d.rng - r  // '1' probability region is at TOP of range
      if d.val >= threshold {
          // Bit is 1 (rare case - val is in top 1/2^logp of range)
          d.val -= threshold
          d.rng = r
          d.normalize()
          return 1
      }
      // Bit is 0 (common case - val is in bottom (2^logp - 1)/2^logp of range)
      d.rng = threshold
      d.normalize()
      return 0
  }
  ```

  Key changes:
  1. Compute threshold as (rng - r) instead of using r directly
  2. When bit=1: subtract threshold from val, set rng = r
  3. When bit=0: set rng = threshold (not r)

  Per RFC 6716 Section 4.1, the probability regions are:
  - [0, rng-r): bit = 0, probability = (2^logp - 1) / 2^logp
  - [rng-r, rng): bit = 1, probability = 1 / 2^logp

  For silence flag (logp=15): P(silence=1) = 1/32768, which is very rare.
  </action>
  <verify>Run `go build ./...` to verify compilation</verify>
  <done>DecodeBit() uses correct threshold calculation with rng - r</done>
</task>

<task type="auto">
  <name>Task 2: Verify round-trip tests still pass</name>
  <files>internal/rangecoding/decoder_test.go, internal/rangecoding/roundtrip_test.go</files>
  <action>
  Run the existing range coder tests to verify the fix is correct:

  ```bash
  go test -v ./internal/rangecoding/... -run "Bit|ICDF"
  ```

  These tests encode and decode bits/symbols and verify round-trip correctness. They should still pass after the fix because the encoder already uses the correct logic.

  If any tests fail, the fix may need adjustment. The encoder's EncodeBit should be symmetric with the decoder's DecodeBit.

  Key tests to verify:
  - TestEncodeDecodeBitRoundTrip
  - TestEncodeDecodeMultipleBitsRoundTrip
  - TestDecodeBit
  </action>
  <verify>Run `go test -v ./internal/rangecoding/... -run "Bit|ICDF"` - all tests pass</verify>
  <done>All range coder bit tests pass, confirming encoder-decoder symmetry</done>
</task>

<task type="auto">
  <name>Task 3: Verify CELT decoder produces audio</name>
  <files>internal/celt/libopus_comparison_test.go</files>
  <action>
  Run the CELT comparison test to verify the fix resolves Q=-100:

  ```bash
  go test -v ./internal/celt/... -run "Divergence"
  ```

  After the fix:
  - DecodeBit(15) should return 0 for non-silent frames (not 1)
  - The test will document improved behavior
  - Silence flag should now match expected RFC 6716 semantics

  Also run the full compliance test to check quality improvement:

  ```bash
  go test -v ./internal/testvectors/... -run "Compliance" -count=1
  ```

  The quality score should improve from Q=-100 toward positive values as the decoder now processes actual audio instead of treating all frames as silence.
  </action>
  <verify>Run `go test -v ./internal/testvectors/... -run "Compliance" -count=1` and check Q values</verify>
  <done>CELT decoder produces non-zero output, Q values improve from -100</done>
</task>

</tasks>

<verification>
1. `go build ./...` compiles without errors
2. `go test ./internal/rangecoding/...` all tests pass
3. `go test ./internal/celt/...` divergence test shows DecodeBit(15) returning 0
4. `go test ./internal/testvectors/... -run Compliance` shows improved Q scores
</verification>

<success_criteria>
- DecodeBit() uses threshold = rng - r (correct per RFC 6716)
- Encoder-decoder bit round-trip tests pass
- CELT silence flag returns 0 for non-silent test vectors
- Quality metric Q improves from -100 toward positive values
</success_criteria>

<output>
After completion, create `.planning/quick/003-fix-decodebit-threshold-to-resolve-q-100/003-SUMMARY.md`

Update `.planning/STATE.md`:
- Move "Fix DecodeBit()" from Pending Todos to completed
- Update "Decoder Q=-100" gap status based on test results
</output>
