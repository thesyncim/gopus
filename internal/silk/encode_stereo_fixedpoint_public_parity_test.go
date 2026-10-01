//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPublicStereoSILKEncodeFixedByteExact compares complete stereo SILK
// packets and final ranges with the selected FIXED_POINT silk_Encode API.
// NB/MB/WB, CBR/VBR, 10/20/40/60ms packets and mid-only inputs exercise the
// actual channel state, stereo symbols, and entropy coder shared by all blocks.
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

			p := newTestPacketEncoder(c.bandwidth, 2)
			p.ctl.Complexity = 2
			p.ctl.BitRate = int32(c.bitrate)
			p.ctl.UseCBR = c.cbr
			p.ctl.MaxBits = int32(c.bitrate * c.frameMs * c.nFrames / 1000)
			assertFixedSILKAPI(t, p, [][]float32{interleaveStereo(left, right)}, -1)
		})
	}
}
