//go:build gopus_dred || gopus_osce

package celt

import "testing"

func TestQuantizePLCPCM16kFrameMatchesLibopusFARGANIntGrid(t *testing.T) {
	frame := []float32{
		0,
		float32(0.5 / 32768),
		float32(1.5 / 32768),
		float32(-1.5 / 32768),
		float32(32766.6 / 32768),
		1.2,
		-1.2,
	}
	want := []float32{
		0,
		1.0 / 32768.0,
		2.0 / 32768.0,
		-1.0 / 32768.0,
		32767.0 / 32768.0,
		32767.0 / 32768.0,
		-32767.0 / 32768.0,
	}

	quantizePLCPCM16kFrame(frame)

	for i := range want {
		if frame[i] != want[i] {
			t.Fatalf("frame[%d]=%g want %g", i, frame[i], want[i])
		}
	}
}

func TestCommitStereoNeuralToDecodeMemMirrorsPreservedPrefix(t *testing.T) {
	d := NewDecoder(2)
	n := d.decodeMemHistoryLen()
	hist := make([]celtSig, 2*n)
	for i := range n {
		hist[i] = celtSig(10 + i)
		hist[n+i] = celtSig(-10 - i)
	}
	d.setDecodeHistory(hist)
	const frameSize = 2
	samples := make([]float32, 2*(frameSize+Overlap))
	for i := range frameSize + Overlap {
		samples[2*i] = float32(1000 + i)
		samples[2*i+1] = float32(2000 + i)
	}

	d.commitStereoNeuralToDecodeMem(samples, frameSize)

	for c := range 2 {
		mem := d.DecodeMem(c)
		for i := range n - frameSize {
			if want := celtSig(10 + frameSize + i); mem[i] != want {
				t.Fatalf("ch%d history[%d]=%v want %v", c, i, mem[i], want)
			}
		}
		for i := range frameSize + Overlap {
			if want := samples[2*i+c]; mem[n-frameSize+i] != want {
				t.Fatalf("ch%d new[%d]=%v want %v", c, i, mem[n-frameSize+i], want)
			}
		}
	}
}
