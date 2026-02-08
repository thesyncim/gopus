.PHONY: lint lint-fix test build build-nopgo pgo-generate pgo-build

GO ?= go
PGO_FILE ?= default.pgo
PGO_BENCH ?= ^Benchmark(DecoderDecode|EncoderEncode)_(CELT|Hybrid|SILK|Stereo|MultiFrame|VoIP|LowDelay)$
PGO_PKG ?= .
PGO_BENCHTIME ?= 20s
PGO_COUNT ?= 1

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
