//go:build gopus_osce

package gopus

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/extsupport"
	"math/rand"
	"testing"
)

func TestEncoderCTLSequence_DREDDurationFuzz(t *testing.T) {
	const dredMaxFrames = 104 // libopus DRED_MAX_FRAMES = 4*DRED_MAX_LATENTS
	if internaldred.MaxFrames != dredMaxFrames {
		t.Fatalf("internal/dred.MaxFrames=%d, want %d (libopus DRED_MAX_FRAMES=4*DRED_MAX_LATENTS)",
			internaldred.MaxFrames, dredMaxFrames)
	}

	pool := []int{-1000, -2, -1, 0, 1, 5, 26, 52, 104, 105, 200, 1000}
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// Model: libopus stores the last accepted DRED duration; an out-of-range
	// SET is rejected (OPUS_BAD_ARG) and leaves the stored value unchanged.
	want := 0
	if got, _ := enc.DREDDuration(); got != want {
		t.Fatalf("default DREDDuration()=%d, want %d", got, want)
	}

	r := rand.New(rand.NewSource(1))
	for step := 0; step < 500; step++ {
		v := pool[r.Intn(len(pool))]
		err := enc.SetDREDDuration(v)
		valid := v >= 0 && v <= dredMaxFrames
		if valid && err != nil {
			t.Fatalf("step %d: SetDREDDuration(%d) error=%v, want nil (libopus accepts 0..%d)", step, v, err, dredMaxFrames)
		}
		if !valid && err == nil {
			t.Fatalf("step %d: SetDREDDuration(%d)=nil, want error (libopus 'value<0 || value>%d goto bad_arg')", step, v, dredMaxFrames)
		}
		if valid {
			want = v
		}
		got, gErr := enc.DREDDuration()
		if gErr != nil {
			t.Fatalf("step %d: DREDDuration() error=%v", step, gErr)
		}
		if got != want {
			t.Fatalf("step %d: DREDDuration()=%d after SetDREDDuration(%d) (valid=%v), want %d",
				step, got, v, valid, want)
		}
	}
}

// TestDecoderCTLSequence_OSCERoundTrip asserts the decoder OSCE BWE / LACE
// activation CTLs round-trip (libopus OPUS_SET/GET_OSCE_BWE; LACE activation is
// the gopus extra-controls postfilter gate). RESET-equivalent reconstruction is
// covered by the default decoder reset path.
func TestDecoderCTLSequence_OSCERoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)
	if err := dec.SetComplexity(0); err != nil {
		t.Fatalf("SetComplexity(0): %v", err)
	}

	if v, err := dec.OSCEBWE(); err != nil || v {
		t.Fatalf("default OSCEBWE()=%v err=%v, want false/nil", v, err)
	}
	if v, err := dec.OSCELACE(); err != nil || v {
		t.Fatalf("default OSCELACE()=%v err=%v, want false/nil", v, err)
	}

	r := rand.New(rand.NewSource(2))
	wantBWE, wantLACE := false, false
	for step := 0; step < 200; step++ {
		switch r.Intn(2) {
		case 0:
			wantBWE = r.Intn(2) == 1
			if err := dec.SetOSCEBWE(wantBWE); err != nil {
				t.Fatalf("step %d: SetOSCEBWE(%v) error=%v", step, wantBWE, err)
			}
		default:
			wantLACE = r.Intn(2) == 1
			if err := dec.SetOSCELACE(wantLACE); err != nil {
				t.Fatalf("step %d: SetOSCELACE(%v) error=%v", step, wantLACE, err)
			}
		}
		gotBWE, _ := dec.OSCEBWE()
		gotLACE, _ := dec.OSCELACE()
		if gotBWE != wantBWE {
			t.Fatalf("step %d: OSCEBWE()=%v, want %v", step, gotBWE, wantBWE)
		}
		if gotLACE != wantLACE {
			t.Fatalf("step %d: OSCELACE()=%v, want %v", step, gotLACE, wantLACE)
		}
	}
}

func TestExtraControlsBuildExposesTopLevelControls(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	assertOptionalEncoderControls(t, enc)
	qext, qextOK := any(enc).(qextEncoderControl)
	if extsupport.QEXT {
		if !qextOK {
			t.Fatal("QEXT build does not expose encoder QEXT control")
		}
		assertSupportedQEXTControl(t, qext)
	} else if qextOK {
		t.Fatal("non-QEXT build unexpectedly exposes encoder QEXT control")
	}
	dred, ok := any(enc).(extraDREDControl)
	if !ok {
		t.Fatal("extra-controls build does not expose encoder DRED control")
	}
	assertWorkingDREDControl(t, dred)
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("top-level encoder SetDNNBlob did not propagate DRED-capable blob to the core encoder")
	}

	dec := newMonoTestDecoder(t)
	assertOptionalDecoderControls(t, dec)
	osce, ok := any(dec).(extraOSCEBWEControl)
	if !ok {
		t.Fatal("extra-controls build does not expose decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, osce)
	lace, ok := any(dec).(extraOSCELACEControl)
	if !ok {
		t.Fatal("extra-controls build does not expose decoder OSCE LACE control")
	}
	assertWorkingOSCELACEControl(t, lace)
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
	msQEXT, msQEXTOK := any(msEnc).(qextEncoderControl)
	if extsupport.QEXT {
		if !msQEXTOK {
			t.Fatal("QEXT build does not expose multistream encoder QEXT control")
		}
		assertSupportedQEXTControl(t, msQEXT)
	} else if msQEXTOK {
		t.Fatal("non-QEXT build unexpectedly exposes multistream encoder QEXT control")
	}
	msDred, ok := any(msEnc).(extraDREDControl)
	if !ok {
		t.Fatal("extra-controls build does not expose multistream encoder DRED control")
	}
	assertWorkingDREDControl(t, msDred)
	if !msEnc.enc.DREDModelLoaded() {
		t.Fatal("top-level multistream encoder SetDNNBlob did not propagate DRED-capable blob to child encoders")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	assertOptionalDecoderControls(t, msDec)
	msOSCE, ok := any(msDec).(extraOSCEBWEControl)
	if !ok {
		t.Fatal("extra-controls build does not expose multistream decoder OSCE BWE control")
	}
	assertWorkingOSCEBWEControl(t, msOSCE)
	msLACE, ok := any(msDec).(extraOSCELACEControl)
	if !ok {
		t.Fatal("extra-controls build does not expose multistream decoder OSCE LACE control")
	}
	assertWorkingOSCELACEControl(t, msLACE)
}

func assertWorkingOSCELACEControl(t *testing.T, dec extraOSCELACEControl) {
	t.Helper()

	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true) error: %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || !got {
		t.Fatalf("OSCELACE()=(%v,%v) want=(true,nil)", got, err)
	}
	if err := dec.SetOSCELACE(false); err != nil {
		t.Fatalf("SetOSCELACE(false) error: %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || got {
		t.Fatalf("OSCELACE()=(%v,%v) want=(false,nil)", got, err)
	}
}
