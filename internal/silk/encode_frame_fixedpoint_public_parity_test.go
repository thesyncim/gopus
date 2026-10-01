//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPublicSILKEncodeFrameFixedByteExact compares complete mono SILK packets
// and final ranges with the selected FIXED_POINT silk_Encode API.
func TestPublicSILKEncodeFrameFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	type kase struct {
		name      string
		bandwidth Bandwidth
		fsKHz     int
		frameMs   int
		cbr       bool
		bitrate   int
		gen       int
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
	for _, b := range bws {
		for _, ms := range []int{20, 10} {
			for _, cbr := range []bool{false, true} {
				for _, g := range []int{0, 2} {
					mode := "vbr"
					if cbr {
						mode = "cbr"
					}
					cases = append(cases, kase{
						name:      fmt.Sprintf("%s_%dms_%s_g%d", bwName(b.bw), ms, mode, g),
						bandwidth: b.bw,
						fsKHz:     b.fsKHz,
						frameMs:   ms,
						cbr:       cbr,
						bitrate:   18000,
						gen:       g,
					})
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			frameSamples := c.frameMs * c.fsKHz

			pcm := make([]float32, frameSamples)
			for i := range pcm {
				tt := float64(i) / float64(c.fsKHz*1000)
				var v float64
				switch c.gen {
				case 0:
					v = 0.5*math.Sin(2*math.Pi*150*tt) + 0.2*math.Sin(2*math.Pi*450*tt)
				default:
					v = 0.4 * math.Sin(2*math.Pi*2500*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*40*tt))
				}
				pcm[i] = float32(v * 0.35)
			}

			p := newTestPacketEncoder(c.bandwidth, 1)
			p.ctl.Complexity = 2
			p.ctl.BitRate = int32(c.bitrate)
			p.ctl.UseCBR = c.cbr
			p.ctl.MaxBits = int32(c.bitrate * c.frameMs / 1000)
			assertFixedSILKAPI(t, p, [][]float32{pcm}, -1)
			if !p.enc.state[0].vadFlags[0] {
				t.Fatal("test signal is not VAD active")
			}
		})
	}
}

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func bwName(b Bandwidth) string {
	switch b {
	case BandwidthNarrowband:
		return "nb"
	case BandwidthMediumband:
		return "mb"
	case BandwidthWideband:
		return "wb"
	}
	return "?"
}
