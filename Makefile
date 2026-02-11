.PHONY: lint lint-fix test test-fast test-parity test-exhaustive test-provenance ensure-libopus fixtures-gen fixtures-gen-decoder fixtures-gen-encoder fixtures-gen-variants build build-nopgo pgo-generate pgo-build clean clean-vectors

GO ?= go
PGO_FILE ?= default.pgo
PGO_BENCH ?= ^Benchmark(DecoderDecode|EncoderEncode)_(CELT|Hybrid|SILK|Stereo|MultiFrame|VoIP|LowDelay)$
PGO_PKG ?= .
PGO_BENCHTIME ?= 20s
PGO_COUNT ?= 1
LIBOPUS_VERSION ?= 1.6.1

# Run golangci-lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1; }
	golangci-lint run ./...

# Run golangci-lint with auto-fix
lint-fix:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1; }
	golangci-lint run --fix ./...

# Run tests
test:
	$(GO) test ./...

# Fast inner-loop tests (skips parity/exhaustive tier checks)
test-fast:
	$(GO) test -short ./...

# Parity tier (default for focused quality work)
test-parity:
	GOPUS_TEST_TIER=parity $(GO) test ./testvectors -run 'TestEncoderComplianceSummary|TestEncoderCompliancePrecisionGuard|TestDecoderParityLibopusMatrix|TestDecoderParityMatrixWithFFmpeg|TestEncoderVariantProfileParityAgainstLibopusFixture' -count=1

# Ensure tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo exists (fetch + build if missing).
ensure-libopus:
	LIBOPUS_VERSION=$(LIBOPUS_VERSION) ./tools/ensure_libopus.sh

# Exhaustive tier includes fixture honesty checks against tmp_check opus_demo/opusdec.
test-exhaustive: ensure-libopus
	GOPUS_TEST_TIER=exhaustive $(GO) test ./testvectors -run 'TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo1601|TestEncoderVariantsFixtureHonestyWithOpusDemo1601|TestDecoderParityMatrixFixtureHonestyWithOpusDemo1601|TestLongFrameReferenceFixtureHonestyWithLiveOpusdec' -count=1

# Exhaustive provenance audit for encoder variant parity.
test-provenance: ensure-libopus
	GOPUS_TEST_TIER=exhaustive $(GO) test ./testvectors -run 'TestEncoderVariantProfileProvenanceAudit' -count=1

# Regenerate fixture files from tmp_check/opus-1.6.1/opus_demo.
fixtures-gen: ensure-libopus fixtures-gen-decoder fixtures-gen-encoder fixtures-gen-variants

fixtures-gen-decoder:
	$(GO) run tools/gen_libopus_decoder_matrix_fixture.go

fixtures-gen-encoder:
	$(GO) run tools/gen_libopus_encoder_packet_fixture.go

fixtures-gen-variants:
	$(GO) run tools/gen_libopus_encoder_variants_fixture.go

# Build with profile-guided optimization (default.pgo auto-discovered by Go toolchain)
build:
	$(GO) build -pgo=auto ./...

# Build without profile-guided optimization
build-nopgo:
	$(GO) build -pgo=off ./...

# Regenerate default.pgo from decode hot-path benchmarks
pgo-generate:
	$(GO) test -run='^$$' -bench='$(PGO_BENCH)' -benchtime=$(PGO_BENCHTIME) -count=$(PGO_COUNT) -cpuprofile $(PGO_FILE) $(PGO_PKG)

# Refresh default.pgo then build with PGO enabled
pgo-build: pgo-generate build

# Remove local build/test artifacts generated during development.
clean:
	find . -maxdepth 1 -type f \( -name '*.test' -o -name '*.prof' -o -name '*.out' -o -name '*.o' -o -name '*.trace' -o -name 'coverage.out' -o -name 'coverage.html' \) -delete

# Remove downloaded official Opus test vectors cache.
clean-vectors:
	rm -rf testvectors/testdata/opus_testvectors/
