package gopus

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestEncodeDiffSILKCBRFloorFinding pins byte-exactness vs the same-arch libopus
// oracle at the SILK NB 10 ms CBR 6 kbps rate floor (and adjacent corners).
//
// It drives a deterministic low-level constant tone (so the result does not
// depend on any float-analysis near-tie — this isolates a genuine same-arch
// rate-control/framing path from the documented arm64 ≤1-ULP float boundary).
//
// At the 6 kbps floor the SILK rate-control loop can bust its maxBits target on
// a voiced frame; libopus then signals PLC (a single zero payload byte) and
// CBR-pads the packet to a code-3 frame (opus_encoder.c lines 2580-2599). gopus
// now mirrors that busted-target path, so every frame is byte-exact across the
// floor and the adjacent 8/12/16 kbps NB 10 ms, 6/8 kbps NB 20 ms, and 12 kbps
// WB 10 ms configs.
func TestEncodeDiffSILKCBRFloorFinding(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}
	const fs = 48000
	const nFrames = 10

	type kase struct {
		name    string
		bwCode  int
		gbw     Bandwidth
		frameMs ExpertFrameDuration
		frameSz int
		bitrate int
	}
	cases := []kase{
		{"nb_10ms_6k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 6000},
		{"nb_10ms_8k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 8000},
		{"nb_10ms_12k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 12000},
		{"nb_10ms_16k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 16000},
		{"nb_20ms_6k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration20Ms, 960, 6000},
		{"nb_20ms_8k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration20Ms, 960, 8000},
		{"wb_10ms_12k", libopustest.EncodeDiffBandwidthWideband, BandwidthWideband, ExpertFrameDuration10Ms, 480, 12000},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			pcm := make([]float32, c.frameSz*nFrames)
			for i := range pcm {
				pcm[i] = float32(0.05 * math.Sin(2*math.Pi*220*float64(i)/fs))
			}
			recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate: fs, Channels: 1, Application: libopustest.EncodeDiffApplicationAudio,
				ForceMode: libopustest.EncodeDiffForceModeSILKOnly, Bandwidth: c.bwCode, MaxBandwidth: c.bwCode,
				Bitrate: c.bitrate, Complexity: 10, Signal: libopustest.EncodeDiffSignalVoice,
				VBR: false, ForceChannels: 1, FrameSize: c.frameSz, FrameCount: nFrames, PCM: pcm,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "encode diff oracle", err)
				return
			}
			enc, err := NewEncoder(EncoderConfig{SampleRate: fs, Channels: 1, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			enc.SetMode(EncoderModeSILK)
			enc.SetBandwidth(c.gbw)
			enc.SetMaxBandwidth(c.gbw)
			enc.SetBitrate(c.bitrate)
			enc.SetComplexity(10)
			enc.SetBitrateMode(BitrateModeCBR)
			enc.SetSignal(SignalVoice)
			enc.SetForceChannels(1)
			if err := enc.SetFrameSize(c.frameSz); err != nil {
				t.Fatalf("SetFrameSize: %v", err)
			}
			if err := enc.SetExpertFrameDuration(c.frameMs); err != nil {
				t.Fatalf("SetExpertFrameDuration: %v", err)
			}

			for f := 0; f < nFrames; f++ {
				frame := pcm[f*c.frameSz : (f+1)*c.frameSz]
				pkt, eerr := enc.EncodeFloat32(frame)
				if eerr != nil {
					t.Fatalf("frame %d: %v", f, eerr)
				}
				o := recs[f]
				if !bytes.Equal(pkt, o.Packet) {
					t.Errorf("frame %d: divergence at %s (br=%d): gopus toc=%02x(len=%d) libopus toc=%02x(len=%d)",
						f, c.name, c.bitrate, byte0(pkt), len(pkt), byte0(o.Packet), len(o.Packet))
				}
			}
		})
	}
}
