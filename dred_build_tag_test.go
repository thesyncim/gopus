//go:build gopus_dred && !gopus_unsupported_controls
// +build gopus_dred,!gopus_unsupported_controls

package gopus

import "testing"

func TestDREDBuildTagExposesSupportedTopLevelControls(t *testing.T) {
	if !SupportsOptionalExtension(OptionalExtensionDRED) {
		t.Fatal("gopus_dred build does not report DRED support")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("gopus_dred build unexpectedly reports OSCE BWE support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("gopus_dred build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("top-level encoder SetDNNBlob did not propagate DRED-capable blob")
	}

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	if _, ok := any(dec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("gopus_dred build unexpectedly exposes decoder OSCE BWE control")
	}
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
	msDred, ok := any(msEnc).(unsupportedDREDControl)
	if !ok {
		t.Fatal("gopus_dred build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	if !msEnc.enc.DREDModelLoaded() {
		t.Fatal("top-level multistream encoder SetDNNBlob did not propagate DRED-capable blob")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	if _, ok := any(msDec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("gopus_dred build unexpectedly exposes multistream decoder OSCE BWE control")
	}
}
