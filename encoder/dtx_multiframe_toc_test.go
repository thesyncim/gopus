package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestDTXMultiFrameCELTHybridTOC pins the multi-frame (>20 ms) CELT/Hybrid DTX
// TOC-only packet. CELT and Hybrid have no single-frame TOC config beyond 20 ms,
// so a 40/60 ms packet in those modes is assembled from N=frameSize/960 internal
// 20 ms frames. When DTX fires for such a frame, libopus' per-sub-frame encode
// returns a 1-byte TOC for each sub-frame and the repacketizer collapses them to
// a TOC-only packet: code 1 for 2 sub-frames (1 byte total) or code 3 with a
// frame-count byte for 3 sub-frames (2 bytes total).
//
// This is the byte-exact target captured from the same-arch libopus
// opus_encode_float oracle (encode_stateful_transition_fuzz_test.go DTX-run
// sweep). Before the fix gopus' DTX path tried to build a SINGLE-frame TOC for
// the full 40/60 ms duration, which has no CELT/Hybrid config, and returned
// ErrInvalidConfig instead of the packet. This regression pins the framing so it
// stays byte-exact without needing the C oracle present.
func TestDTXMultiFrameCELTHybridTOC(t *testing.T) {
	const fs = 48000
	// Drive enough true-silence frames to cross the DTX-fire threshold
	// (NB_SPEECH_FRAMES_BEFORE_DTX*20*2 = 400 Q1) with margin at every duration.
	const silentFrames = 20

	type kase struct {
		name      string
		mode      Mode
		bw        types.Bandwidth
		channels  int
		frameSize int
		// wantTOC is the leading TOC byte; wantLen the full DTX packet length.
		wantTOC byte
		wantLen int
		// wantCount is the code-3 frame-count byte (only checked when wantLen==2).
		wantCount byte
	}
	cases := []kase{
		// CELT FB: config 31 (0xf8>>3). 40 ms -> code 1 (2 frames), 1 byte.
		{"celt_fb_40ms_mono", ModeCELT, types.BandwidthFullband, 1, 1920, 0xf9, 1, 0},
		// 60 ms -> code 3 (3 frames) + count byte 0x03, 2 bytes.
		{"celt_fb_60ms_mono", ModeCELT, types.BandwidthFullband, 1, 2880, 0xfb, 2, 0x03},
		// Stereo sets the TOC stereo bit (0x04).
		{"celt_fb_40ms_stereo", ModeCELT, types.BandwidthFullband, 2, 1920, 0xfd, 1, 0},
		{"celt_fb_60ms_stereo", ModeCELT, types.BandwidthFullband, 2, 2880, 0xff, 2, 0x03},
		// CELT WB: config 23. 40 ms code 1.
		{"celt_wb_40ms_mono", ModeCELT, types.BandwidthWideband, 1, 1920, 0xb9, 1, 0},
		// Hybrid FB: config 15 (0x78>>3). 40 ms code 1.
		{"hybrid_fb_40ms_mono", ModeHybrid, types.BandwidthFullband, 1, 1920, 0x79, 1, 0},
		{"hybrid_fb_60ms_mono", ModeHybrid, types.BandwidthFullband, 1, 2880, 0x7b, 2, 0x03},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			enc := NewEncoder(fs, c.channels)
			enc.SetMode(c.mode)
			enc.SetBandwidth(c.bw)
			enc.SetMaxBandwidth(c.bw)
			enc.SetFrameSize(c.frameSize)
			enc.SetBitrate(32000)
			enc.SetBitrateMode(ModeVBR)
			enc.SetComplexity(10)
			enc.SetDTX(true)
			if c.channels == 2 {
				enc.SetForceChannels(2)
			}

			silence := make([]float32, c.frameSize*c.channels)
			var pkt []byte
			fired := false
			for f := 0; f < silentFrames; f++ {
				p, err := enc.EncodeFloat32(silence, c.frameSize)
				if err != nil {
					t.Fatalf("frame %d: EncodeFloat32 error: %v", f, err)
				}
				// DTX TOC-only packet is at most 2 bytes; the first such frame marks
				// DTX has fired. Capture it.
				if len(p) <= 2 && len(p) >= 1 {
					pkt = append([]byte(nil), p...)
					fired = true
					break
				}
			}
			if !fired {
				t.Fatalf("%s: DTX never fired within %d silent frames", c.name, silentFrames)
			}
			if len(pkt) != c.wantLen {
				t.Fatalf("%s: DTX packet len=%d want %d (toc=%#02x)", c.name, len(pkt), c.wantLen, pkt[0])
			}
			if pkt[0] != c.wantTOC {
				t.Errorf("%s: DTX TOC=%#02x want %#02x", c.name, pkt[0], c.wantTOC)
			}
			if c.wantLen == 2 && pkt[1] != c.wantCount {
				t.Errorf("%s: DTX code-3 count byte=%#02x want %#02x", c.name, pkt[1], c.wantCount)
			}
		})
	}
}
