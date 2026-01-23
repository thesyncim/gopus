---
phase: quick-002
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - examples/ffmpeg-interop/main.go
  - examples/roundtrip/main.go
  - examples/ogg-file/main.go
  - examples/README.md
autonomous: true

must_haves:
  truths:
    - "User can run examples/ffmpeg-interop to encode Ogg Opus files readable by ffmpeg"
    - "User can run examples/roundtrip to validate encode-decode signal quality"
    - "User can run examples/ogg-file to create standard Ogg Opus files"
  artifacts:
    - path: "examples/ffmpeg-interop/main.go"
      provides: "FFmpeg interop example with encode/decode validation"
    - path: "examples/roundtrip/main.go"
      provides: "Roundtrip validation with SNR measurement"
    - path: "examples/ogg-file/main.go"
      provides: "Ogg Opus file creation example"
    - path: "examples/README.md"
      provides: "Documentation for all examples"
  key_links:
    - from: "examples/ffmpeg-interop/main.go"
      to: "container/ogg"
      via: "ogg.NewWriter for FFmpeg-compatible output"
    - from: "examples/roundtrip/main.go"
      to: "gopus.Encoder/Decoder"
      via: "encode then decode same samples"
---

<objective>
Create practical example programs demonstrating gopus usage with ffmpeg interoperability and roundtrip validation.

Purpose: Provide real-world usage patterns that users can run immediately to verify gopus works with their tooling (ffmpeg) and understand quality characteristics.

Output: Three standalone example programs in examples/ directory with comprehensive documentation.
</objective>

<execution_context>
@~/.claude/get-shit-done/workflows/execute-plan.md
@~/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/STATE.md
@encoder.go
@decoder.go
@stream.go
@container/ogg/writer.go
@container/ogg/reader.go
</context>

<tasks>

<task type="auto">
  <name>Task 1: Create FFmpeg interop example</name>
  <files>examples/ffmpeg-interop/main.go</files>
  <action>
Create `examples/ffmpeg-interop/main.go` that demonstrates:

1. **Encode with gopus, verify with ffmpeg:**
   - Generate a test signal (440Hz sine wave, 2 seconds, stereo)
   - Encode to Ogg Opus using gopus encoder + container/ogg
   - Write to output.opus file
   - Print ffprobe command to verify the file
   - Print ffplay command to play the file

2. **Encode with ffmpeg, decode with gopus:**
   - Print instructions to create test.opus with ffmpeg
   - Read test.opus (if exists) using container/ogg reader
   - Decode with gopus decoder
   - Print sample count and basic stats

Include clear comments explaining each step. Use flag package for output filename.

Key API usage:
- `ogg.NewWriter(file, 48000, 2)` for Ogg container
- `gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)` for encoding
- `gopus.NewDecoder(48000, 2)` for decoding
- `ogg.NewReader(file)` for reading Ogg files

Error handling: Log fatal on errors for simplicity (example code).
  </action>
  <verify>
    go build ./examples/ffmpeg-interop && ./examples/ffmpeg-interop/ffmpeg-interop -out /tmp/test.opus
    ls -la /tmp/test.opus
  </verify>
  <done>FFmpeg interop example compiles and creates valid Ogg Opus file</done>
</task>

<task type="auto">
  <name>Task 2: Create roundtrip validation example</name>
  <files>examples/roundtrip/main.go</files>
  <action>
Create `examples/roundtrip/main.go` that demonstrates:

1. **Signal generation:**
   - Generate various test signals: sine wave, white noise, sweep
   - Support different configurations via flags: sample rate, channels, duration

2. **Encode-decode roundtrip:**
   - Encode the generated signal with gopus
   - Decode immediately back to PCM
   - Compare original vs decoded

3. **Quality metrics:**
   - Calculate Signal-to-Noise Ratio (SNR)
   - Calculate peak error
   - Calculate correlation coefficient
   - Print human-readable quality report

4. **Multiple modes:**
   - Test VoIP mode (SILK optimized)
   - Test Audio mode (CELT/Hybrid)
   - Test different bitrates (32k, 64k, 128k)

Key implementation:
```go
func calculateSNR(original, decoded []float32) float64 {
    var signal, noise float64
    for i := range original {
        signal += float64(original[i] * original[i])
        diff := original[i] - decoded[i]
        noise += float64(diff * diff)
    }
    if noise == 0 {
        return math.Inf(1)
    }
    return 10 * math.Log10(signal / noise)
}
```

Include flag support for: -rate (sample rate), -channels (1 or 2), -duration (seconds), -bitrate (bps).
  </action>
  <verify>
    go build ./examples/roundtrip && ./examples/roundtrip/roundtrip -duration 1
  </verify>
  <done>Roundtrip example compiles and prints quality metrics</done>
</task>

<task type="auto">
  <name>Task 3: Create Ogg file example and README</name>
  <files>examples/ogg-file/main.go, examples/README.md</files>
  <action>
**Part A: Create `examples/ogg-file/main.go`:**

Simple example showing Ogg Opus file I/O:
1. Create Ogg Opus file from generated audio
2. Read back and decode
3. Show duration, sample count, file size
4. Demonstrate seeking (if supported) or note it's not implemented

Use case: User wants to create podcast-style audio files.

Flags: -out (output file), -duration (seconds), -bitrate (bps)

**Part B: Create `examples/README.md`:**

Document all three examples:

```markdown
# gopus Examples

Practical examples demonstrating gopus usage patterns.

## Prerequisites

- Go 1.21+
- ffmpeg/ffprobe (for interop example validation)

## Examples

### ffmpeg-interop

Demonstrates interoperability with ffmpeg tooling.

[usage, what it does, expected output]

### roundtrip

Validates encode-decode quality with metrics.

[usage, what it does, expected output]

### ogg-file

Creates and reads Ogg Opus files.

[usage, what it does, expected output]

## Building

go build ./examples/ffmpeg-interop
go build ./examples/roundtrip
go build ./examples/ogg-file

## Notes

- gopus is in active development; quality metrics may vary
- See Known Gaps in .planning/STATE.md for current limitations
```
  </action>
  <verify>
    go build ./examples/ogg-file && ./examples/ogg-file/ogg-file -out /tmp/test2.opus -duration 1
    cat examples/README.md | head -30
  </verify>
  <done>Ogg file example compiles and README documents all examples</done>
</task>

</tasks>

<verification>
All examples compile and run:
```bash
go build ./examples/...
./examples/ffmpeg-interop/ffmpeg-interop -out /tmp/interop.opus
./examples/roundtrip/roundtrip -duration 1
./examples/ogg-file/ogg-file -out /tmp/ogg.opus
```

FFmpeg can read gopus output (if ffprobe available):
```bash
ffprobe /tmp/interop.opus 2>&1 | grep -i opus
```
</verification>

<success_criteria>
- [ ] examples/ffmpeg-interop/main.go exists and compiles
- [ ] examples/roundtrip/main.go exists and compiles
- [ ] examples/ogg-file/main.go exists and compiles
- [ ] examples/README.md documents all examples
- [ ] All examples run without panics
- [ ] FFmpeg interop example creates valid Ogg Opus files
- [ ] Roundtrip example prints quality metrics
</success_criteria>

<output>
After completion, create `.planning/quick/002-add-example-projects-with-ffmpeg-interop/002-SUMMARY.md`
</output>
