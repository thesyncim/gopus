package gopus_test

// Same-arch sample-exact gate for the coupled-stereo Hybrid (SILK+CELT) FLOAT
// decode path in the multistream per-stream decoder.
//
// A projection-decode bisection found that a coupled-stereo Hybrid FB/SWB 20 ms
// per-stream frame, decoded standalone on a fresh multistream stereo decoder,
// diverged from same-arch libopus opus_decode_float by maxAbs ~3.8e-3 on frame 0
// (and ~0.1-0.4 across a 6-frame projection sequence) -- far above the documented
// arm64 <=1-ULP CELT float drift budget.
//
// Root cause: the multistream per-stream Hybrid decode skipped the libopus
// opus_decode_frame redundancy-flag read (ec_dec_bit_logp(&dec,12) and the
// celt_to_silk / redundancy_bytes reads) that runs after SILK and before the CELT
// highband. Skipping it left the shared range decoder mis-positioned, so the CELT
// highband (start band 17, celt_accum) decoded from the wrong bits. The CELT->SILK
// redundant 5 ms frame was also not decoded on the shared CELT decoder before the
// main highband, leaving the main decode reading stale CELT state. The top-level
// gopus.Decoder already mirrors opus_decode_frame here and is sample-exact; the
// multistream streamState had a parallel Hybrid path that did not.
//
// This gate drives libopus (same arch, FLOAT build) to encode coupled-stereo and
// mono Hybrid FB/SWB packets, decodes each standalone on a fresh multistream
// decoder, and requires sample-exact agreement with libopus opus_decode_float
// (amd64-exact; arm64 absorbs only the documented <=1-ULP CELT drift).

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/multistream"
)

func hybridStereoFloatBudget() float64 {
	if runtime.GOARCH == "amd64" {
		return 0
	}
	// Documented darwin/arm64 <=1-ULP CELT/Hybrid float drift; three orders of
	// magnitude below the ~3.8e-3 real divergence this gate guards against.
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

					// Fresh multistream decoder per packet (frame-0 isolation),
					// mirroring the oracle's fresh-decoder-per-case decode.
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
				}
			}
		})
	}
}
