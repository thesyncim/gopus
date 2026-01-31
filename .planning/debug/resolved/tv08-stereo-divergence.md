---
status: resolved
trigger: "TV08 is failing with Q=-92.46 (catastrophic failure). Stereo decoder test with mode switching."
created: 2026-01-30T10:00:00Z
updated: 2026-01-30T10:45:00Z
---

## Current Focus

hypothesis: CONFIRMED - defer block in decodeMonoPacketToStereo restores prevEnergy after silence frame
test: Check that silence frame doesn't update prevEnergy persistently
expecting: After fixing, Frame 1 should see -28.0 energies and produce silence output
next_action: Fix decodeMonoPacketToStereo to NOT restore prevEnergy after silence, update with silence energies instead

## Symptoms

expected: Q > 0 (matching reference output)
actual: Q = -92.46 (catastrophic failure)
errors: Massive divergence from reference
reproduction: Run TV08 compliance test
started: Unknown

## Eliminated

## Evidence

- timestamp: 2026-01-30T10:05:00Z
  checked: TV08 packet-by-packet comparison
  found: First divergence at packet 208
  implication: Not SILK issue - divergence in CELT section

- timestamp: 2026-01-30T10:05:01Z
  checked: Packet 208 details
  found: CELT Config=27 (SWB), Stereo=false (MONO), FrameCount=2, FrameSize=960
  implication: Mono packet being decoded by stereo decoder

- timestamp: 2026-01-30T10:05:02Z
  checked: Packet 207 details
  found: CELT Config=21, Stereo=true
  implication: STEREO->MONO transition within CELT mode

- timestamp: 2026-01-30T10:05:03Z
  checked: Sample comparison at divergence
  found: First 10 samples match perfectly, divergence starts around sample 3330 (of 3840)
  implication: Divergence in second frame of 2-frame packet (each frame = 1920 stereo samples)

- timestamp: 2026-01-30T10:10:00Z
  checked: Packet 208 structure
  found: FrameCode=2 (2 different-sized frames), Frame1=2 bytes, Frame2=104 bytes
  implication: Very short first frame (likely silence), normal second frame

- timestamp: 2026-01-30T10:10:01Z
  checked: Frame-by-frame comparison
  found: Frame 0 (2-byte frame) matches perfectly. Frame 1 (104-byte frame) diverges massively
  implication: Second frame decoding is problematic

- timestamp: 2026-01-30T10:12:00Z
  checked: Frame 2 with fresh mono decoder
  found: Fresh mono decoder produces small values (near 0), reference also has small values (0 to -2)
  implication: Issue is STATE-dependent - previous stereo decoding corrupts state for mono frame

- timestamp: 2026-01-30T10:30:00Z
  checked: libopus silence handling vs gopus
  found: libopus sets oldBandE=-28 after silence. gopus's defer block restores d.prevEnergy to original value
  implication: Frame 1 sees wrong energy state (original instead of silence -28)

- timestamp: 2026-01-30T10:32:00Z
  checked: decodeMonoPacketToStereo silence path
  found: defer restores prevEnergy even after silence frame, which undoes the silence energy state
  implication: After silence frame, next frame gets wrong energy prediction

## Resolution

root_cause: In decodeMonoPacketToStereo, the silence path restores d.prevEnergy to origPrevEnergy but does NOT update it to silence energies (-28.0). In libopus, after a silence frame, oldBandE is set to -28.0 for all bands. This ensures subsequent frames see the correct energy state for prediction.
fix: Added code to set d.prevEnergy[i] = -28.0 for all bands after restoring origPrevEnergy in the silence path.
verification: TV08 Q improved from -92.46 to +32.69. Full compliance: 11/12 tests passing.
files_changed: [internal/celt/decoder.go]
