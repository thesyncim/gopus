//go:build trace
// +build trace

// Package cgo traces Laplace encoding differences between gopus and libopus.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestCompareLaplaceEncoding compares Laplace encoding between gopus and libopus.
func TestCompareLaplaceEncoding(t *testing.T) {
	// Test a range of QI values with the first band's model
	// For first frame (intra=false), band 0, LM=3:
	// - prob[0] = 72, prob[1] = 127 (from eProbModel[3][0])
	// - fs = prob[0] << 7 = 72 << 7 = 9216
	// - decay = prob[1] << 6 = 127 << 6 = 8128

	// eProbModel from decoder.go
	eProbModelLM3Inter := []int{72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128, 64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40, 114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11}

	// Test for band 0
	band := 0
	pi := 2 * band
	fs := eProbModelLM3Inter[pi] << 7      // 72 << 7 = 9216
	decay := eProbModelLM3Inter[pi+1] << 6 // 127 << 6 = 8128

	t.Logf("Band 0: fs=%d, decay=%d", fs, decay)

	// Encode several QI values and compare bytes
	testQIs := []int{0, 1, -1, 2, -2, 3, -3, 4, -4, 5}

	for _, qi := range testQIs {
		// Encode with gopus
		bufGo := make([]byte, 256)
		reGo := &rangecoding.Encoder{}
		reGo.Init(bufGo)

		// Call the encoding
		encodeLaplaceGo(reGo, qi, fs, decay)

		// Get the output bytes
		reGo.Done()
		goBytes := reGo.RangeBytes()
		goTell := reGo.Tell()

		t.Logf("QI=%+d: gopus bytes=%d tell=%d, first bytes: %02x",
			qi, goBytes, goTell, bufGo[:4])

		// Now decode to verify
		rdGo := &rangecoding.Decoder{}
		rdGo.Init(bufGo[:goBytes+5]) // Include some padding

		decodedQI := decodeLaplaceGo(rdGo, fs, decay)
		marker := ""
		if decodedQI != qi {
			marker = " <-- MISMATCH"
		}
		t.Logf("  Decoded QI=%+d%s", decodedQI, marker)
	}
}

// laplaceMinP = 1 (minimum probability)
// laplaceFTBits = 15 (total bits for frequency table)
// laplaceFS = 1 << 15 = 32768 (total frequency)
const (
	testLaplaceMinP    = 1
	testLaplaceFTBits  = 15
	testLaplaceFS      = 1 << testLaplaceFTBits
	testLaplaceLogMinP = 0 // log2(1) = 0
)

// ec_laplace_get_freq1 matches libopus ec_laplace_get_freq1
func testECLaplaceGetFreq1(fs, decay int) int {
	ft := 32768 // 1 << 15
	if fs+testLaplaceMinP > ft {
		return 0
	}
	return (fs * decay) >> 15
}

func encodeLaplaceGo(re *rangecoding.Encoder, val int, fs int, decay int) int {
	fl := 0
	if val != 0 {
		s := 0
		if val < 0 {
			s = -1
		}
		absVal := (val + s) ^ s
		fl = fs
		fs = testECLaplaceGetFreq1(fs, decay)
		i := 1
		for fs > 0 && i < absVal {
			fs *= 2
			fl += fs + 2*testLaplaceMinP
			fs = (fs * decay) >> 15
			i++
		}
		if fs == 0 {
			ndiMax := (testLaplaceFS - fl + testLaplaceMinP - 1) >> testLaplaceLogMinP
			ndiMax = (ndiMax - s) >> 1
			di := absVal - i
			if di > ndiMax-1 {
				di = ndiMax - 1
			}
			fl += (2*di + 1 + s) * testLaplaceMinP
			if testLaplaceFS-fl < testLaplaceMinP {
				fs = testLaplaceFS - fl
			} else {
				fs = testLaplaceMinP
			}
			absVal = i + di
			val = (absVal + s) ^ s
		} else {
			fs += testLaplaceMinP
			if s == 0 {
				fl += fs
			}
		}
	}
	if fl+fs > testLaplaceFS {
		fs = testLaplaceFS - fl
	}
	re.EncodeBin(uint32(fl), uint32(fl+fs), testLaplaceFTBits)
	return val
}

func decodeLaplaceGo(rd *rangecoding.Decoder, fs int, decay int) int {
	fm := rd.DecodeBin(testLaplaceFTBits)

	fl := 0
	val := 0

	if int(fm) < fs {
		// val = 0
	} else {
		fl = fs
		fs = testECLaplaceGetFreq1(fs, decay)
		val = 1
		for fs > 0 && fl+fs <= int(fm) {
			fl += fs
			fs += fs
			fs += 2 * testLaplaceMinP
			val++
			fs = (fs * decay) >> 15
		}
		if fs == 0 {
			ndi := (int(fm) - fl) / testLaplaceMinP
			val += ndi / 2
			if ndi%2 != 0 {
				val = -val
			}
			fs = testLaplaceMinP
			fl += ndi * testLaplaceMinP
		} else {
			fl += fs
			fs += testLaplaceMinP
			if fl > int(fm) {
				val = -val
				fl -= fs
			}
		}
	}
	rd.Update(uint32(fl), uint32(fl+fs), 1<<testLaplaceFTBits)
	return val
}

// TestCompareFullCoarseEncodeDecode compares full coarse energy encode/decode cycle.
func TestCompareFullCoarseEncodeDecode(t *testing.T) {
	// This test encodes coarse energies and then decodes them to verify roundtrip
	// We use the same energies that would be computed for a 440Hz sine wave

	// Simulated band energies for 440Hz sine wave (from earlier tests)
	// These are in mean-relative log2 scale (after eMeans subtraction)
	energies := []float64{
		2.0, 5.6, 6.8, 5.4, 3.6,
		3.0, 2.2, 2.4, 1.4, 0.6,
		0.8, 0.8, 0.8, 0.8, 0.8,
		0.8, 0.8, -0.2, 0.0, 0.0,
		1.0,
	}

	nbBands := 21
	lm := 3
	intra := false

	// Encode
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Simulate encoding (simplified without full encoder state)
	// Just encode the QI values directly

	// eProbModel from decoder.go (LM=3, inter)
	eProbModelLM3Inter := []int{72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128, 64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40, 114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11}

	// Compute QI values (simplified: just round energy / 6dB)
	qis := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		// This is a simplified version - real encoding uses prediction
		qis[i] = int(energies[i] / 6.0) // Very rough approximation
	}

	t.Log("QI values: ", qis)

	// Encode each band
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := eProbModelLM3Inter[pi] << 7
		decay := eProbModelLM3Inter[pi+1] << 6
		encodeLaplaceGo(re, qis[band], fs, decay)
	}

	bytesUsed := re.RangeBytes()
	re.Done()
	t.Logf("Encoded %d bands using %d bytes", nbBands, bytesUsed)

	// Decode
	rd := &rangecoding.Decoder{}
	rd.Init(buf[:bytesUsed+10])

	decodedQIs := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := eProbModelLM3Inter[pi] << 7
		decay := eProbModelLM3Inter[pi+1] << 6
		decodedQIs[band] = decodeLaplaceGo(rd, fs, decay)
	}

	t.Log("Decoded QIs: ", decodedQIs)

	// Compare
	match := true
	for i := 0; i < nbBands; i++ {
		if qis[i] != decodedQIs[i] {
			t.Logf("QI mismatch at band %d: encoded=%d decoded=%d", i, qis[i], decodedQIs[i])
			match = false
		}
	}
	if match {
		t.Log("All QIs match after roundtrip!")
	}

	_ = lm
	_ = intra
}
