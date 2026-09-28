package gopus_test

// Same-architecture parity gate for public multistream float-output decoding of
// mono and coupled-stereo Hybrid FB/SWB packets. The selected C reference matches
// the active feature and ISA build. FIXED_POINT converts mapped opus_res through
// RES2FLOAT; floating builds use opus_decode_float arithmetic.

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/multistream"
)

func hybridStereoFloatBudget() float64 {
	if hybridStereoFixedPointBuild {
		return 0
	}
	if runtime.GOARCH == "amd64" {
		return 0
	}
	// The floating CELT/Hybrid path stays within its documented arm64 ULP budget.
	return 1e-6
}

func TestHybridStereoFloatSameArchParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960 // 20 ms @ 48k
		frameCount = 6
	)

	// Distinct, decorrelated L/R content so the SILK stereo MS->LR unmixing and the
	// CELT highband both carry meaningful energy.
	makePCM := func(channels int) []float32 {
		pcm := make([]float32, frameSize*channels*frameCount)
		for f := range frameCount {
			for i := range frameSize {
				tt := float64(f*frameSize+i) / float64(sampleRate)
				base := f*frameSize + i
				if channels == 2 {
					pcm[(base)*2] = float32(0.5*math.Sin(2*math.Pi*440*tt) + 0.1*math.Sin(2*math.Pi*1900*tt))
					pcm[(base)*2+1] = float32(0.4*math.Sin(2*math.Pi*523*tt) + 0.12*math.Sin(2*math.Pi*2600*tt))
				} else {
					pcm[base] = float32(0.5*math.Sin(2*math.Pi*440*tt) + 0.1*math.Sin(2*math.Pi*1900*tt))
				}
			}
		}
		return pcm
	}

	cases := []struct {
		name      string
		channels  int
		forceCh   int
		bandwidth int
	}{
		{"coupled-stereo/FB", 2, 2, libopustest.OpusBandwidthFullband},
		{"coupled-stereo/SWB", 2, 2, libopustest.OpusBandwidthSuperwideband},
		{"mono/FB", 1, 1, libopustest.OpusBandwidthFullband},
		{"mono/SWB", 1, 1, libopustest.OpusBandwidthSuperwideband},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := makePCM(tc.channels)
			for _, bitrate := range []int{24000, 32000, 48000, 64000, 96000, 128000} {
				recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
					SampleRate: sampleRate, Channels: tc.channels,
					Application:   libopustest.EncodeDiffApplicationAudio,
					ForceMode:     libopustest.OpusForceModeHybrid,
					Bandwidth:     tc.bandwidth,
					Bitrate:       bitrate,
					Complexity:    10,
					Signal:        3002, // OPUS_SIGNAL_MUSIC
					VBR:           true,
					VBRConstraint: false,
					ForceChannels: tc.forceCh,
					FrameSize:     frameSize,
					FrameCount:    frameCount,
					PCM:           pcm,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "hybrid encode oracle", err)
				}

				coupled := 0
				mapping := []byte{0}
				if tc.channels == 2 {
					coupled = 1
					mapping = []byte{0, 1}
				}

				for fi, rec := range recs {
					if rec.Ret <= 1 || len(rec.Packet) == 0 {
						continue
					}
					pkt := rec.Packet
					cfg := pkt[0] >> 3
					if cfg < 12 || cfg > 15 {
						continue // not Hybrid; skip
					}

					// Fresh multistream decoder per packet isolates frame state and
					// matches the oracle's fresh decoder for each case.
					dec, err := multistream.NewDecoder(sampleRate, tc.channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got, err := dec.DecodeToFloat32(pkt, frameSize)
					if err != nil {
						t.Fatalf("br=%d frame=%d decode: %v", bitrate, fi, err)
					}

					res, err := libopustest.ProbeDecodeDiff(sampleRate, tc.channels, []libopustest.DecodeDiffCase{{
						Packet:    pkt,
						Format:    libopustest.DecodeDiffFormatFloat32,
						FrameSize: uint32(frameSize),
					}})
					if err != nil {
						libopustest.HelperUnavailable(t, "hybrid decode oracle", err)
					}
					if res[0].Code <= 0 {
						t.Fatalf("oracle decode code=%d", res[0].Code)
					}
					want := res[0].Float32()
					if len(got) != len(want) {
						t.Fatalf("length mismatch gopus=%d libopus=%d", len(got), len(want))
					}

					var maxAbs float64
					var maxIdx int
					for j := range want {
						d := math.Abs(float64(got[j]) - float64(want[j]))
						if d > maxAbs {
							maxAbs = d
							maxIdx = j
						}
					}
					if maxAbs > hybridStereoFloatBudget() {
						t.Fatalf("br=%d frame=%d TOC=0x%02x cfg=%d: hybrid stereo float decode not sample-exact: maxAbs=%g at idx=%d (gopus=%g libopus=%g, budget=%g)",
							bitrate, fi, pkt[0], cfg, maxAbs, maxIdx, got[maxIdx], want[maxIdx], hybridStereoFloatBudget())
					}

					if tc.name == "mono/FB" && bitrate == 24000 && fi == 0 {
						into := make([]float32, frameSize*tc.channels)
						for range 2 {
							if n, err := dec.DecodeIntoFloat32(pkt, into, frameSize); err != nil || n != frameSize {
								t.Fatalf("warm DecodeIntoFloat32=(%d,%v), want (%d,nil)", n, err, frameSize)
							}
						}
						if allocs := testing.AllocsPerRun(100, func() {
							if n, err := dec.DecodeIntoFloat32(pkt, into, frameSize); err != nil || n != frameSize {
								t.Fatalf("warm DecodeIntoFloat32=(%d,%v), want (%d,nil)", n, err, frameSize)
							}
						}); allocs != 0 {
							t.Fatalf("warm DecodeIntoFloat32 allocations=%g want 0", allocs)
						}
					}
				}
			}
		})
	}
}
