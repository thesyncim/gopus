//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestOracleCustomDynamicFFTExact(t *testing.T) {
	libopustest.RequireOracle(t)
	// This mode uses dynamically generated FFT tables at lengths 40..320.
	const sampleRate, frameSize, maxBytes = 48000, 640, 200
	pcm := generateSine(440, sampleRate, frameSize)
	ref := runCustomOracle(t, []oracleCase{{sampleRate, frameSize, 1, maxBytes, pcm}})[0]
	if ref.status < 0 || len(ref.decoded) != frameSize {
		t.Fatalf("C status=%d decoded samples=%d want=%d", ref.status, len(ref.decoded), frameSize)
	}
	mode, err := custom.NewMode(sampleRate, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.EncodeFloat(pcm, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, ref.packet) || enc.FinalRange() != ref.encRange {
		t.Fatalf("encoded packet/range: got=%x/%08x want=%x/%08x", packet, enc.FinalRange(), ref.packet, ref.encRange)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := dec.DecodeFloat(ref.packet, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != frameSize || dec.FinalRange() != ref.decRange {
		t.Fatalf("decoded samples/range=%d/%08x want=%d/%08x", len(decoded), dec.FinalRange(), frameSize, ref.decRange)
	}
	for i, sample := range decoded {
		if math.Float32bits(sample) != math.Float32bits(ref.decoded[i]) {
			t.Fatalf("decoded sample %d bits=%08x want=%08x", i, math.Float32bits(sample), math.Float32bits(ref.decoded[i]))
		}
	}
}

func TestCustomTransformResetMatchesFreshDecoder(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprint(channels), func(t *testing.T) {
			const rate, frame = 96000, 960
			pcm := generateSine(440, rate, frame)
			if channels == 2 {
				pcm = generateSineStereo(440, 523.25, rate, frame)
			}
			ref := runCustomOracle(t, []oracleCase{{rate, frame, channels, 200, pcm}})[0]
			if ref.status < 0 || len(ref.decoded) != frame*channels {
				t.Fatalf("C status=%d samples=%d", ref.status, len(ref.decoded))
			}
			mode, err := custom.NewMode(rate, frame)
			if err != nil {
				t.Fatal(err)
			}
			if mode.Overlap <= 120 {
				t.Fatal("mode does not exercise an overlap larger than standard Opus")
			}
			dec, err := custom.NewDecoder(mode, channels)
			if err != nil {
				t.Fatal(err)
			}
			var fresh []float32
			for cycle := 0; cycle < 3; cycle++ {
				if cycle > 0 {
					dec.Reset()
				}
				got, err := dec.DecodeFloat(ref.packet, frame)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(ref.decoded) || dec.FinalRange() != ref.decRange {
					t.Fatalf("cycle%d samples/range=%d/%08x want=%d/%08x", cycle, len(got), dec.FinalRange(), len(ref.decoded), ref.decRange)
				}
				if cycle == 0 {
					fresh = append([]float32(nil), got...)
				}
				for i, v := range got {
					if math.Float32bits(v) != math.Float32bits(fresh[i]) {
						t.Fatalf("cycle%d sample%d bits=%08x want=%08x", cycle, i, math.Float32bits(v), math.Float32bits(fresh[i]))
					}
				}
			}
		})
	}
}

// Geometry alone is insufficient: opus_custom_mode_create also requires FFT
// factors supported by kiss_fft.c:kf_factor. The live oracle supplies status.
func TestOracleCustomRejectsUnsupportedFFTFactors(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		frame  int
		status int32
		err    error
	}{{440, -7, custom.ErrAllocFail}, {882, -1, custom.ErrBadArg}} {
		ref := runCustomOracle(t, []oracleCase{{44100, tc.frame, 1, 200, generateSine(440, 44100, tc.frame)}})[0]
		if ref.status != tc.status {
			t.Fatalf("frame%d C status=%d want=%d", tc.frame, ref.status, tc.status)
		}
		if _, err := custom.NewMode(44100, tc.frame); err != tc.err {
			t.Fatalf("frame%d error=%v want=%v", tc.frame, err, tc.err)
		}
	}
}
