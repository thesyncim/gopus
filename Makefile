.PHONY: lint lint-fix test test-fast test-parity test-exhaustive test-provenance ensure-libopus fixtures-gen fixtures-gen-decoder fixtures-gen-encoder fixtures-gen-variants fixtures-gen-amd64 docker-buildx-bootstrap docker-build docker-build-exhaustive docker-test docker-test-exhaustive docker-shell build build-nopgo pgo-generate pgo-build clean clean-vectors

GO ?= go
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
	GOPUS_TEST_TIER=exhaustive $(GO) test ./testvectors -run 'TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo|TestEncoderVariantsFixtureHonestyWithOpusDemo|TestDecoderParityMatrixFixtureHonestyWithOpusDemo|TestLongFrameReferenceFixtureHonestyWithLiveOpusdec' -count=1

# Exhaustive provenance audit for encoder variant parity.
test-provenance: ensure-libopus
	GOPUS_TEST_TIER=exhaustive $(GO) test ./testvectors -run 'TestEncoderVariantProfileProvenanceAudit' -count=1

# Regenerate fixture files from tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo.
fixtures-gen: ensure-libopus fixtures-gen-decoder fixtures-gen-encoder fixtures-gen-variants

fixtures-gen-decoder:
	$(GO) run tools/gen_libopus_decoder_matrix_fixture.go

fixtures-gen-encoder:
	$(GO) run tools/gen_libopus_encoder_packet_fixture.go

fixtures-gen-variants:
	$(GO) run tools/gen_libopus_encoder_variants_fixture.go

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
			GOPUS_ENCODER_PACKETS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_packets_fixture_amd64.json go run tools/gen_libopus_encoder_packet_fixture.go && \
			GOPUS_ENCODER_VARIANTS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_variants_fixture_amd64.json go run tools/gen_libopus_encoder_variants_fixture.go"

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
