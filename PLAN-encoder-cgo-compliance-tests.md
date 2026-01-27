# Encoder CGO Compliance Test Suite

## Status: IMPLEMENTED ✓

The CGO-based encoder compliance tests are now in place. They currently show that the gopus encoder is still in development (0/80 configurations pass). As the encoder improves, these tests will start passing and serve as a quality baseline.

## Goal

Create a CGO-based encoder compliance test suite that uses libopus as the reference decoder to verify gopus encoder output quality.

## Why CGO Instead of opusdec CLI?

- **No external tool dependency** - tests run without installing opusdec
- **Direct integration** - uses existing CGO infrastructure in `internal/celt/cgo_test/`
- **No Ogg container overhead** - encode raw packets, decode directly
- **Faster execution** - no process spawning or file I/O

---

## File Structure

```
internal/celt/cgo_test/
├── libopus_wrapper.go              # EXISTING - has NewLibopusDecoder, DecodeFloat
├── encoder_compliance_test.go      # NEW - main test functions
└── encoder_test_helpers_test.go    # NEW - signal generation, utilities
```

All new files go in the existing `cgo_test` package to reuse CGO setup.

---

## Implementation Plan

### File 1: `encoder_test_helpers_test.go`

#### Types

```go
// EncoderTestConfig defines a single encoder compliance test case
type EncoderTestConfig struct {
    Name      string
    Mode      string  // "SILK", "CELT", "Hybrid"
    Bandwidth string  // "NB", "MB", "WB", "SWB", "FB"
    FrameSize int     // samples at 48kHz: 120, 240, 480, 960, 1920, 2880
    Channels  int     // 1 or 2
    Bitrate   int     // bps
}

// EncoderTestResult holds the outcome of a single test
type EncoderTestResult struct {
    Config       EncoderTestConfig
    Quality      float64       // Q metric (Q >= 0 = pass)
    SNR          float64       // dB
    Passed       bool
    TotalSamples int
    EncodedBytes int
    Error        error
}
```

#### Signal Generation Functions

```go
// generateMultiFrequencySignal creates a test signal with multiple tones
// covering the frequency range appropriate for the bandwidth
func generateMultiFrequencySignal(samples, channels int, freqs []float64) []float32

// frequenciesForBandwidth returns test frequencies appropriate for bandwidth
// NB: 200, 400, 800 Hz (up to 4kHz audio)
// MB: 200, 500, 1200 Hz (up to 6kHz audio)
// WB: 300, 800, 2000 Hz (up to 8kHz audio)
// SWB: 400, 1000, 3000, 6000 Hz (up to 12kHz audio)
// FB: 440, 1000, 2000, 5000, 10000 Hz (up to 20kHz audio)
func frequenciesForBandwidth(bandwidth string) []float64
```

#### Encoding/Decoding Pipeline

```go
// encodeSignal encodes a signal using gopus encoder with given config
func encodeSignal(signal []float32, cfg EncoderTestConfig) ([][]byte, error)

// decodeWithLibopus decodes packets using libopus CGO decoder
func decodeWithLibopus(packets [][]byte, sampleRate, channels int) ([]float32, error)

// compareAudio computes quality metrics, handling pre-skip alignment
func compareAudio(original, decoded []float32, sampleRate int) (q, snr float64)
```

#### Configuration Builders

```go
// buildSILKConfigs returns test configurations for SILK mode
func buildSILKConfigs() []EncoderTestConfig

// buildCELTConfigs returns test configurations for CELT mode
func buildCELTConfigs() []EncoderTestConfig

// buildHybridConfigs returns test configurations for Hybrid mode
func buildHybridConfigs() []EncoderTestConfig

// buildAllConfigs returns all test configurations
func buildAllConfigs() []EncoderTestConfig
```

---

### File 2: `encoder_compliance_test.go`

#### Test Functions

```go
// TestEncoderComplianceSILK_CGO tests SILK mode encoder
func TestEncoderComplianceSILK_CGO(t *testing.T) {
    configs := buildSILKConfigs()
    for _, cfg := range configs {
        t.Run(cfg.Name, func(t *testing.T) {
            result := runEncoderTest(t, cfg)
            if !result.Passed {
                t.Errorf("Q=%.2f (threshold: 0.0)", result.Quality)
            }
        })
    }
}

// TestEncoderComplianceCELT_CGO tests CELT mode encoder
func TestEncoderComplianceCELT_CGO(t *testing.T)

// TestEncoderComplianceHybrid_CGO tests Hybrid mode encoder
func TestEncoderComplianceHybrid_CGO(t *testing.T)

// TestEncoderComplianceSummary_CGO runs all configs and prints summary table
func TestEncoderComplianceSummary_CGO(t *testing.T)
```

#### Core Test Runner

```go
func runEncoderTest(t *testing.T, cfg EncoderTestConfig) EncoderTestResult {
    // 1. Generate 1 second of test signal
    sampleRate := 48000
    totalSamples := sampleRate * cfg.Channels
    freqs := frequenciesForBandwidth(cfg.Bandwidth)
    signal := generateMultiFrequencySignal(totalSamples, cfg.Channels, freqs)

    // 2. Encode with gopus
    packets, err := encodeSignal(signal, cfg)
    if err != nil {
        return EncoderTestResult{Config: cfg, Error: err}
    }

    // 3. Decode with libopus
    decoded, err := decodeWithLibopus(packets, sampleRate, cfg.Channels)
    if err != nil {
        return EncoderTestResult{Config: cfg, Error: err}
    }

    // 4. Strip pre-skip (312 samples * channels)
    preSkip := 312 * cfg.Channels
    if len(decoded) > preSkip {
        decoded = decoded[preSkip:]
    }

    // 5. Compute quality
    q, snr := compareAudio(signal, decoded, sampleRate)

    return EncoderTestResult{
        Config:   cfg,
        Quality:  q,
        SNR:      snr,
        Passed:   q >= 0.0, // 48 dB threshold
    }
}
```

---

## Test Coverage Matrix

### SILK Mode (configs 0-11)

| Bandwidth | Frame Sizes | Channels | Bitrates |
|-----------|-------------|----------|----------|
| NB (4kHz) | 10, 20, 40, 60ms | mono, stereo | 12k, 24k |
| MB (6kHz) | 10, 20, 40, 60ms | mono, stereo | 16k, 24k |
| WB (8kHz) | 10, 20, 40, 60ms | mono, stereo | 24k, 32k |

### CELT Mode (configs 16-31)

| Bandwidth | Frame Sizes | Channels | Bitrates |
|-----------|-------------|----------|----------|
| FB (20kHz) | 2.5, 5, 10, 20ms | mono, stereo | 64k, 128k |

### Hybrid Mode (configs 12-15)

| Bandwidth | Frame Sizes | Channels | Bitrates |
|-----------|-------------|----------|----------|
| SWB (12kHz) | 10, 20ms | mono, stereo | 48k, 64k |
| FB (20kHz) | 10, 20ms | mono, stereo | 64k, 96k |

---

## Summary Output Format

```
Encoder Compliance Summary (libopus reference decoder)
======================================================
Configuration                       |      Q |  SNR(dB) | Status
------------------------------------|--------|----------|--------
SILK-NB-20ms-mono-12k               |  12.50 |    54.00 | PASS
SILK-WB-20ms-mono-32k               |  18.75 |    57.00 | PASS
CELT-FB-20ms-mono-64k               |  25.00 |    60.00 | PASS
CELT-FB-10ms-stereo-128k            |  22.50 |    58.80 | PASS
Hybrid-SWB-20ms-mono-48k            |  15.00 |    55.20 | PASS
------------------------------------|--------|----------|--------
Total: 24/24 passed (100.0%)

Thresholds: PASS >= 0.0 (48 dB SNR)
```

---

## Integration Points

### Existing CGO Infrastructure

Uses `NewLibopusDecoder` and `DecodeFloat` from `libopus_wrapper.go`:

```go
libDec, err := NewLibopusDecoder(48000, channels)
if err != nil {
    return result
}
defer libDec.Destroy()

for _, packet := range packets {
    samples, n := libDec.DecodeFloat(packet, frameSize)
    decoded = append(decoded, samples[:n]...)
}
```

### Quality Metrics

Import from existing `internal/testvectors/quality.go`:

```go
import "github.com/thesyncim/gopus/internal/testvectors"

q := testvectors.ComputeQualityFloat32(decoded, original, sampleRate)
snr := testvectors.SNRFromQuality(q)
passed := testvectors.QualityPasses(q)
```

### Gopus Encoder

Uses public API from `encoder.go`:

```go
import gopus "github.com/thesyncim/gopus"

enc, _ := gopus.NewEncoder(48000, channels, gopus.ApplicationAudio)
enc.SetBitrate(cfg.Bitrate)
enc.SetFrameSize(cfg.FrameSize)

data := make([]byte, 1275) // max packet size
for offset := 0; offset < len(signal); offset += cfg.FrameSize * channels {
    frame := signal[offset : offset+cfg.FrameSize*channels]
    n, _ := enc.Encode(frame, data)
    packets = append(packets, data[:n])
}
```

---

## Verification

After implementation, run:

```bash
# Run all encoder CGO compliance tests
go test ./internal/celt/cgo_test -run "EncoderCompliance" -v

# Run just the summary
go test ./internal/celt/cgo_test -run "TestEncoderComplianceSummary_CGO" -v

# Run specific mode
go test ./internal/celt/cgo_test -run "TestEncoderComplianceCELT_CGO" -v
```

Expected results:
- All configurations should have Q >= 0.0 (48 dB SNR)
- CELT mode typically achieves Q > 20 (57+ dB)
- SILK mode at low bitrates may be closer to threshold

---

## Key Design Decisions

1. **Table-driven tests** - Easy to add new configurations
2. **Separate helper file** - Keeps test functions readable
3. **Bandwidth-appropriate frequencies** - Tests actual codec range
4. **Pre-skip handling** - Proper Opus decoder alignment (312 samples)
5. **Reuse existing code** - CGO wrapper, quality metrics
6. **Clear output format** - Summary table for quick assessment
