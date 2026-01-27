package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/testvectors"
)

// TestTmpVectorCompareLibopus compares gopus vs libopus across all packets for selected vectors.
func TestTmpVectorCompareLibopus(t *testing.T) {
	vectors := []string{"testvector03", "testvector04", "testvector05", "testvector06", "testvector07", "testvector08", "testvector09", "testvector10", "testvector12"}

	for _, name := range vectors {
		t.Run(name, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + name + ".bit"

			packets, err := testvectors.ReadBitstreamFile(bitFile)
			if err != nil {
				t.Skipf("Could not load %s: %v", name, err)
			}

			goDec, err := gopus.NewDecoder(48000, 2)
			if err != nil {
				t.Fatalf("gopus decoder: %v", err)
			}
			libDec, _ := NewLibopusDecoder(48000, 2)
			if libDec == nil {
				t.Fatalf("libopus decoder create failed")
			}
			defer libDec.Destroy()

			var sigPow, noisePow float64
			var total int
			for _, pkt := range packets {
				goOut, err := goDec.DecodeFloat32(pkt.Data)
				if err != nil {
					// skip errored packet (shouldn't happen)
					continue
				}
				libOut, libN := libDec.DecodeFloat(pkt.Data, 5760)
				if libN <= 0 {
					continue
				}
				n := libN * 2
				if len(goOut) < n {
					n = len(goOut)
				}
				for i := 0; i < n; i++ {
					diff := float64(goOut[i]) - float64(libOut[i])
					noisePow += diff * diff
					sigPow += float64(libOut[i]) * float64(libOut[i])
				}
				total += n
			}

			snr := 10 * math.Log10(sigPow/noisePow)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}
			t.Logf("gopus vs libopus SNR: %.2f dB (samples=%d)", snr, total)
		})
	}
}
