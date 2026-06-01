package gopus

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestEncodeDiffSILKCBRFloorFinding pins the one arch-INDEPENDENT encode
// divergence surfaced by TestEncodeDifferentialFuzz: SILK NB 10 ms CBR at the
// 6 kbps rate floor produces a packet that overruns the CBR byte target.
//
// It drives a deterministic low-level constant tone (so the result does not
// depend on any float-analysis near-tie — this isolates a genuine same-arch
// rate-control/framing gap from the documented arm64 ≤1-ULP float boundary) and
// asserts:
//
//   - The 6 kbps NB 10 ms CBR corner diverges in the TOC framing: gopus emits a
//     code-0 (unpadded) packet larger than the CBR target where libopus emits a
//     code-3 packet padded to the target (its SILK target-rate control throttles
//     to a near-empty frame; gopus's does not). This is logged, not failed, as
//     the documented finding (the harness excludes this exact corner).
//
//   - Every adjacent config is byte-exact on the same tone: 8/12/16 kbps at NB
//     10 ms, 6/8 kbps at NB 20 ms, and 12 kbps at WB 10 ms. This bounds the
//     finding to the single floor corner and proves it is not a broad CBR or SILK
//     framing bug.
func TestEncodeDiffSILKCBRFloorFinding(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}
	const fs = 48000
	const nFrames = 10

	type kase struct {
		name        string
		bwCode      int
		gbw         Bandwidth
		frameMs     ExpertFrameDuration
		frameSz     int
		bitrate     int
		expectFloor bool // the 6 kbps NB 10 ms corner is the documented finding
	}
	cases := []kase{
		{"nb_10ms_6k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 6000, true},
		{"nb_10ms_8k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 8000, false},
		{"nb_10ms_12k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 12000, false},
		{"nb_10ms_16k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration10Ms, 480, 16000, false},
		{"nb_20ms_6k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration20Ms, 960, 6000, false},
		{"nb_20ms_8k", libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, ExpertFrameDuration20Ms, 960, 8000, false},
		{"wb_10ms_12k", libopustest.EncodeDiffBandwidthWideband, BandwidthWideband, ExpertFrameDuration10Ms, 480, 12000, false},
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

			diverged := false
			for f := 0; f < nFrames; f++ {
				frame := pcm[f*c.frameSz : (f+1)*c.frameSz]
				pkt, eerr := enc.EncodeFloat32(frame)
				if eerr != nil {
					t.Fatalf("frame %d: %v", f, eerr)
				}
				o := recs[f]
				if !bytes.Equal(pkt, o.Packet) {
					diverged = true
					if c.expectFloor {
						t.Logf("frame %d: documented SILK CBR floor finding — gopus toc=%02x(len=%d) "+
							"overruns CBR target; libopus toc=%02x(len=%d) code-3 padded",
							f, byte0(pkt), len(pkt), byte0(o.Packet), len(o.Packet))
					} else {
						t.Errorf("frame %d: UNEXPECTED divergence at %s (br=%d): gopus toc=%02x(len=%d) libopus toc=%02x(len=%d)",
							f, c.name, c.bitrate, byte0(pkt), len(pkt), byte0(o.Packet), len(o.Packet))
					}
				}
			}
			if c.expectFloor && !diverged {
				t.Logf("note: %s did not diverge on this tone (finding is frame-dependent at the floor)", c.name)
			}
		})
	}
}
