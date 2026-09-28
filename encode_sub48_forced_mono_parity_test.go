package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestSub48StereoForcedMonoCELTParity codes stereo input as a mono CELT
// stream at the sub-48 kHz API rates. compute_mdcts folds the two channel
// spectra before its upsample scaling, so every packet byte and final range
// must match the same-architecture libopus opus_encode_float oracle.
func TestSub48StereoForcedMonoCELTParity(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}
	const nFrames = 8
	durs := []ExpertFrameDuration{ExpertFrameDuration2_5Ms, ExpertFrameDuration10Ms, ExpertFrameDuration20Ms}
	rcModes := []BitrateMode{BitrateModeCBR, BitrateModeVBR, BitrateModeCVBR}
	for _, fs := range []int{8000, 12000, 16000, 24000} {
		for di, dur := range durs {
			rc := rcModes[di%len(rcModes)]
			t.Run(fmt.Sprintf("fs%d/%s", fs, sub48DurationLabel(dur)), func(t *testing.T) {
				frame := sub48NativeFrameSamples(fs, dur)
				src, err := testsignal.GenerateCorpusSignal(testsignal.CorpusMusicV1, fs, frame*nFrames*2, 2)
				if err != nil {
					t.Fatalf("GenerateCorpusSignal: %v", err)
				}
				vbr, constraint := vbrFlags(rc)
				recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
					SampleRate:    fs,
					Channels:      2,
					Application:   libopustest.EncodeDiffApplicationAudio,
					ForceMode:     libopustest.EncodeDiffForceModeCELTOnly,
					Bandwidth:     libopustest.EncodeDiffBandwidthFullband,
					MaxBandwidth:  libopustest.EncodeDiffBandwidthFullband,
					Bitrate:       48000,
					Complexity:    10,
					Signal:        libopustest.EncodeDiffSignalMusic,
					VBR:           vbr,
					VBRConstraint: constraint,
					ForceChannels: 1,
					FrameSize:     frame,
					FrameCount:    nFrames,
					PCM:           src,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "encode diff oracle", err)
					return
				}
				enc, err := NewEncoder(EncoderConfig{SampleRate: fs, Channels: 2, Application: ApplicationAudio})
				if err != nil {
					t.Fatal(err)
				}
				for _, set := range []error{
					enc.SetMode(EncoderModeCELT),
					enc.SetBandwidth(BandwidthFullband),
					enc.SetMaxBandwidth(BandwidthFullband),
					enc.SetBitrate(48000),
					enc.SetBitrateMode(rc),
					enc.SetComplexity(10),
					enc.SetSignal(SignalMusic),
					enc.SetForceChannels(1),
					enc.SetFrameSize(frame),
					enc.SetExpertFrameDuration(dur),
				} {
					if set != nil {
						t.Fatalf("configure gopus encoder: %v", set)
					}
				}
				for f := range nFrames {
					pkt, err := encDiffEncodeOneFrame(enc, src[f*frame*2:(f+1)*frame*2])
					if err != nil {
						t.Fatalf("frame %d: %v", f, err)
					}
					o := recs[f]
					if !bytes.Equal(pkt, o.Packet) {
						t.Fatalf("frame %d: packet differs at byte %d (gopus len=%d libopus len=%d)",
							f, firstByteDiff(pkt, o.Packet), len(pkt), len(o.Packet))
					}
					if got := enc.FinalRange(); got != o.FinalRange {
						t.Fatalf("frame %d: final range gopus=%08x libopus=%08x", f, got, o.FinalRange)
					}
				}
			})
		}
	}
}
