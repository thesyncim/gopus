//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedQEXTHybridSWBMonoPLCRecoveryMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameCount = 9
		seed       = int64(34994603)
	)
	spec := encodeSweepSpec{
		name:        "hybrid_swb_ch1_10ms_24000bps_vbr0_fecfalse_dtxfalse",
		application: ApplicationVoIP,
		mode:        EncoderModeHybrid,
		bandwidth:   BandwidthSuperwideband,
		frameMs:     ExpertFrameDuration10Ms,
		bitrate:     24000,
		channels:    1,
		vbr:         BitrateModeVBR,
	}
	rng := rand.New(rand.NewSource(seed))
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    spec.channels,
		Application: spec.application,
	})
	if err != nil {
		t.Fatalf("NewEncoder(%s): %v", spec.name, err)
	}
	for _, configure := range []func() error{
		func() error { return enc.SetMode(spec.mode) },
		func() error { return enc.SetFrameSize(spec.frameSamples48k()) },
		func() error { return enc.SetExpertFrameDuration(spec.frameMs) },
		func() error { return enc.SetBandwidth(spec.bandwidth) },
		func() error { return enc.SetBitrate(spec.bitrate) },
		func() error { return enc.SetBitrateMode(spec.vbr) },
		func() error { return enc.SetComplexity(10) },
	} {
		if err := configure(); err != nil {
			t.Fatalf("configure %s: %v", spec.name, err)
		}
	}
	enc.SetFEC(false)
	enc.SetDTX(false)
	encoded := make([][]byte, frameCount)
	for frame := range encoded {
		pcm := genPCM(rng, spec.frameSamples48k(), spec.channels, 48000)
		packet, err := encodeOneFrame(enc, pcm)
		if err != nil {
			t.Fatalf("encode %s frame %d: %v", spec.name, frame, err)
		}
		encoded[frame] = append([]byte(nil), packet...)
	}
	loss := plcDropPattern(rng, frameCount)
	// The fixed seed preserves the PCM and loss schedule independently of the
	// broader encoder sweep's ordering.
	packets := make([][]byte, frameCount)
	for frame := range packets {
		if !loss[frame] {
			packets[frame] = encoded[frame]
		}
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("%dhz", sampleRate), func(t *testing.T) {
			frameSize := fixedFrameSamplesAtRate(spec, sampleRate)
			assertFixedDecodeSequence(t, sampleRate, spec.channels, frameSize, packets)

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, spec.channels))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]int16, frameSize*spec.channels)
			decodeSequence := func() error {
				dec.Reset()
				for _, packet := range packets {
					n, err := dec.DecodeInt16(packet, out)
					if err != nil {
						return err
					}
					if n != frameSize {
						return fmt.Errorf("decoded %d samples, want %d", n, frameSize)
					}
				}
				return nil
			}
			if err := decodeSequence(); err != nil {
				t.Fatal(err)
			}
			var decodeErr error
			allocs := testing.AllocsPerRun(20, func() {
				decodeErr = decodeSequence()
			})
			if decodeErr != nil {
				t.Fatalf("warmed PLC/recovery sequence: %v", decodeErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed PLC/recovery sequence allocated %g times", allocs)
			}
		})
	}
}
