//go:build !gopus_dred && !gopus_extra_controls
// +build !gopus_dred,!gopus_extra_controls

package gopus

import "testing"

func assertDecoderDREDRuntimeLoadedForTest(t testing.TB, _ *Decoder, _ string) {
	t.Helper()
}
