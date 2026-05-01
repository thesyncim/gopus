//go:build gopus_unsupported_controls && gopus_qext
// +build gopus_unsupported_controls,gopus_qext

package gopus

import "testing"

func TestQEXTUnsupportedControlsBuildOptionalExtensionContract(t *testing.T) {
	if !SupportsOptionalExtension(OptionalExtensionDNNBlob) {
		t.Fatal("gopus_unsupported_controls,gopus_qext build does not report DNN blob support")
	}
	if SupportsOptionalExtension(OptionalExtensionDRED) {
		t.Fatal("gopus_unsupported_controls,gopus_qext build unexpectedly reports DRED support")
	}
	if !SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Fatal("gopus_unsupported_controls,gopus_qext build does not report QEXT support")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("gopus_unsupported_controls,gopus_qext build unexpectedly reports OSCE BWE support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("combined quarantine build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	qext, ok := any(enc).(qextEncoderControl)
	if !ok {
		t.Fatal("combined quarantine build does not expose encoder QEXT control")
	}
	assertSupportedQEXTControl(t, qext)

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	osce, ok := any(dec).(unsupportedOSCEBWEControl)
	if !ok {
		t.Fatal("combined quarantine build does not expose decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, osce)

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, msEnc)
	msDred, ok := any(msEnc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("combined quarantine build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	msQEXT, ok := any(msEnc).(qextEncoderControl)
	if !ok {
		t.Fatal("combined quarantine build does not expose multistream encoder QEXT control")
	}
	assertSupportedQEXTControl(t, msQEXT)

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	msOSCE, ok := any(msDec).(unsupportedOSCEBWEControl)
	if !ok {
		t.Fatal("combined quarantine build does not expose multistream decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, msOSCE)
}
