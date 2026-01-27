package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func TestTmpSilkNativeMBWB(t *testing.T) {
	tests := []struct {
		name   string
		vector string
		fsHz   int
		delay  int
	}{
		{"MB", "testvector03", 12000, 10},
		{"WB", "testvector04", 16000, 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + tt.vector + ".bit"
			packets, err := loadPacketsSimple(bitFile, 3)
			if err != nil || len(packets) == 0 {
				t.Skip("Could not load packets")
			}

			for pktIdx, pkt := range packets {
				if len(pkt) == 0 {
					continue
				}
				toc := gopus.ParseTOC(pkt[0])
				if toc.Mode != gopus.ModeSILK {
					continue
				}
				silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
				if !ok {
					continue
				}
				duration := silk.FrameDurationFromTOC(toc.FrameSize)

				// gopus native decode (mono)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				goDec := silk.NewDecoder()
				goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
				if err != nil {
					t.Fatalf("gopus native decode failed: %v", err)
				}

				// libopus decode at native rate
				libDec, err := NewLibopusDecoder(tt.fsHz, 1)
				if err != nil || libDec == nil {
					t.Fatalf("libopus decoder create failed")
				}
				libPcm, libSamples := libDec.DecodeFloat(pkt, len(goNative)+tt.delay+10)
				libDec.Destroy()
				if libSamples < 0 {
					t.Fatalf("libopus decode failed")
				}

				minLen := len(goNative)
				if libSamples-tt.delay < minLen {
					minLen = libSamples - tt.delay
				}
				if minLen <= 0 {
					t.Fatalf("no samples to compare")
				}

				var sigPow, noisePow float64
				for i := 0; i < minLen; i++ {
					sig := float64(libPcm[i+tt.delay])
					diff := float64(goNative[i]) - sig
					sigPow += sig * sig
					noisePow += diff * diff
				}
				snr := 10 * math.Log10(sigPow/noisePow)
				if math.IsNaN(snr) || math.IsInf(snr, 1) {
					snr = 999.0
				}
				t.Logf("Packet %d: native SNR=%.2f dB (len=%d)", pktIdx, snr, minLen)
			}
		})
	}
}
