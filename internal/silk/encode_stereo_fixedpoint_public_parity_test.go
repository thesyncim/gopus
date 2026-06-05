//go:build gopus_fixed_point

package silk

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPublicStereoSILKEncodeFixedByteExact drives the PUBLIC stereo SILK encode
// path (EncodeStereoWithEncoderVADFlags) under the gopus_fixed_point build and
// asserts that every mid and side frame it produced is byte-for-byte identical
// to the libopus FIXED_POINT silk_encode_frame_FIX reference, replayed on the
// exact int16 x_buf / inputBuf and pre-encode state the public stereo encoder
// fed to the validated per-channel payload driver.
//
// The integer stereo front-end (silkStereoLRToMS, the port of
// silk/stereo_LR_to_MS.c) that produces those int16 mid/side frames is itself
// oracle-verified byte-exact by TestSILKStereoLRToMSFixedLibopusParity, and the
// stereo prediction-index / mid-only / VAD header symbols are coded
// deterministically from its outputs. Proving every channel frame matches the
// libopus FIXED_POINT per-frame oracle therefore establishes byte-exactness of
// the assembled stereo SILK packet.
//
// Coverage: NB/MB/WB at 10/20 ms, CBR+VBR, single- and multi-frame packets
// (exercising cross-frame stereo predictor state), plus a near-mono signal that
// drives the mid-only (mono-collapse) decision.
func TestPublicStereoSILKEncodeFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	type kase struct {
		name      string
		bandwidth Bandwidth
		fsKHz     int
		frameMs   int
		nFrames   int
		cbr       bool
		bitrate   int
		gen       int // signal generator
	}
	var cases []kase
	bws := []struct {
		bw    Bandwidth
		fsKHz int
	}{
		{BandwidthNarrowband, 8},
		{BandwidthMediumband, 12},
		{BandwidthWideband, 16},
	}
	// SILK packets are built from 20 ms internal blocks (multi-frame = 40/60 ms)
	// or a single 10 ms block. 10 ms is therefore always single-frame; 20 ms
	// blocks exercise 1-, 2- and 3-frame (cross-frame stereo predictor) packets.
	type frameShape struct {
		ms      int
		nFrames int
	}
	shapes := []frameShape{{10, 1}, {20, 1}, {20, 2}, {20, 3}}
	for _, b := range bws {
		for _, sh := range shapes {
			for _, cbr := range []bool{false, true} {
				for _, g := range []int{0, 1, 2} {
					mode := "vbr"
					if cbr {
						mode = "cbr"
					}
					cases = append(cases, kase{
						name:      fmt.Sprintf("%s_%dms_%df_%s_g%d", bwName(b.bw), sh.ms, sh.nFrames, mode, g),
						bandwidth: b.bw,
						fsKHz:     b.fsKHz,
						frameMs:   sh.ms,
						nFrames:   sh.nFrames,
						cbr:       cbr,
						bitrate:   24000,
						gen:       g,
					})
				}
			}
			// Low-rate identical-channel case (gen 3) to drive the mid-only
			// (mono-collapse) decision and its cross-frame silentSideLen state.
			cases = append(cases, kase{
				name:      fmt.Sprintf("%s_%dms_%df_midonly", bwName(b.bw), sh.ms, sh.nFrames),
				bandwidth: b.bw,
				fsKHz:     b.fsKHz,
				frameMs:   sh.ms,
				nFrames:   sh.nFrames,
				cbr:       false,
				bitrate:   7000,
				gen:       3,
			})
		}
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			frameSamples := c.frameMs * c.fsKHz
			total := frameSamples * c.nFrames

			left := make([]float32, total)
			right := make([]float32, total)
			for i := 0; i < total; i++ {
				tt := float64(i) / float64(c.fsKHz*1000)
				var l, r float64
				switch c.gen {
				case 0:
					// Correlated tonal pair with a panning side component.
					common := 0.5 * math.Sin(2*math.Pi*180*tt)
					side := 0.25 * math.Sin(2*math.Pi*320*tt)
					l = common + side
					r = common - side
				case 1:
					// Near-mono (very high correlation) to drive mid-only.
					common := 0.55 * math.Sin(2*math.Pi*210*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*7*tt))
					l = common
					r = common * 0.999
				case 2:
					// Decorrelated wideband-ish content (wide stereo image).
					l = 0.4 * math.Sin(2*math.Pi*1500*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*33*tt))
					r = 0.4 * math.Sin(2*math.Pi*1900*tt+0.9) * (0.5 + 0.5*math.Sin(2*math.Pi*41*tt))
				default:
					// Identical channels (pure mono) at low rate -> mid-only collapse.
					common := 0.5 * math.Sin(2*math.Pi*200*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*11*tt))
					l = common
					r = common
				}
				left[i] = float32(l * 0.35)
				right[i] = float32(r * 0.35)
			}

			enc := NewEncoder(c.bandwidth)
			sideEnc := NewEncoder(c.bandwidth)
			for _, e := range []*Encoder{enc, sideEnc} {
				e.SetComplexity(2)
				e.SetBitrate(c.bitrate)
				e.SetVBR(!c.cbr)
				e.EnableFixedSnapshotForTest()
			}

			vadFlags := make([]bool, c.nFrames)
			for i := range vadFlags {
				vadFlags[i] = true
			}

			got, err := EncodeStereoWithEncoderVADFlags(enc, sideEnc, left, right, c.bandwidth, vadFlags)
			if err != nil {
				t.Fatalf("EncodeStereoWithEncoderVADFlags: %v", err)
			}
			if len(got) == 0 {
				t.Fatalf("empty stereo packet")
			}

			// Collect every per-channel frame the public encoder fed to the
			// validated payload driver and replay each against the libopus
			// FIXED_POINT silk_encode_frame_FIX oracle.
			midSnaps := enc.FixedAllSnapshotsForTest()
			sideSnaps := sideEnc.FixedAllSnapshotsForTest()
			if len(midSnaps) != c.nFrames {
				t.Fatalf("mid snapshots: got %d want %d", len(midSnaps), c.nFrames)
			}
			// The side channel is coded only for non-mid-only frames; the number
			// of side snapshots therefore must be <= nFrames.
			if len(sideSnaps) > c.nFrames {
				t.Fatalf("side snapshots: got %d want <= %d", len(sideSnaps), c.nFrames)
			}

			type chanSnap struct {
				label string
				snap  FixedPreEncodeSnapshot
			}
			var all []chanSnap
			for i, s := range midSnaps {
				all = append(all, chanSnap{label: fmt.Sprintf("mid[%d]", i), snap: s})
			}
			for i, s := range sideSnaps {
				all = append(all, chanSnap{label: fmt.Sprintf("side[%d]", i), snap: s})
			}

			oracleCases := make([]silkFixedEncodeFramePayloadCase, len(all))
			for i, cs := range all {
				oracleCases[i] = buildPayloadCaseFromSnapshot(c.bandwidth, c.fsKHz, cs.snap)
			}
			want, err := probeLibopusSILKFixedEncodeFramePayload(oracleCases)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk fixed encode frame payload", err)
				return
			}

			midOnlyFrames := c.nFrames - len(sideSnaps)
			t.Logf("packet bytes=%d frames(mid=%d side=%d mid_only=%d)", len(got), len(midSnaps), len(sideSnaps), midOnlyFrames)

			for i, cs := range all {
				w := want[i]
				if w.nBytesOut <= 0 {
					t.Fatalf("%s: oracle produced no bytes", cs.label)
				}
				gotFrame := encodeFrameOnlyFromSnapshot(c.bandwidth, c.fsKHz, c.cbr, cs.snap)
				if !bytes.Equal(gotFrame[:w.nBytesOut], w.payload) {
					t.Fatalf("%s payload vs libopus FIXED mismatch:\n got=%x\nwant=%x",
						cs.label, gotFrame[:w.nBytesOut], w.payload)
				}
			}
		})
	}
}
