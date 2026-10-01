//go:build gopus_osce && gopus_qext && !gopus_dred

package gopus

import "testing"

func TestQEXTExtraControlsBuildOptionalExtensionContract(t *testing.T) {
	if !SupportsOptionalExtension(OptionalExtensionDNNBlob) {
		t.Fatal("gopus_osce,gopus_qext build does not report DNN blob support")
	}
	if SupportsOptionalExtension(OptionalExtensionDRED) {
		t.Fatal("gopus_osce,gopus_qext build unexpectedly reports DRED support")
	}
	if !SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Fatal("gopus_osce,gopus_qext build does not report QEXT support")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("gopus_osce,gopus_qext build unexpectedly reports OSCE BWE support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(extraDREDControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	qext, ok := any(enc).(qextEncoderControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose encoder QEXT control")
	}
	assertSupportedQEXTControl(t, qext)

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	osce, ok := any(dec).(extraOSCEBWEControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, osce)
	lace, ok := any(dec).(extraOSCELACEControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose decoder OSCE LACE control")
	}
	assertWorkingOSCELACEControl(t, lace)

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, msEnc)
	msDred, ok := any(msEnc).(extraDREDControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	msQEXT, ok := any(msEnc).(qextEncoderControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose multistream encoder QEXT control")
	}
	assertSupportedQEXTControl(t, msQEXT)

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	msOSCE, ok := any(msDec).(extraOSCEBWEControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose multistream decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, msOSCE)
	msLACE, ok := any(msDec).(extraOSCELACEControl)
	if !ok {
		t.Fatal("QEXT+extra-controls build does not expose multistream decoder OSCE LACE control")
	}
	assertWorkingOSCELACEControl(t, msLACE)
}
