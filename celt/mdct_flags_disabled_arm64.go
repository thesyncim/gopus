//go:build arm64

package celt

const mdctUseNativeMulEnabled = false

const mdctUseF64MixEnabled = false

// Match the pinned libopus arm64 float-path accumulation pattern on long-frame
// CELT MDCT mixes. This closes real packet-decision drift on parity fixtures.
const mdctUseFMALikeMixEnabled = true

const mdctUseNativeMulShort240Enabled = true

const mdctUseFMALikeMixShort240Enabled = true
