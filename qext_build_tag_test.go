//go:build gopus_qext && !gopus_dred && !gopus_unsupported_controls
// +build gopus_qext,!gopus_dred,!gopus_unsupported_controls

package gopus

import "testing"

func TestQEXTBuildTagExposesSupportedTopLevelControls(t *testing.T) {
	if !SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Fatal("gopus_qext build does not report QEXT support")
	}
	if SupportsOptionalExtension(OptionalExtensionDRED) {
		t.Fatal("gopus_qext build unexpectedly reports DRED support")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("gopus_qext build unexpectedly reports OSCE BWE support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	assertSupportedQEXTControl(t, enc)
	if _, ok := any(enc).(unsupportedDREDControl); ok {
		t.Fatal("gopus_qext build unexpectedly exposes encoder DRED control")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, msEnc)
	assertSupportedQEXTControl(t, msEnc)
	if _, ok := any(msEnc).(unsupportedDREDControl); ok {
		t.Fatal("gopus_qext build unexpectedly exposes multistream encoder DRED control")
	}
}
