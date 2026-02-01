package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

func TestPVQRoundTrip(t *testing.T) {
	// Create a simple unit-norm shape (mimics what encoder produces)
	n := 8  // Band width
	k := 10 // Number of pulses

	// Create a shape that's roughly what we'd see for a tonal signal
	shape := make([]float64, n)
	shape[0] = 0.3
	shape[1] = 0.7
	shape[2] = 0.5
	shape[3] = 0.2
	shape[4] = 0.1
	shape[5] = 0.1
	shape[6] = 0.05
	shape[7] = 0.05

	// Normalize to unit L2 norm
	var norm float64
	for _, v := range shape {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	for i := range shape {
		shape[i] /= norm
	}

	t.Logf("Input shape (L2=%.4f):", norm)
	for i, v := range shape {
		t.Logf("  shape[%d] = %.4f", i, v)
	}

	// Encode
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(re)
	enc.EncodeBandPVQ(shape, n, k)

	data := re.Done()
	t.Logf("\nEncoded data: %d bytes", len(data))

	// Decode
	rd := &rangecoding.Decoder{}
	rd.Init(data)

	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	decodedShape := dec.DecodePVQWithTrace(0, n, k)

	t.Logf("\nDecoded shape:")
	for i, v := range decodedShape {
		t.Logf("  shape[%d] = %.4f (orig=%.4f)", i, v, shape[i])
	}

	// Compute correlation
	var sumProd, sumOrig2, sumDec2 float64
	for i := range shape {
		sumProd += shape[i] * decodedShape[i]
		sumOrig2 += shape[i] * shape[i]
		sumDec2 += decodedShape[i] * decodedShape[i]
	}
	corr := sumProd / (math.Sqrt(sumOrig2*sumDec2) + 1e-10)
	t.Logf("\nCorrelation: %.4f", corr)

	if corr < 0.9 {
		t.Errorf("PVQ round-trip correlation too low: %.4f", corr)
	}
}
