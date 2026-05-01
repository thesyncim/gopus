//go:build gopus_dred && gopus_qext && !gopus_unsupported_controls
// +build gopus_dred,gopus_qext,!gopus_unsupported_controls

package gopus

import "testing"

func TestCombinedDREDQEXTBuildOptionalExtensionContract(t *testing.T) {
	if !SupportsOptionalExtension(OptionalExtensionDNNBlob) {
		t.Fatal("combined gopus_dred,gopus_qext build does not report DNN blob support")
	}
	if !SupportsOptionalExtension(OptionalExtensionDRED) {
		t.Fatal("combined gopus_dred,gopus_qext build does not report DRED support")
	}
	if !SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Fatal("combined gopus_dred,gopus_qext build does not report QEXT support")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly reports OSCE BWE support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("combined build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	qext, ok := any(enc).(qextEncoderControl)
	if !ok {
		t.Fatal("combined build does not expose encoder QEXT control")
	}
	assertSupportedQEXTControl(t, qext)

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	if _, ok := any(dec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly exposes decoder OSCE BWE control")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, msEnc)
	msDred, ok := any(msEnc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("combined build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	msQEXT, ok := any(msEnc).(qextEncoderControl)
	if !ok {
		t.Fatal("combined build does not expose multistream encoder QEXT control")
	}
	assertSupportedQEXTControl(t, msQEXT)

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	if _, ok := any(msDec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("combined gopus_dred,gopus_qext build unexpectedly exposes multistream decoder OSCE BWE control")
	}
}
