//go:build gopus_dred || gopus_extra_controls

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

func TestUpdateStereoDREDNeuralHistoryMirrorsPreservedPrefix(t *testing.T) {
	d := NewDecoder(2)
	const history = 6
	const frameSize = 2
	hist := make([]celtSig, history*2)
	for i := 0; i < history; i++ {
		hist[i] = celtSig(10 + i)
		hist[history+i] = celtSig(20 + i)
	}
	samples := []float32{
		100, 200,
		101, 201,
	}

	d.updateStereoDREDNeuralHistory(hist, frameSize, history, samples)

	wantL := []celtSig{12, 13, 14, 15, 100, 101}
	wantR := []celtSig{12, 13, 14, 15, 200, 201}
	for i, want := range wantL {
		if hist[i] != want {
			t.Fatalf("left[%d]=%v want %v", i, hist[i], want)
		}
	}
	for i, want := range wantR {
		if hist[history+i] != want {
			t.Fatalf("right[%d]=%v want %v", i, hist[history+i], want)
		}
	}
}
