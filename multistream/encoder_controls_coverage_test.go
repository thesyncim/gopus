package multistream

import (
	"errors"
	"math"
	"testing"

	encpkg "github.com/thesyncim/gopus/internal/encoder"
)

// TestEncoderControls_BroadcastRoundTrips covers the multistream Encoder control
// wrappers that fan a setting out to every per-stream encoder and read it back
// from the first stream (libopus opus_multistream_encoder_ctl broadcast / get-
// from-stream-0 semantics). Each subtest sets a value, verifies the aggregate
// getter reflects it, and confirms the value reached every child encoder.
func TestEncoderControls_BroadcastRoundTrips(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}

	t.Run("LowDelay", func(t *testing.T) {
		for _, want := range []bool{true, false} {
			enc.SetLowDelay(want)
			if got := enc.LowDelay(); got != want {
				t.Fatalf("LowDelay()=%v want %v", got, want)
			}
			for i, e := range enc.encoders {
				if got := e.LowDelay(); got != want {
					t.Fatalf("stream %d LowDelay()=%v want %v (broadcast)", i, got, want)
				}
			}
		}
	})

	t.Run("VoIPApplication", func(t *testing.T) {
		for _, want := range []bool{true, false} {
			enc.SetVoIPApplication(want)
			if got := enc.VoIPApplication(); got != want {
				t.Fatalf("VoIPApplication()=%v want %v", got, want)
			}
			for i, e := range enc.encoders {
				if got := e.VoIPApplication(); got != want {
					t.Fatalf("stream %d VoIPApplication()=%v want %v (broadcast)", i, got, want)
				}
			}
		}
	})

	t.Run("BitrateMode", func(t *testing.T) {
		for _, want := range []encpkg.BitrateMode{encpkg.ModeVBR, encpkg.ModeCVBR, encpkg.ModeCBR} {
			enc.SetBitrateMode(want)
			if got := enc.BitrateMode(); got != want {
				t.Fatalf("BitrateMode()=%v want %v", got, want)
			}
			for i, e := range enc.encoders {
				if got := e.GetBitrateMode(); got != want {
					t.Fatalf("stream %d GetBitrateMode()=%v want %v (broadcast)", i, got, want)
				}
			}
		}
	})

	t.Run("ForceChannels", func(t *testing.T) {
		for _, want := range []int{1, 2, -1} {
			enc.SetForceChannels(want)
			if got := enc.ForceChannels(); got != want {
				t.Fatalf("ForceChannels()=%d want %d", got, want)
			}
			for i, e := range enc.encoders {
				if got := e.ForceChannels(); got != want {
					t.Fatalf("stream %d ForceChannels()=%d want %d (broadcast)", i, got, want)
				}
			}
		}
	})
}

// TestEncoderControls_InBandFEC covers SetInBandFEC across its valid range plus
// its error path, and verifies the configuration broadcasts and reads back from
// stream 0. SetInBandFEC differs from SetFEC in that it carries the numeric
// libopus FEC config (0=disabled, 1=enabled, 2=music-safe).
func TestEncoderControls_InBandFEC(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}

	for _, cfg := range []int{
		encpkg.InBandFECDisabled,
		encpkg.InBandFECEnabled,
		encpkg.InBandFECMusicSafe,
	} {
		if err := enc.SetInBandFEC(cfg); err != nil {
			t.Fatalf("SetInBandFEC(%d) error: %v", cfg, err)
		}
		if got := enc.InBandFEC(); got != cfg {
			t.Fatalf("InBandFEC()=%d want %d", got, cfg)
		}
		for i, e := range enc.encoders {
			if got := e.InBandFEC(); got != cfg {
				t.Fatalf("stream %d InBandFEC()=%d want %d (broadcast)", i, got, cfg)
			}
		}
	}

	// Out-of-range config returns an error and leaves the prior value intact.
	if err := enc.SetInBandFEC(encpkg.InBandFECMusicSafe); err != nil {
		t.Fatalf("SetInBandFEC(music-safe) error: %v", err)
	}
	if err := enc.SetInBandFEC(99); err == nil {
		t.Fatal("SetInBandFEC(99) accepted an out-of-range config")
	}
	if got := enc.InBandFEC(); got != encpkg.InBandFECMusicSafe {
		t.Fatalf("InBandFEC()=%d after rejected set, want %d unchanged", got, encpkg.InBandFECMusicSafe)
	}
}

// TestEncoderControls_RestrictedSilkApplication covers the SetRestrictedSilk
// broadcast wrapper. The underlying encoder exposes no getter, so this asserts
// the wrapper applies cleanly for both states (exercising the broadcast loop).
func TestEncoderControls_RestrictedSilkApplication(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	enc.SetRestrictedSilkApplication(true)
	enc.SetRestrictedSilkApplication(false)
}

// TestEncoderControls_Mode covers the aggregate Mode() getter, which reports the
// base mode from the first stream encoder. SetMode broadcasts; Mode() mirrors it.
func TestEncoderControls_Mode(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, want := range []encpkg.Mode{encpkg.ModeCELT, encpkg.ModeSILK, encpkg.ModeHybrid} {
		enc.SetMode(want)
		if got := enc.Mode(); got != want {
			t.Fatalf("Mode()=%v want %v", got, want)
		}
	}
}

// TestEncoderControls_EncodeFloat32 covers the EncodeFloat32 convenience wrapper,
// which is an alias for Encode. It must produce a decodable packet for a basic
// stereo frame.
func TestEncoderControls_EncodeFloat32(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	const frameSize = 960
	pcm := make([]float32, frameSize*2)
	for i := range frameSize {
		v := float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
		pcm[2*i] = v
		pcm[2*i+1] = v
	}

	packet, err := enc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("EncodeFloat32 returned an empty packet")
	}

	// Length mismatch must surface ErrInvalidInput, same as Encode.
	if _, err := enc.EncodeFloat32(pcm[:frameSize], frameSize); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("EncodeFloat32 short input: got %v, want ErrInvalidInput", err)
	}
}
