FOCUS_GATE_TARGETS := test-doc-contract test-dnn-blob-parity test-core-oracles-parity test-dred-tag test-qext-parity test-extra-controls-tag test-extra-controls-parity test-quality test-exactness test-exhaustive test-provenance

.PHONY: lint lint-fix test test-fast test-race test-fuzz-smoke test-fuzz-safety test-consumer-smoke test-examples-smoke $(FOCUS_GATE_TARGETS) quality-report test-assembly-safety test-soak-safety bench-guard bench-libopus-guard bench-decoder-libopus-guard bench-encoder-libopus-guard bench-testvectors bench-testvectors-compare bench-testvectors-report verify-production verify-production-exhaustive verify-safety release-evidence release-preflight ensure-libopus ensure-libopus-qext ensure-testvectors fixtures-gen fixtures-gen-decoder fixtures-gen-decoder-loss fixtures-gen-encoder fixtures-gen-variants fixtures-gen-linux-amd64 docker-buildx-bootstrap docker-build docker-build-exhaustive docker-test docker-test-exhaustive docker-shell build build-nopgo pgo-generate pgo-build clean clean-vectors bench-kernels

GO ?= go
GO_WORK_ENV ?= GOWORK=off
GOLANGCI_LINT ?= golangci-lint
GOLANGCI_LINT_VERSION ?= v1.64.8
GO_RUNNABLE_TEST ?= bash ./tools/run_go_test_runnable.sh
ASSEMBLY_SAFETY_MATRIX ?= bash ./tools/run_assembly_safety_matrix.sh
FOCUS_GATE ?= bash ./tools/run_focus_gate.sh
FOCUS_GATE_CMD = GO=$(GO) GO_WORK_ENV="$(GO_WORK_ENV)" $(FOCUS_GATE)
PGO_FILE ?= default.pgo
PGO_FLAG ?= -pgo=$(PGO_FILE)
PGO_GENERATE_FLAG ?= -pgo=off
PGO_REPORT_PROFILE ?= $(PGO_FILE)
PGO_BENCH ?= ^Benchmark(DecoderDecode_CELT|DecoderDecodeInt16|DecoderDecode_Stereo|EncoderEncode_CallerBuffer|EncoderEncodeInt16|EncoderEncode_Restricted(CELT|CELT5ms|SILK)CBRStreamAfterReset)$$
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
QUALITY_REPORT_DIR ?= reports/quality
GOPUS_SAFETY_FUZZTIME ?= 12s
GOPUS_SAFETY_PARSER_FUZZTIME ?= $(GOPUS_SAFETY_FUZZTIME)
GOPUS_FUZZ_SMOKE_FUZZTIME ?= 50000x
GOPUS_SAFETY_SOAK_DURATION ?= 30s
GOPUS_SAFETY_SOAK_REPORT_INTERVAL ?= 10s
GOPUS_SAFETY_SOAK_MAX_RSS_GROWTH_MIB ?= 256
GOPUS_SAFETY_SOAK_MAX_GOROUTINE_GROWTH ?= 16
GOPUS_SAFETY_SOAK_MAX_ALLOCS ?= 0.0
BENCH_TESTVECTORS_COMPARE_TIME ?= 200ms
BENCH_TESTVECTORS_COMPARE_TIMES ?=
BENCH_TESTVECTORS_COMPARE_COUNT ?= 3
BENCH_TESTVECTORS_COMPARE_CASES ?= all
BENCH_TESTVECTORS_COMPARE_PATHS ?= all
BENCH_TESTVECTORS_COMPARE_TIME_FLAG = $(if $(BENCH_TESTVECTORS_COMPARE_TIMES),-benchtimes=$(BENCH_TESTVECTORS_COMPARE_TIMES),-benchtime=$(BENCH_TESTVECTORS_COMPARE_TIME))
BENCH_LIBOPUS_GUARD_TIME ?= 200ms
BENCH_LIBOPUS_GUARD_COUNT ?= 3
BENCH_LIBOPUS_GUARD_RATIO ?= 3.25
BENCH_LIBOPUS_GUARD_ALLOCS ?= 0
BENCH_ENCODER_LIBOPUS_GUARD_RATIO ?= 3.25
BENCH_ENCODER_LIBOPUS_GUARD_ALLOCS ?= 150
BENCH_ENCODER_LIBOPUS_GUARD_CASES ?= all
TEST_VECTOR_URL ?= https://opus-codec.org/static/testvectors/opus_testvectors-rfc8251.tar.gz
TEST_VECTOR_FALLBACK_URL ?= https://www.ietf.org/proceedings/98/slides/materials-98-codec-opus-newvectors-00.tar.gz
RUNNABLE_FAST = GOPUS_TEST_TIER=fast $(GO_RUNNABLE_TEST)
RUNNABLE_PARITY = GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_RUNNABLE_TEST)

# Run golangci-lint
lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: GOWORK=off go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; exit 1; }
	$(GO_WORK_ENV) $(GOLANGCI_LINT) run ./...

# Run golangci-lint with auto-fix
lint-fix:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: GOWORK=off go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; exit 1; }
	$(GO_WORK_ENV) $(GOLANGCI_LINT) run --fix ./...

# Run the default package suite with pinned-reference oracles active.
test: ensure-libopus
	$(RUNNABLE_PARITY)

# Fast inner-loop tests (skips parity/exhaustive tier checks)
test-fast:
	$(RUNNABLE_FAST) -short

# Race detector sweep across all packages at fast test tier (keeps runtime bounded).
test-race:
	$(RUNNABLE_FAST) -race -count=1 -timeout=20m

# Fuzz smoke run for packet/fixture parsers.
test-fuzz-smoke:
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzParsePacket_NoPanic' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketExtensionIterator_NoPanic' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketMutationHelpers_NoPanic' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzParseOpusDemoBitstream' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1

# Safety-focused fuzzing for malformed packets, Ogg pages, and libopus differential decode.
test-fuzz-safety: ensure-libopus
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzParsePacket_NoPanic' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketExtensionIterator_NoPanic' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketMutationHelpers_NoPanic' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzDecodeNeverPanics' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run='^$$' -fuzz='FuzzFindDREDPayload_NoPanic' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run='^$$' -fuzz='FuzzOggReaderNeverPanics' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzParseOpusDemoBitstream' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzDecodeAgainstLibopus' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1

# Downstream consumer smoke path from a nested external module boundary.
test-consumer-smoke:
	cd examples/external-consumer-smoke && $(GO_WORK_ENV) $(GO) test ./... -count=1

# Compile and test maintained examples, including build-tag-only surfaces.
# The Gio loopback example uses a headless smoke tag so Linux CI does not need
# desktop window-system pkg-config dependencies just to compile its tests.
test-examples-smoke:
	$(GO_WORK_ENV) $(GO) test ./examples/... -count=1
	$(GO_WORK_ENV) $(GO) test -tags gopus_dred ./examples/... -count=1
	$(GO_WORK_ENV) $(GO) test -tags gopus_qext ./examples/... -count=1
	tmp="$$(mktemp -d)" && trap 'rm -rf "$$tmp"' EXIT && cd examples/webrtc-control && $(GO_WORK_ENV) $(GO) build -o "$$tmp/webrtc-control" .
	cd examples/webrtc-dred-loopback && $(GO_WORK_ENV) $(GO) test -tags gopus_webrtc_headless ./... -count=1
	cd examples/webrtc-dred-loopback && $(GO_WORK_ENV) $(GO) test -tags 'gopus_dred gopus_webrtc_headless' ./... -count=1

# Docs and release-surface contracts stay focused so they do not require
# heavyweight libopus fixture trees.
test-doc-contract:

# Default-supported DNN blob control parity against libopus USE_WEIGHTS_FILE
# model blobs. The target fails if the required libopus-backed test is skipped.
test-dnn-blob-parity: ensure-libopus

# Pinned low-level CELT/range/SILK internal oracles.
test-core-oracles-parity: ensure-libopus

# Supported DRED feature-tag parity gate. The extra-controls tag remains a
# broader parity umbrella; this target verifies the supported DRED surface by itself.
test-dred-tag: ensure-libopus

# Supported QEXT feature-tag parity. The default build keeps QEXT controls
# absent and leaves packet-extension payload plumbing behind compile-time gates.
test-qext-parity: ensure-libopus-qext

# Extra-controls build smoke for controls that should never leak into the default surface.
test-extra-controls-tag: ensure-libopus

# Required tag-gated DRED/OSCE oracle sweep. Keep it separate from the
# extra-controls API smoke so support claims stay seam-scoped.
test-extra-controls-parity: ensure-libopus

# Primary libopus-facing focused gate.
test-quality: ensure-libopus ensure-testvectors

# Optional libopus-internal exactness checks. These are intentionally not part
# of the default production gate so math optimizations can move while quality
# and interoperability stay enforced.
test-exactness:

$(FOCUS_GATE_TARGETS):
	$(FOCUS_GATE_CMD) $(patsubst test-%,%,$@)

# Compact markdown summary for the quality + compatibility gates.
quality-report: ensure-libopus
	$(GO_WORK_ENV) $(GO) run ./tools/qualityreport -out-dir $(QUALITY_REPORT_DIR)

# Native assembly/fallback validation matrix.
test-assembly-safety: ensure-libopus
	$(ASSEMBLY_SAFETY_MATRIX)

# Long-running randomized encode/decode corruption soak.
test-soak-safety:
	$(GO_WORK_ENV) $(GO) run ./tools/safety_soak -duration $(GOPUS_SAFETY_SOAK_DURATION) -report-interval $(GOPUS_SAFETY_SOAK_REPORT_INTERVAL) -max-rss-growth-mib $(GOPUS_SAFETY_SOAK_MAX_RSS_GROWTH_MIB) -max-goroutine-growth $(GOPUS_SAFETY_SOAK_MAX_GOROUTINE_GROWTH) -max-hotpath-allocs $(GOPUS_SAFETY_SOAK_MAX_ALLOCS)

# Hot-path performance guardrail checks (median benchmark thresholds + alloc bounds).
bench-guard:
	$(GO_WORK_ENV) $(GO) run ./tools/benchguard -config tools/bench_guardrails.json

# Libopus-relative codec performance guardrails against the pinned reference.
bench-libopus-guard: bench-decoder-libopus-guard bench-encoder-libopus-guard

# Libopus-relative decode performance guardrail on the official RFC 8251 bitstreams.
bench-decoder-libopus-guard: ensure-libopus ensure-testvectors
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/testvectorbenchcmp -cases=aggregate -paths=all -benchtime=$(BENCH_LIBOPUS_GUARD_TIME) -count=$(BENCH_LIBOPUS_GUARD_COUNT) -format=tsv -max-gopus-libopus-ratio=$(BENCH_LIBOPUS_GUARD_RATIO) -max-gopus-allocs-per-op=$(BENCH_LIBOPUS_GUARD_ALLOCS)

# Libopus-relative encoder performance guardrail across CELT, SILK, and Hybrid workloads.
bench-encoder-libopus-guard: ensure-libopus
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/encoderbenchcmp -cases=$(BENCH_ENCODER_LIBOPUS_GUARD_CASES) -benchtime=$(BENCH_LIBOPUS_GUARD_TIME) -count=$(BENCH_LIBOPUS_GUARD_COUNT) -format=tsv -max-gopus-libopus-ratio=$(BENCH_ENCODER_LIBOPUS_GUARD_RATIO) -max-gopus-allocs-per-op=$(BENCH_ENCODER_LIBOPUS_GUARD_ALLOCS)

# Decode the official RFC 8251 bitstreams with benchmark metrics per vector.
bench-testvectors: ensure-testvectors
	$(GO_WORK_ENV) $(GO) test $(PGO_FLAG) ./testvectors -run='^$$' -bench='^BenchmarkDecodeOfficialTestVectors$$' -benchmem -count=1

# Compare the same official bitstreams against pinned libopus and emit Markdown.
bench-testvectors-compare: ensure-libopus ensure-testvectors
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/testvectorbenchcmp -cases=$(BENCH_TESTVECTORS_COMPARE_CASES) -paths=$(BENCH_TESTVECTORS_COMPARE_PATHS) $(BENCH_TESTVECTORS_COMPARE_TIME_FLAG) -count=$(BENCH_TESTVECTORS_COMPARE_COUNT) -gopus-pgo=$(PGO_REPORT_PROFILE) -format=markdown

# Generate a local Markdown benchmark report.
bench-testvectors-report: ensure-libopus ensure-testvectors
	@mkdir -p reports/quality
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/testvectorbenchcmp -cases=$(BENCH_TESTVECTORS_COMPARE_CASES) -paths=$(BENCH_TESTVECTORS_COMPARE_PATHS) $(BENCH_TESTVECTORS_COMPARE_TIME_FLAG) -count=$(BENCH_TESTVECTORS_COMPARE_COUNT) -gopus-pgo=$(PGO_REPORT_PROFILE) -format=markdown -out reports/quality/testvector-benchmarks.md

# Default production verification gate.
verify-production: ensure-libopus
	$(RUNNABLE_PARITY) -count=1 -timeout=25m
	$(MAKE) test-consumer-smoke
	$(MAKE) test-examples-smoke
	$(MAKE) test-dnn-blob-parity
	$(MAKE) test-core-oracles-parity
	$(MAKE) test-dred-tag
	$(MAKE) test-qext-parity
	$(MAKE) test-extra-controls-tag
	$(MAKE) test-extra-controls-parity
	$(MAKE) bench-guard
	$(MAKE) bench-libopus-guard
	$(MAKE) test-race

# Extended production gate (includes fuzz + exhaustive fixture honesty).
verify-production-exhaustive: verify-production
	$(MAKE) test-fuzz-smoke
	$(MAKE) test-exhaustive
	$(MAKE) test-provenance

# Safety verification gate: strong existing checks first, then adversarial stress.
verify-safety: ensure-libopus
	$(MAKE) test-race
	$(MAKE) test-quality
	$(MAKE) test-exhaustive
	$(MAKE) bench-guard
	$(MAKE) bench-libopus-guard
	$(MAKE) test-assembly-safety
	$(MAKE) test-fuzz-safety
	$(MAKE) test-soak-safety
	$(MAKE) release-evidence

# Generate a release evidence bundle (gates + key benchmarks).
release-evidence: ensure-libopus
	./tools/gen_release_evidence.sh $(RELEASE_EVIDENCE_DIR)

# Local release preflight before pushing a public tag.
release-preflight:
	@test -n "$(TAG)" || { echo "TAG is required, for example: make release-preflight TAG=v0.1.0"; exit 1; }
	@case "$(TAG)" in \
		v[0-9]*.[0-9]*.[0-9]*) ;; \
		*) echo "TAG must look like v0.1.0"; exit 1 ;; \
	esac
	@git diff --quiet --ignore-submodules -- && git diff --cached --quiet --ignore-submodules -- || { echo "working tree must be clean before release-preflight"; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || { echo "tag $(TAG) already exists locally"; exit 1; }
	$(MAKE) lint
	$(MAKE) verify-production-exhaustive
	$(MAKE) release-evidence
	@test -n "$$(find "$(RELEASE_EVIDENCE_DIR)" -maxdepth 1 -type f -name 'release-evidence-*.md' -print -quit)" || { echo "missing generated release evidence summary in $(RELEASE_EVIDENCE_DIR)"; exit 1; }
	@grep -q 'Overall result: PASS' "$$(find "$(RELEASE_EVIDENCE_DIR)" -maxdepth 1 -type f -name 'release-evidence-*.md' | sort | tail -n 1)" || { echo "latest release evidence summary did not pass"; exit 1; }

# Ensure tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo exists (fetch + build if missing).
ensure-libopus:
	LIBOPUS_VERSION=$(LIBOPUS_VERSION) ./tools/ensure_libopus.sh

# Ensure tmp_check/opus-$(LIBOPUS_VERSION)-qext/opus_demo exists with ENABLE_QEXT.
ensure-libopus-qext:
	LIBOPUS_VERSION=$(LIBOPUS_VERSION) LIBOPUS_ENABLE_QEXT=1 ./tools/ensure_libopus.sh

# Ensure the downloaded official RFC 8251 test-vector cache exists.
ensure-testvectors:
	@bash -c 'set -euo pipefail; \
		dir="testvectors/testdata/opus_testvectors"; \
		complete() { \
			for n in 01 02 03 04 05 06 07 08 09 10 11 12; do \
				for ext in bit dec; do \
					test -s "$$dir/testvector$$n.$$ext" || return 1; \
				done; \
			done; \
		}; \
		if ! complete; then \
			tmp=$$(mktemp -d); \
			trap "rm -rf \"$$tmp\"" EXIT; \
			archive="$$tmp/opus_testvectors-rfc8251.tar.gz"; \
			for url in "$(TEST_VECTOR_URL)" "$(TEST_VECTOR_FALLBACK_URL)"; do \
				echo "fetching official test vectors from $$url"; \
				if curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 --max-time 180 "$$url" -o "$$archive"; then \
					fetched=1; \
					break; \
				fi; \
			done; \
			test "$${fetched:-}" = 1 || { echo "failed to fetch official test vectors"; exit 1; }; \
			rm -rf "$$dir"; \
			mkdir -p "$$dir"; \
			tar -xzf "$$archive" -C "$$tmp"; \
			find "$$tmp" -type f \( -name "testvector*.bit" -o -name "testvector*.dec" \) -exec cp {} "$$dir"/ \;; \
			complete || { echo "downloaded official test vectors are incomplete"; exit 1; }; \
		fi'
	cd testvectors && $(GO_WORK_ENV) $(GO) test . -run='^TestParseTestVectorBitstreams$$' -count=1

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
		bash -c "make test-exhaustive"

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

# Exhaustive provenance audit for encoder variant parity.
test-provenance: ensure-libopus

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

# Regenerate linux/amd64-specific fixture files in a cached linux/amd64 container.
fixtures-gen-linux-amd64: docker-build-exhaustive
	docker run --rm --platform $(DOCKER_EXHAUSTIVE_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && \
			GOPUS_DECODER_MATRIX_FIXTURE_OUT=testvectors/testdata/libopus_decoder_matrix_fixture_linux_amd64.json go run tools/gen_libopus_decoder_matrix_fixture.go && \
			GOPUS_DECODER_LOSS_FIXTURE_OUT=testvectors/testdata/libopus_decoder_loss_fixture_linux_amd64.json go run tools/gen_libopus_decoder_loss_fixture.go && \
			GOPUS_ENCODER_PACKETS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_packets_fixture_linux_amd64.json go run tools/gen_libopus_encoder_packet_fixture.go && \
			GOPUS_ENCODER_VARIANTS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_variants_fixture_linux_amd64.json go run tools/gen_libopus_encoder_variants_fixture.go && \
			GOPUS_OPUSDEC_CROSSVAL_FIXTURE_OUT=celt/testdata/opusdec_crossval_fixture_amd64.json go run tools/gen_opusdec_crossval_fixture.go"

# Build with profile-guided optimization.
build:
	$(GO_WORK_ENV) $(GO) build $(PGO_FLAG) ./...

# Build without profile-guided optimization
build-nopgo:
	$(GO_WORK_ENV) $(GO) build -pgo=off ./...

# Regenerate default.pgo from representative public encode/decode hot-path benchmarks
pgo-generate:
	$(GO_WORK_ENV) $(GO) test $(PGO_GENERATE_FLAG) -run='^$$' -bench='$(PGO_BENCH)' -benchtime=$(PGO_BENCHTIME) -count=$(PGO_COUNT) -cpuprofile $(PGO_FILE) $(PGO_PKG)

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
