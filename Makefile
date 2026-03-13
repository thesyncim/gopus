.PHONY: lint lint-fix test test-fast test-race test-race-parity test-fuzz-smoke test-fuzz-safety test-parity test-exhaustive test-provenance test-assembly-safety test-soak-safety bench-guard agent-preflight agent-claims agent-claim agent-release verify-production verify-production-exhaustive verify-safety release-evidence ensure-libopus fixtures-gen fixtures-gen-decoder fixtures-gen-decoder-loss fixtures-gen-encoder fixtures-gen-variants fixtures-gen-amd64 docker-buildx-bootstrap docker-build docker-build-exhaustive docker-test docker-test-exhaustive docker-shell build build-nopgo pgo-generate pgo-build clean clean-vectors bench-kernels

GO ?= go
GO_WORK_ENV ?= GOWORK=off
GO_RUNNABLE_TEST ?= bash ./tools/run_go_test_runnable.sh
ASSEMBLY_SAFETY_MATRIX ?= bash ./tools/run_assembly_safety_matrix.sh
PGO_FILE ?= default.pgo
PGO_BENCH ?= ^Benchmark(DecoderDecode|EncoderEncode)_(CELT|Hybrid|SILK|Stereo|MultiFrame|VoIP|LowDelay)$
PGO_PKG ?= .
PGO_BENCHTIME ?= 20s
PGO_COUNT ?= 1
LIBOPUS_VERSION ?= 1.6.1
DOCKER_IMAGE ?= gopus-ci
DOCKERFILE_CI ?= Dockerfile.ci
DOCKER_DISABLE_OPUSDEC ?= 0
DOCKER_DISABLE_OPUSENC ?= 1
DOCKER_CACHE_DIR ?= .docker-cache
DOCKER_BUILDER ?= gopus-buildx
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_M),arm64)
DOCKER_PLATFORM ?= linux/arm64
else ifeq ($(UNAME_M),aarch64)
DOCKER_PLATFORM ?= linux/arm64
else
DOCKER_PLATFORM ?= linux/amd64
endif
DOCKER_CACHE_SUFFIX := $(subst /,-,$(DOCKER_PLATFORM))
DOCKER_EXHAUSTIVE_PLATFORM ?= linux/amd64
DOCKER_EXHAUSTIVE_CACHE_SUFFIX := $(subst /,-,$(DOCKER_EXHAUSTIVE_PLATFORM))
DOCKER_BUILDX_CACHE_DIR := $(DOCKER_CACHE_DIR)/buildx-$(DOCKER_CACHE_SUFFIX)
DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR := $(DOCKER_CACHE_DIR)/buildx-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX)
RELEASE_EVIDENCE_DIR ?= reports/release
GOPUS_SAFETY_FUZZTIME ?= 12s
GOPUS_SAFETY_PARSER_FUZZTIME ?= $(GOPUS_SAFETY_FUZZTIME)
GOPUS_SAFETY_SOAK_DURATION ?= 30s
GOPUS_SAFETY_SOAK_REPORT_INTERVAL ?= 10s
GOPUS_SAFETY_SOAK_MAX_RSS_GROWTH_MIB ?= 256
GOPUS_SAFETY_SOAK_MAX_GOROUTINE_GROWTH ?= 16
GOPUS_SAFETY_SOAK_MAX_ALLOCS ?= 0.0

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
	$(GO_RUNNABLE_TEST)

# Fast inner-loop tests (skips parity/exhaustive tier checks)
test-fast:
	$(GO_RUNNABLE_TEST) -short

# Race detector sweep across all packages at fast test tier (keeps runtime bounded).
test-race:
	GOPUS_TEST_TIER=fast $(GO_RUNNABLE_TEST) -race -count=1 -timeout=20m

# Deeper race sweep at parity tier.
test-race-parity:
	GOPUS_TEST_TIER=parity $(GO_RUNNABLE_TEST) -race -count=1 -timeout=30m

# Fuzz smoke run for packet/fixture parsers.
test-fuzz-smoke:
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzParsePacket_NoPanic' -fuzztime=10s -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzParseOpusDemoBitstream' -fuzztime=10s -count=1

# Safety-focused fuzzing for malformed packets, Ogg pages, and libopus differential decode.
test-fuzz-safety: ensure-libopus
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzParsePacket_NoPanic' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzDecodeNeverPanics' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run='^$$' -fuzz='FuzzOggReaderNeverPanics' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzParseOpusDemoBitstream' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzDecodeAgainstLibopus' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1

# Parity tier (default for focused quality work)
test-parity:
	GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test ./testvectors -run 'TestEncoderComplianceSummary|TestEncoderCompliancePrecisionGuard|TestDecoderParityLibopusMatrix|TestDecoderParityMatrixWithFFmpeg|TestEncoderVariantProfileParityAgainstLibopusFixture' -count=1

# Native assembly/fallback validation matrix.
test-assembly-safety: ensure-libopus
	$(ASSEMBLY_SAFETY_MATRIX)

# Long-running randomized encode/decode corruption soak.
test-soak-safety:
	$(GO_WORK_ENV) $(GO) run ./tools/safety_soak -duration $(GOPUS_SAFETY_SOAK_DURATION) -report-interval $(GOPUS_SAFETY_SOAK_REPORT_INTERVAL) -max-rss-growth-mib $(GOPUS_SAFETY_SOAK_MAX_RSS_GROWTH_MIB) -max-goroutine-growth $(GOPUS_SAFETY_SOAK_MAX_GOROUTINE_GROWTH) -max-hotpath-allocs $(GOPUS_SAFETY_SOAK_MAX_ALLOCS)

# Hot-path performance guardrail checks (median benchmark thresholds + alloc bounds).
bench-guard:
	$(GO_WORK_ENV) $(GO) run ./tools/benchguard -config tools/bench_guardrails.json

# Session memory + overlap preflight for concurrent agent work.
agent-preflight:
	bash ./tools/agent_preflight.sh

# List current claims.
agent-claims:
	bash ./tools/agent_claim.sh list

# Add a claim (required vars: AGENT, PATHS; optional: NOTE, HOURS, CLAIM_ID).
agent-claim:
	@test -n "$(AGENT)" || { echo "AGENT is required"; exit 1; }
	@test -n "$(PATHS)" || { echo "PATHS is required"; exit 1; }
	bash ./tools/agent_claim.sh claim "$(AGENT)" "$(PATHS)" "$(NOTE)" "$(HOURS)" "$(CLAIM_ID)"

# Release a claim (required var: CLAIM_ID).
agent-release:
	@test -n "$(CLAIM_ID)" || { echo "CLAIM_ID is required"; exit 1; }
	bash ./tools/agent_claim.sh release "$(CLAIM_ID)"

# Default production verification gate.
verify-production: ensure-libopus
	GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_RUNNABLE_TEST) -count=1
	$(MAKE) bench-guard
	$(MAKE) test-race

# Extended production gate (includes fuzz + exhaustive fixture honesty).
verify-production-exhaustive: verify-production
	$(MAKE) test-fuzz-smoke
	$(MAKE) test-exhaustive
	$(MAKE) test-provenance

# Safety verification gate: strong existing checks first, then adversarial stress.
verify-safety: ensure-libopus
	$(MAKE) test-race
	$(MAKE) test-parity
	$(MAKE) test-exhaustive
	$(MAKE) bench-guard
	$(MAKE) test-assembly-safety
	$(MAKE) test-fuzz-safety
	$(MAKE) test-soak-safety
	$(MAKE) release-evidence

# Generate a release evidence bundle (gates + key benchmarks).
release-evidence: ensure-libopus
	./tools/gen_release_evidence.sh $(RELEASE_EVIDENCE_DIR)

# Ensure tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo exists (fetch + build if missing).
ensure-libopus:
	LIBOPUS_VERSION=$(LIBOPUS_VERSION) ./tools/ensure_libopus.sh

# Build pinned Linux CI image with codec/tooling dependencies.
docker-buildx-bootstrap:
	@docker buildx inspect $(DOCKER_BUILDER) >/dev/null 2>&1 || docker buildx create --name $(DOCKER_BUILDER) --driver docker-container >/dev/null
	@docker buildx inspect $(DOCKER_BUILDER) --bootstrap >/dev/null

# Build pinned Linux CI image with codec/tooling dependencies.
docker-build: docker-buildx-bootstrap
	@mkdir -p $(DOCKER_BUILDX_CACHE_DIR)
	@rm -rf $(DOCKER_BUILDX_CACHE_DIR)-new
	docker buildx build --builder $(DOCKER_BUILDER) --load --platform $(DOCKER_PLATFORM) --cache-from type=local,src=$(DOCKER_BUILDX_CACHE_DIR) --cache-to type=local,dest=$(DOCKER_BUILDX_CACHE_DIR)-new,mode=max --build-arg LIBOPUS_VERSION=$(LIBOPUS_VERSION) -f $(DOCKERFILE_CI) -t $(DOCKER_IMAGE) .
	@rm -rf $(DOCKER_BUILDX_CACHE_DIR)
	@mv $(DOCKER_BUILDX_CACHE_DIR)-new $(DOCKER_BUILDX_CACHE_DIR)

# Build image for exhaustive fixture-honesty checks (defaults to linux/amd64).
docker-build-exhaustive: docker-buildx-bootstrap
	@mkdir -p $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)
	@rm -rf $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)-new
	docker buildx build --builder $(DOCKER_BUILDER) --load --platform $(DOCKER_EXHAUSTIVE_PLATFORM) --cache-from type=local,src=$(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR) --cache-to type=local,dest=$(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)-new,mode=max --build-arg LIBOPUS_VERSION=$(LIBOPUS_VERSION) -f $(DOCKERFILE_CI) -t $(DOCKER_IMAGE) .
	@rm -rf $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)
	@mv $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)-new $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)

# Run full test suite in cached Linux container (modules/build/libopus volumes).
docker-test: docker-build
	docker run --rm --platform $(DOCKER_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		-e GOPUS_DISABLE_OPUSDEC=$(DOCKER_DISABLE_OPUSDEC) \
		-e GOPUS_DISABLE_OPUSENC=$(DOCKER_DISABLE_OPUSENC) \
		$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && go test ./... -count=1"

# Run exhaustive fixture honesty/provenance checks in cached Linux container.
docker-test-exhaustive: docker-build-exhaustive
	docker run --rm --platform $(DOCKER_EXHAUSTIVE_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		-e GOPUS_DISABLE_OPUSDEC=$(DOCKER_DISABLE_OPUSDEC) \
		-e GOPUS_DISABLE_OPUSENC=$(DOCKER_DISABLE_OPUSENC) \
		$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && GOPUS_TEST_TIER=exhaustive go test ./testvectors -run 'TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo|TestEncoderVariantsFixtureHonestyWithOpusDemo|TestDecoderParityMatrixFixtureHonestyWithOpusDemo|TestLongFrameReferenceFixtureHonestyWithLiveOpusdec' -count=1"

# Open an interactive shell with the same cached Docker environment.
docker-shell: docker-build
	docker run --rm -it --platform $(DOCKER_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		-e GOPUS_DISABLE_OPUSDEC=$(DOCKER_DISABLE_OPUSDEC) \
		-e GOPUS_DISABLE_OPUSENC=$(DOCKER_DISABLE_OPUSENC) \
		$(DOCKER_IMAGE) \
		bash

# Exhaustive tier includes fixture honesty checks against pinned tmp_check opus_demo/opusdec.
test-exhaustive: ensure-libopus
	GOPUS_TEST_TIER=exhaustive $(GO_WORK_ENV) $(GO) test ./testvectors -run 'TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo|TestEncoderVariantsFixtureHonestyWithOpusDemo|TestDecoderParityMatrixFixtureHonestyWithOpusDemo|TestDecoderLossFixtureHonestyWithOpusDemo|TestLongFrameReferenceFixtureHonestyWithLiveOpusdec' -count=1

# Exhaustive provenance audit for encoder variant parity.
test-provenance: ensure-libopus
	GOPUS_TEST_TIER=exhaustive $(GO_WORK_ENV) $(GO) test ./testvectors -run 'TestEncoderVariantProfileProvenanceAudit' -count=1

# Regenerate fixture files from tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo.
fixtures-gen: ensure-libopus fixtures-gen-decoder fixtures-gen-decoder-loss fixtures-gen-encoder fixtures-gen-variants

fixtures-gen-decoder:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_decoder_matrix_fixture.go

fixtures-gen-decoder-loss:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_decoder_loss_fixture.go

fixtures-gen-encoder:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_encoder_packet_fixture.go

fixtures-gen-variants:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_encoder_variants_fixture.go

# Regenerate amd64-specific fixture files in a cached linux/amd64 container.
fixtures-gen-amd64: docker-build-exhaustive
	docker run --rm --platform $(DOCKER_EXHAUSTIVE_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && \
			GOPUS_DECODER_MATRIX_FIXTURE_OUT=testvectors/testdata/libopus_decoder_matrix_fixture_amd64.json go run tools/gen_libopus_decoder_matrix_fixture.go && \
			GOPUS_DECODER_LOSS_FIXTURE_OUT=testvectors/testdata/libopus_decoder_loss_fixture_amd64.json go run tools/gen_libopus_decoder_loss_fixture.go && \
			GOPUS_ENCODER_PACKETS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_packets_fixture_amd64.json go run tools/gen_libopus_encoder_packet_fixture.go && \
			GOPUS_ENCODER_VARIANTS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_variants_fixture_amd64.json go run tools/gen_libopus_encoder_variants_fixture.go && \
			GOPUS_OPUSDEC_CROSSVAL_FIXTURE_OUT=celt/testdata/opusdec_crossval_fixture_amd64.json go run tools/gen_opusdec_crossval_fixture.go"

# Build with profile-guided optimization (default.pgo auto-discovered by Go toolchain)
build:
	$(GO_WORK_ENV) $(GO) build -pgo=auto ./...

# Build without profile-guided optimization
build-nopgo:
	$(GO_WORK_ENV) $(GO) build -pgo=off ./...

# Regenerate default.pgo from decode hot-path benchmarks
pgo-generate:
	$(GO_WORK_ENV) $(GO) test -run='^$$' -bench='$(PGO_BENCH)' -benchtime=$(PGO_BENCHTIME) -count=$(PGO_COUNT) -cpuprofile $(PGO_FILE) $(PGO_PKG)

# Refresh default.pgo then build with PGO enabled
pgo-build: pgo-generate build

# Remove local build/test artifacts generated during development.
clean:
	find . -maxdepth 1 -type f \( -name '*.test' -o -name '*.prof' -o -name '*.out' -o -name '*.o' -o -name '*.trace' -o -name 'coverage.out' -o -name 'coverage.html' \) -delete

# Remove downloaded official Opus test vectors cache.
clean-vectors:
	rm -rf testvectors/testdata/opus_testvectors/

# Run kernel-level benchmarks for CELT and SILK DSP functions.
bench-kernels:
	$(GO_WORK_ENV) $(GO) test -bench=. -benchmem -count=5 ./celt/ ./silk/
