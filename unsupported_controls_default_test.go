//go:build !gopus_unsupported_controls
// +build !gopus_unsupported_controls

package gopus

import "testing"

func TestDefaultBuildQuarantinesUnsupportedControls(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if _, ok := any(enc).(unsupportedDREDControl); ok {
		t.Fatal("Encoder unexpectedly exposes DRED control in the default build")
	}

	dec := newMonoTestDecoder(t)
	if _, ok := any(dec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("Decoder unexpectedly exposes OSCE BWE control in the default build")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if _, ok := any(msEnc).(unsupportedDREDControl); ok {
		t.Fatal("MultistreamEncoder unexpectedly exposes DRED control in the default build")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if _, ok := any(msDec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("MultistreamDecoder unexpectedly exposes OSCE BWE control in the default build")
	}
}
