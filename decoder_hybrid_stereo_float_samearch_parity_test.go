package gopus_test

// Same-architecture parity gate for public multistream float-output decoding of
// mono and coupled-stereo Hybrid FB/SWB packets. The selected C reference matches
// the active feature and ISA build. FIXED_POINT converts mapped opus_res through
// RES2FLOAT; floating builds use opus_decode_float arithmetic.

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/multistream"
)

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

				compared := 0
				for fi, rec := range recs {
					if rec.Ret <= 1 || len(rec.Packet) == 0 {
						t.Fatalf("br=%d frame=%d: invalid oracle packet result=%d bytes=%d", bitrate, fi, rec.Ret, len(rec.Packet))
					}
					pkt := rec.Packet
					cfg := pkt[0] >> 3
					if cfg < 12 || cfg > 15 {
						continue // Check required Hybrid coverage for this bitrate below.
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

					for j := range want {
						if math.Float32bits(got[j]) != math.Float32bits(want[j]) {
							t.Fatalf("br=%d frame=%d TOC=0x%02x sample=%d bits=%08x want=%08x",
								bitrate, fi, pkt[0], j, math.Float32bits(got[j]), math.Float32bits(want[j]))
						}
					}
					compared++

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
				if compared == 0 {
					t.Fatalf("br=%d: no Hybrid packets compared among %d oracle records", bitrate, len(recs))
				}
			}
		})
	}
}
