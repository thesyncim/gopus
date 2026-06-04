// dtx_multiframe_framing_parity_test.go is a byte-structure parity gate for the
// multi-frame (>20ms CELT/Hybrid, >60ms SILK) DTX packet framing. libopus runs
// decide_dtx_mode once per internal sub-frame (opus_encoder.c:1769-1831), so when
// a trailing sub-frame DTXs while an earlier sub-frame is still active the packet
// is a MIX of real and length-0 frames (code 2 for two sub-frames, code 3 VBR for
// three) padded to the CBR target — NOT collapsed to an all-empty TOC-only
// packet. This drives a decaying speech->silence stream at 40/60ms and asserts
// that gopus's per-packet framing (TOC frame-code, sub-frame count, which
// sub-frames are length-0, and padding presence) matches the libopus oracle for
// every packet. The deep CELT payload bytes are NOT compared because they carry
// the documented darwin/arm64 ≤1-ULP FMA drift (project_arm64_celt_1ulp_drift.md)
// — the framing is integer-exact on every arch.
//
//go:build gopus_libopus_oracle

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// dtxFadePCMSequence builds a PCM stream: speechFrames full-amplitude voiced
// frames, then fadeFrames linearly-fading voiced frames, then silenceFrames zero
// frames, then resumeFrames full-amplitude voiced frames. The fade walks the
// per-sub-frame activity/energy decision across sub-frame boundaries so the DTX
// counter can cross its threshold mid-packet (trailing-DTX), and the resume
// segment exercises the speech-onset packet where an early sub-frame is still
// DTX'd while a later sub-frame carries payload.
func dtxFadePCMSequence(frameSize, channels, speechFrames, fadeFrames, silenceFrames, resumeFrames int) []float32 {
	total := (speechFrames + fadeFrames + silenceFrames + resumeFrames) * frameSize * channels
	pcm := make([]float32, total)
	frameIdx := 0
	emit := func(fi int, amp float64) {
		for i := 0; i < frameSize; i++ {
			n := frameIdx*frameSize + i
			tt := float64(n) / 48000.0
			env := 0.85 + 0.15*math.Sin(2*math.Pi*1.1*tt)
			s := 0.30*math.Sin(2*math.Pi*110*tt) +
				0.18*math.Sin(2*math.Pi*220*tt+0.08) +
				0.10*math.Sin(2*math.Pi*330*tt+0.21) +
				0.06*math.Sin(2*math.Pi*440*tt+0.35)
			v := float32(amp * env * s)
			off := fi * frameSize * channels
			for ch := 0; ch < channels; ch++ {
				pcm[off+i*channels+ch] = v
			}
		}
	}
	for fi := 0; fi < speechFrames; fi++ {
		emit(fi, 1.0)
		frameIdx++
	}
	for j := 0; j < fadeFrames; j++ {
		amp := 1.0 - float64(j+1)/float64(fadeFrames+1)
		emit(speechFrames+j, amp)
		frameIdx++
	}
	frameIdx += silenceFrames // silence segment already zero
	for j := 0; j < resumeFrames; j++ {
		emit(speechFrames+fadeFrames+silenceFrames+j, 1.0)
		frameIdx++
	}
	return pcm
}

// packetFraming is the arch-independent framing fingerprint of an Opus packet:
// the TOC frame-code, the sub-frame count, which sub-frames carry no payload
// (length 0, i.e. DTX'd), and whether the packet is padded.
type packetFraming struct {
	code       byte
	count      int
	zeroFrames string // "01.." one char per sub-frame: '1' => length 0
	padded     bool
}

func framingOf(pkt []byte) (packetFraming, error) {
	var starts [48]int
	var lens [48]int
	toc, count, _, _, err := packetFrameLayout(pkt, &starts, &lens)
	if err != nil {
		return packetFraming{}, err
	}
	pf := packetFraming{code: toc & 0x03, count: count}
	for i := 0; i < count; i++ {
		if lens[i] == 0 {
			pf.zeroFrames += "1"
		} else {
			pf.zeroFrames += "0"
		}
	}
	if toc&0x03 == 3 && len(pkt) >= 2 {
		pf.padded = pkt[1]&0x40 != 0
	}
	return pf, nil
}

func TestDTXMultiframeFramingParity(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getDTXSeqHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dtx multiframe framing", err)
	}

	type kase struct {
		name      string
		frameSize int
		mode      string
		bw        string
		bitrate   int
	}
	cases := []kase{
		{"celt_fb_40ms_mono", 1920, "celt", "fb", 64000},
		{"celt_fb_60ms_mono", 2880, "celt", "fb", 64000},
		{"hybrid_fb_40ms_mono", 1920, "hybrid", "fb", 48000},
		{"hybrid_fb_60ms_mono", 2880, "hybrid", "fb", 48000},
		// SILK >60ms packets are also multi-frame (80ms->2x40, 100ms->5x20,
		// 120ms->2x60, opus_encoder.c:1713-1725). The old whole-frame DTX path
		// could not build a TOC for these (no single-frame SILK config beyond
		// 60ms) and errored; the per-sub-frame path frames them correctly.
		{"silk_wb_80ms_mono", 3840, "silk", "wb", 16000},
		{"silk_wb_100ms_mono", 4800, "silk", "wb", 16000},
		{"silk_wb_120ms_mono", 5760, "silk", "wb", 16000},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			sawMixedDTX := false
			for sp := 4; sp <= 18; sp++ {
				for fade := 0; fade <= 8; fade++ {
					pcm := dtxFadePCMSequence(c.frameSize, 1, sp, fade, 24, 8)
					want := runDTXOracleCBR(t, helperPath, pcm, c.frameSize, 1, c.bitrate, c.bw, c.mode)
					got := runGopusDTXSequenceCBR(t, pcm, c.frameSize, 1, c.bitrate, c.bw, c.mode)
					if len(got) != len(want) {
						t.Fatalf("[sp=%d fade=%d] packet count gopus=%d libopus=%d", sp, fade, len(got), len(want))
					}
					for i := range want {
						gf, gErr := framingOf(got[i])
						wf, wErr := framingOf(want[i].Data)
						if gErr != nil || wErr != nil {
							t.Fatalf("[sp=%d fade=%d] frame %d parse err gopus=%v libopus=%v", sp, fade, i, gErr, wErr)
						}
						if gf != wf {
							t.Errorf("[sp=%d fade=%d] frame %d FRAMING mismatch\n  gopus  %+v\n  libopus %+v", sp, fade, i, gf, wf)
						}
						// A "mixed" packet has both a zero-length and a non-zero
						// sub-frame: the regression we are guarding against.
						if wf.count > 1 {
							hasZero, hasNonZero := false, false
							for _, ch := range wf.zeroFrames {
								if ch == '1' {
									hasZero = true
								} else {
									hasNonZero = true
								}
							}
							if hasZero && hasNonZero {
								sawMixedDTX = true
							}
						}
					}
				}
			}
			if !sawMixedDTX {
				t.Logf("%s: sweep produced no mixed real+DTX multi-frame packet (parity still asserted)", c.name)
			}
		})
	}
}
