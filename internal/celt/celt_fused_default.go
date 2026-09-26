//go:build !arm64 || nosimd || !goexperiment.simd

package celt

// celtFusedFloat is false on the builds paired with sequential C reductions:
// every amd64 build, and the arm64 ordinary and nosimd builds, which pair with
// the scalar libopus build.
const celtFusedFloat = false
