//go:build !gopus_tmp_env

package celt

// Decoder tracing is compiled out in production builds.
const decodeTracingEnabled = false
