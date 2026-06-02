//go:build gopus_libopus_bench && purego

package gopus_test

// scoreboardGopusIsPureGo reports whether the gopus codec under test was built
// with -tags purego (scalar Go, no asm kernels). The perf scoreboard uses it to
// confirm the gopus side matches the selected fair tier: purego-vs-noasm wants
// this true, asm-vs-asm wants it false.
const scoreboardGopusIsPureGo = true
