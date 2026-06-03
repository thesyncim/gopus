//go:build gopus_qext

package gopus_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// hd96kEncodeSine builds an interleaved native 96 kHz sine for the given
// channels/frameSize/frameCount.
func hd96kEncodeSine(channels, frameSize, frameCount int) []float32 {
	const fs = 96000.0
	n := frameSize * channels * frameCount
	pcm := make([]float32, n)
	for f := 0; f < frameCount; f++ {
		for i := 0; i < frameSize; i++ {
			idx := f*frameSize + i
			phase := 2 * math.Pi * 1000 * float64(idx) / fs
			base := (f*frameSize + i) * channels
			pcm[base] = float32(0.4 * math.Sin(phase))
			if channels == 2 {
				pcm[base+1] = float32(0.3 * math.Sin(phase+0.4))
			}
		}
	}
	return pcm
}

// TestQEXTEncode96kOracleAvailable confirms the native 96 kHz QEXT encode oracle
// (opus_encoder_create(96000) + OPUS_SET_QEXT) builds, runs, and produces a
// native code-3 CELT-only fullband packet for both mono and stereo. This pins
// the reference that the native HD96k encode routing is validated against.
func TestQEXTEncode96kOracleAvailable(t *testing.T) {
	const frameSize = 1920
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kEncodeSine(ch, frameSize, 1)
			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       256000,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 4000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			if len(res.Packets) != 1 || len(res.Packets[0]) == 0 {
				t.Fatalf("oracle produced no packet")
			}
			pkt := res.Packets[0]
			toc := pkt[0]
			// CELT-only fullband 20 ms -> config 31, code 3: TOC 0xfb.
			if toc&0x03 != 3 {
				t.Errorf("native 96k packet TOC code = %d, want 3 (got toc=0x%02x)", toc&0x03, toc)
			}
			t.Logf("native 96k QEXT %d-ch packet: len=%d toc=0x%02x finalRange=%d",
				ch, len(pkt), toc, res.FinalRanges[0])
		})
	}
}
