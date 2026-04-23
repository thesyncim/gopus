//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import "testing"

func TestUnsupportedControlsBuildExposesQuarantinedTopLevelControls(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	assertSupportedQEXTControl(t, enc)
	dred, ok := any(enc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("unsupported-controls build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("top-level encoder SetDNNBlob did not propagate DRED-capable blob to the core encoder")
	}

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	osce, ok := any(dec).(unsupportedOSCEBWEControl)
	if !ok {
		t.Fatal("unsupported-controls build does not expose decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, osce)
	standaloneDRED := NewDREDDecoder()
	if standaloneDRED == nil {
		t.Fatal("NewDREDDecoder returned nil")
	}
	if err := standaloneDRED.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("standalone DRED SetDNNBlob error: %v", err)
	}
	if !standaloneDRED.ModelLoaded() {
		t.Fatal("standalone DRED decoder did not retain model blob")
	}
	if dredState := NewDRED(); dredState == nil {
		t.Fatal("NewDRED returned nil")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, msEnc)
	assertSupportedQEXTControl(t, msEnc)
	msDred, ok := any(msEnc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("unsupported-controls build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	if !msEnc.enc.DREDModelLoaded() {
		t.Fatal("top-level multistream encoder SetDNNBlob did not propagate DRED-capable blob to child encoders")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	msOSCE, ok := any(msDec).(unsupportedOSCEBWEControl)
	if !ok {
		t.Fatal("unsupported-controls build does not expose multistream decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, msOSCE)
}
