package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestInitialStereoToMonoMatchesLibopus locks the first-frame channel choice
// against the live C encoder. A new encoder starts with stream_channels=2 and
// prev_channels=0, so choosing mono at low rate does not arm toMono.
func TestInitialStereoToMonoMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize  = 480
		frameCount = 3
		bitrate    = 14049
	)
	pcm := make([]float32, frameSize*frameCount*2)
	for i := range frameSize * frameCount {
		phase := 2 * math.Pi * float64(i) / 48000
		pcm[2*i] = float32(0.2 * math.Sin(220*phase))
		pcm[2*i+1] = float32(0.2 * math.Sin(340*phase))
	}
	for _, tc := range []struct {
		name      string
		forceMode int
		goMode    Mode
	}{
		{"auto", libopustest.EncodeDiffForceModeAuto, ModeAuto},
		{"forced_silk", libopustest.EncodeDiffForceModeSILKOnly, ModeSILK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate:  48000,
				Channels:    2,
				Application: libopustest.EncodeDiffApplicationAudio,
				ForceMode:   tc.forceMode,
				Bitrate:     bitrate,
				Complexity:  10,
				Signal:      libopustest.EncodeDiffSignalAuto,
				FrameSize:   frameSize,
				FrameCount:  frameCount,
				PCM:         pcm,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "initial stereo reference", err)
				return
			}
			if len(recs) != frameCount {
				t.Fatalf("C records=%d want=%d", len(recs), frameCount)
			}
			enc := NewEncoder(48000, 2)
			enc.SetMode(tc.goMode)
			enc.SetBitrate(bitrate)
			enc.SetBitrateMode(ModeCBR)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()
			for frame := range frameCount {
				input := pcm[frame*frameSize*2 : (frame+1)*frameSize*2]
				got, encodeErr := enc.EncodeFloat32WithAnalysisMaxBytes(input, frameSize, input, 4000)
				if encodeErr != nil {
					t.Fatalf("frame %d: Go encode: %v", frame, encodeErr)
				}
				want := recs[frame]
				if want.Ret < 0 || len(want.Packet) != want.Ret {
					t.Fatalf("frame %d: invalid C record ret=%d len=%d", frame, want.Ret, len(want.Packet))
				}
				if frame == 0 && (len(want.Packet) == 0 || want.Packet[0]&0x04 != 0) {
					t.Fatalf("C first frame does not choose mono: packet=%x", want.Packet)
				}
				if !bytes.Equal(got, want.Packet) {
					t.Errorf("frame %d: packet Go=%x C=%x", frame, got, want.Packet)
				}
				if gotRange := enc.FinalRange(); gotRange != want.FinalRange {
					t.Errorf("frame %d: final range Go=%08x C=%08x", frame, gotRange, want.FinalRange)
				}
			}
		})
	}
}
