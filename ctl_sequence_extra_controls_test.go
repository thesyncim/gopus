//go:build gopus_osce

// ctl_sequence_extra_controls_test.go fuzzes the gopus_osce-gated CTLs
// (encoder OPUS_SET/GET_DRED_DURATION, decoder OPUS_SET/GET_OSCE_BWE and the
// OSCE LACE activation) for get-after-set and boundary-validation parity with
// the libopus #ifdef-gated semantics.
//
// The default float libopus reference (RefPath) compiles these CTLs out
// (config.h leaves ENABLE_DRED / ENABLE_OSCE_BWE undefined), so the generic
// ctl-sequence oracle cannot stand in here. Instead the libopus-documented
// behaviour is asserted inline, citing the reference source:
//
//   - OPUS_SET_DRED_DURATION (opus_encoder.c): "if(value<0 || value>DRED_MAX_FRAMES)
//     goto bad_arg; st->dred_duration = value". DRED_MAX_FRAMES = 4*DRED_MAX_LATENTS
//     = 4*26 = 104 (dnn/dred_config.h), which equals internal/dred.MaxFrames.
//   - OPUS_SET_OSCE_BWE (opus_decoder.c): "if(value<0 || value>1) goto bad_arg;
//     st->DecControl.enable_osce_bwe = value". The gopus setter takes a bool, so
//     the {0,1} validation is enforced by the type; round-trip is asserted here.

package gopus

import (
	"math/rand"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
)

// TestEncoderCTLSequence_DREDDurationFuzz drives seeded DRED-duration SET/GET
// programs (with boundary / out-of-range values) and asserts the validation
// boundary and get-after-set match libopus OPUS_SET/GET_DRED_DURATION.
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
