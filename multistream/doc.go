// Package multistream implements the low-level multistream engine used by gopus.
//
// Most applications should prefer the top-level gopus multistream wrappers.
//
// This package exposes advanced implementation details that may change before
// the first release. Unsupported DRED and OSCE helper surfaces are
// intentionally tag-gated and absent from the default build.
package multistream
