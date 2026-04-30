// Package multistream implements the low-level multistream engine used by gopus.
//
// Most applications should prefer the top-level gopus multistream wrappers.
//
// This package exposes advanced implementation details that may change before
// the first release. DRED is available only in builds using -tags gopus_dred.
// OSCE helper surfaces remain quarantine-gated and absent from the default
// build.
package multistream
