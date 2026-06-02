//go:build gopus_libopus_bench && !purego

package gopus_test

// scoreboardGopusIsPureGo is false for the default gopus build (NEON/amd64 asm
// kernels active). See benchmark_libopus_scoreboard_purego_test.go for the
// purego counterpart and how the perf scoreboard uses this to flag tier/build
// mismatches.
const scoreboardGopusIsPureGo = false
