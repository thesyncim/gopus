//go:build !gopus_tmp_env && arm64

package celt

const mdctUseNativeMulEnabled = false

const mdctUseF64MixEnabled = false

const mdctUseFMALikeMixEnabled = false

const mdctUseNativeMulShort240Enabled = true

const mdctUseFMALikeMixShort240Enabled = true
