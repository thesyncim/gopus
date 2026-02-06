package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestSILK10msRoundtripDirect tests SILK 10ms encoding + decoding directly.
// This bypasses the Opus wrapper to isolate SILK-level issues.
func TestSILK10msRoundtripDirect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bw     Bandwidth
		subfrL int
	}{
		{"NB", BandwidthNarrowband, 40},
		{"WB", BandwidthWideband, 80},
	} {
		for _, nSF := range []int{2, 4} {
			durMs := nSF * 5
			frameSamples := nSF * tc.subfrL
			cfg := GetBandwidthConfig(tc.bw)
			fsKHz := cfg.SampleRate / 1000
			t.Run(fmt.Sprintf("%s-%dms", tc.name, durMs), func(t *testing.T) {
				enc := NewEncoder(tc.bw)
				enc.SetBitrate(32000)

				numFrames := 10
				totalSamples := numFrames * frameSamples
				pcm := make([]float32, totalSamples+frameSamples)
				for i := range pcm {
					pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*float64(i)/float64(cfg.SampleRate)))
				}

				dec := NewDecoder()

				for i := 0; i < numFrames; i++ {
					start := i * frameSamples
					end := start + frameSamples
					pkt := enc.EncodeFrame(pcm[start:end], nil, true)
					if pkt == nil || len(pkt) == 0 {
						t.Logf("Frame %d: nil/empty packet", i)
						continue
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)

					// Decode using SILK decoder directly via Decode()
					// frameSizeSamples must be at 48kHz for FrameDurationFromTOC
					frameSizeAt48k := frameSamples * 48000 / cfg.SampleRate
					out, err := dec.Decode(cp, tc.bw, frameSizeAt48k, true)
					if err != nil {
						t.Logf("Frame %d: decode error: %v", i, err)
						continue
					}
					n := len(out)

					var energy float64
					for j := 0; j < n; j++ {
						energy += float64(out[j]) * float64(out[j])
					}
					rms := math.Sqrt(energy / float64(n))
					origRMS := 0.5 / math.Sqrt(2)

					if i >= 3 {
						ratio := rms / origRMS * 100
						t.Logf("Frame %d: %d bytes, decoded %d, RMS=%.4f ratio=%.1f%%",
							i, len(cp), n, rms, ratio)
					}
				}

				// Also test encoder + range decoder parity: encode a single frame
				// and manually decode indices to check bitstream correctness.
				t.Run("indices-parity", func(t *testing.T) {
					enc2 := NewEncoder(tc.bw)
					enc2.SetBitrate(32000)

					// Warmup frames
					for i := 0; i < 3; i++ {
						pkt := enc2.EncodeFrame(pcm[:frameSamples], nil, true)
						_ = pkt
					}

					// Encode test frame
					pkt := enc2.EncodeFrame(pcm[:frameSamples], nil, true)
					if pkt == nil || len(pkt) < 2 {
						t.Skip("empty packet")
					}

					// Try to decode indices using range decoder
					rd := &rangecoding.Decoder{}
					rd.Init(pkt)

					// Read VAD+LBRR header (2 bits for 1 frame)
					headerICDF := []uint16{uint16(256 - (256 >> 2)), 0}
					header := rd.DecodeICDF16(headerICDF, 8)
					vadFlag := (header >> 1) & 1
					lbrrFlag := header & 1
					t.Logf("Header: VAD=%d, LBRR=%d", vadFlag, lbrrFlag)

					// Read LBRR flag
					if lbrrFlag != 0 {
						t.Logf("LBRR present (unexpected for test)")
					}

					// Read frame type
					var ix int
					if vadFlag != 0 {
						ix = rd.DecodeICDF(silk_type_offset_VAD_iCDF, 8) + 2
					} else {
						ix = rd.DecodeICDF(silk_type_offset_no_VAD_iCDF, 8)
					}
					signalType := ix >> 1
					quantOffset := ix & 1
					t.Logf("SignalType=%d, QuantOffset=%d", signalType, quantOffset)

					// Read gains
					msb := rd.DecodeICDF(silk_gain_iCDF[signalType], 8)
					lsb := rd.DecodeICDF(silk_uniform8_iCDF, 8)
					gain0 := (msb << 3) + lsb
					t.Logf("Gain[0]: MSB=%d, LSB=%d, idx=%d", msb, lsb, gain0)

					for sf := 1; sf < nSF; sf++ {
						delta := rd.DecodeICDF(silk_delta_gain_iCDF, 8)
						t.Logf("Gain[%d]: delta=%d", sf, delta)
					}

					// Read NLSF stage1
					var cb *nlsfCB
					if tc.bw == BandwidthWideband {
						cb = &silk_NLSF_CB_WB
					} else {
						cb = &silk_NLSF_CB_NB_MB
					}
					stypeBand := signalType >> 1
					cb1Offset := stypeBand * cb.nVectors
					stage1 := rd.DecodeICDF(cb.cb1ICDF[cb1Offset:], 8)
					t.Logf("NLSF stage1=%d", stage1)

					ecIx := make([]int16, cb.order)
					predQ8 := make([]uint8, cb.order)
					silkNLSFUnpack(ecIx, predQ8, cb, stage1)

					for i := 0; i < cb.order; i++ {
						idx := rd.DecodeICDF(cb.ecICDF[int(ecIx[i]):], 8)
						if idx == 0 {
							idx -= rd.DecodeICDF(silk_NLSF_EXT_iCDF, 8)
						} else if idx == 2*nlsfQuantMaxAmplitude {
							idx += rd.DecodeICDF(silk_NLSF_EXT_iCDF, 8)
						}
						_ = idx - nlsfQuantMaxAmplitude
					}

					// NLSF interpolation: only for 4 subframes
					if nSF == maxNbSubfr {
						interp := rd.DecodeICDF(silk_NLSF_interpolation_factor_iCDF, 8)
						t.Logf("NLSF interp=%d", interp)
					} else {
						t.Logf("NLSF interp: skipped (10ms, %d subframes)", nSF)
					}

					// If voiced, read pitch
					if signalType == typeVoiced {
						_, contourICDF, lagLowICDF := pitchLagTables(fsKHz, nSF)
						lagMSB := rd.DecodeICDF(silk_pitch_lag_iCDF[fsKHz-8:], 8)
						lagLSB := rd.DecodeICDF(lagLowICDF, 8)
						contour := rd.DecodeICDF(contourICDF, 8)
						t.Logf("Pitch: lagMSB=%d, lagLSB=%d, contour=%d", lagMSB, lagLSB, contour)

						// LTP
						per := rd.DecodeICDF(silk_LTP_per_index_iCDF, 8)
						t.Logf("LTP PER=%d", per)
						for sf := 0; sf < nSF; sf++ {
							cbIdx := rd.DecodeICDF(silk_LTP_gain_iCDF_ptrs[per], 8)
							t.Logf("LTP[%d]: cbIdx=%d", sf, cbIdx)
						}

						// LTP scale (only independent)
						ltpScale := rd.DecodeICDF(silk_LTPscale_iCDF, 8)
						t.Logf("LTP scale=%d", ltpScale)
					}

					// Seed
					seed := rd.DecodeICDF(silk_uniform4_iCDF, 8)
					t.Logf("Seed=%d", seed)
					t.Logf("Bits used: %d", rd.Tell())
				})
			})
		}
	}
}
