# CGO Test Suite

This directory contains integration tests that compare gopus against the reference libopus C implementation via CGO bindings.

## Directory Structure

### Wrapper Files (CGO Bindings)
- `libopus_wrapper.go` - Main libopus encoder/decoder bindings
- `intermediate_wrappers.go` - Intermediate computation helpers
- `mdct_wrapper.go` - MDCT transform wrappers
- `silk_gain_wrapper.go` - SILK gain computation
- `libopus_resampler_wrapper.go` - Resampler bindings
- `smlawb_wrapper.go` - Fixed-point multiply-accumulate

### Test Categories

#### Compliance Tests
- `encoder_compliance_test.go` - Encoder quality metrics (Q >= 0 for pass)
- `libopus_compliance_test.go` - RFC 8251 compliance validation

#### Roundtrip Tests
- `encode_decode_roundtrip_test.go` - Full encode/decode cycle
- `gopus_roundtrip_test.go` - gopus-specific roundtrip

#### Comparison Tests (`*_compare_test.go`, `*_libopus_test.go`)
Compare gopus output against libopus for specific components:
- Energy computation
- MDCT transforms
- PVQ encoding
- Range encoder state
- SILK/CELT parameters

#### Test Vector Tests (`tv*_test.go`)
Validate against official Opus test vectors (RFC 8251):
- `tv02_*` - Test vector 02 (SILK narrowband)
- `tv12_*` - Test vector 12 (Hybrid mode transitions)

#### Component Tests
Organized by subsystem:
- `silk_*_test.go` - SILK encoder/decoder tests
- `stereo_*_test.go` - Stereo processing tests
- `transient_*_test.go` - Transient detection tests
- `header_*_test.go` - Packet header encoding
- `postfilter_*_test.go` - Post-filter validation

### Test Helpers
- `encoder_test_helpers_test.go` - Shared test utilities and configurations

## Running Tests

```bash
# Run all cgo tests (requires libopus)
go test ./internal/celt/cgo_test/...

# Run compliance tests only
go test ./internal/celt/cgo_test/... -run "Compliance"

# Run with verbose output
go test ./internal/celt/cgo_test/... -v

# Skip long-running tests
go test ./internal/celt/cgo_test/... -short
```

## Requirements

- libopus development headers and library
- CGO enabled (`CGO_ENABLED=1`)
- pkg-config (for finding libopus)
