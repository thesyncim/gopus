package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/testvectors"
)

// Temporary: compare gopus vs libopus for CELT mono/stereo packets with stereo output.
func TestTmpCELTStereoCompare(t *testing.T) {
	// Ensure tracer is disabled
	original := celt.DefaultTracer
	celt.SetTracer(&celt.NoopTracer{})
	defer celt.SetTracer(original)

	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skip("Could not load packets")
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

	var monoSig, monoNoise float64
	var stereoSig, stereoNoise float64
	monoPackets := 0
	stereoPackets := 0

	for _, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt.Data[0])

		goOut, err := goDec.DecodeFloat32(pkt.Data)
		if err != nil {
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
		if n == 0 {
			continue
		}
		if toc.Stereo {
			stereoPackets++
		} else {
			monoPackets++
		}
		for i := 0; i < n; i++ {
			sig := float64(libOut[i])
			diff := float64(goOut[i]) - sig
			if toc.Stereo {
				stereoSig += sig * sig
				stereoNoise += diff * diff
			} else {
				monoSig += sig * sig
				monoNoise += diff * diff
			}
		}
	}

	snr := func(sig, noise float64) float64 {
		if noise == 0 {
			return 999.0
		}
		return 10 * math.Log10(sig/noise)
	}

	t.Logf("mono packets=%d SNR=%.2f dB", monoPackets, snr(monoSig, monoNoise))
	t.Logf("stereo packets=%d SNR=%.2f dB", stereoPackets, snr(stereoSig, stereoNoise))
}
