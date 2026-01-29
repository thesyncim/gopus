// overlap_energy_trace_test.go - Trace overlap buffer energy to detect divergence
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestOverlapEnergyAndOutputCorrelation traces if output errors correlate with overlap state
func TestOverlapEnergyAndOutputCorrelation(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	t.Log("Tracking output error growth and its correlation with overlap state:")
	t.Log("Pkt | Transient | Synth AvgErr | State Err | Overlap Energy")
	t.Log("----+-----------+--------------+-----------+---------------")

	for i := 0; i < 75 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Get overlap energy BEFORE decoding
		overlapBefore := goDec.GetCELTDecoder().OverlapBuffer()
		var overlapEnergy float64
		for _, v := range overlapBefore {
			overlapEnergy += v * v
		}

		goPcm, _ := goDec.DecodeFloat32(pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		// Compute synthesis error
		n := minInt(len(goPcm), libN*2)
		var synthErr float64
		for j := 0; j < n; j++ {
			synthErr += math.Abs(float64(goPcm[j]) - float64(libPcm[j]))
		}
		avgSynthErr := synthErr / float64(n)

		// Get preemph state error
		libMem0, _ := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()
		stateErr := math.Abs(goState[0] - float64(libMem0))

		// Detect transient
		isTransient := false
		if len(pkt) > 1 && toc.Mode == gopus.ModeCELT && toc.FrameSize == 960 {
			// Simplified transient detection - check if output varies significantly
			// A better way would be to parse the packet, but this is a rough indicator
			if i >= 61 && i <= 65 {
				// We know these are transient from previous tests
				isTransient = (i == 61 || i == 62 || i == 64)
			}
		}

		transientStr := "   "
		if isTransient {
			transientStr = " T "
		}

		// Only print interesting packets (transients or high error)
		if i >= 55 && i <= 75 {
			t.Logf("%3d |%s| %.2e     | %.6f  | %.2f",
				i, transientStr, avgSynthErr, stateErr, overlapEnergy)
		}
	}
}

// TestResetAndDecodeTransient tests if the transient error is caused by accumulated state
func TestResetAndDecodeTransient(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	t.Log("Comparing transient decode with full history vs fresh decoder:")

	// Method 1: Decode all packets up to 61, then decode 61
	goDec1, _ := gopus.NewDecoder(48000, 2)
	libDec1, _ := NewLibopusDecoder(48000, 2)
	defer libDec1.Destroy()

	for i := 0; i < 61; i++ {
		goDec1.DecodeFloat32(packets[i])
		libDec1.DecodeFloat(packets[i], 5760)
	}

	// Method 2: Fresh decoder, decode only packet 61
	goDec2, _ := gopus.NewDecoder(48000, 2)
	libDec2, _ := NewLibopusDecoder(48000, 2)
	defer libDec2.Destroy()

	// Decode packet 61 with both methods
	goPcm1, _ := goDec1.DecodeFloat32(packets[61])
	libPcm1, libN1 := libDec1.DecodeFloat(packets[61], 5760)

	goPcm2, _ := goDec2.DecodeFloat32(packets[61])
	libPcm2, libN2 := libDec2.DecodeFloat(packets[61], 5760)

	// Compare
	n1 := minInt(len(goPcm1), libN1*2)
	var snr1Sig, snr1Noise float64
	for j := 0; j < n1; j++ {
		s := float64(libPcm1[j])
		d := float64(goPcm1[j]) - s
		snr1Sig += s * s
		snr1Noise += d * d
	}
	snr1 := 10 * math.Log10(snr1Sig/snr1Noise)

	n2 := minInt(len(goPcm2), libN2*2)
	var snr2Sig, snr2Noise float64
	for j := 0; j < n2; j++ {
		s := float64(libPcm2[j])
		d := float64(goPcm2[j]) - s
		snr2Sig += s * s
		snr2Noise += d * d
	}
	snr2 := 10 * math.Log10(snr2Sig/snr2Noise)

	// Get preemph state
	libMem1, _ := libDec1.GetPreemphState()
	goState1 := goDec1.GetCELTDecoder().PreemphState()
	stateErr1 := math.Abs(goState1[0] - float64(libMem1))

	libMem2, _ := libDec2.GetPreemphState()
	goState2 := goDec2.GetCELTDecoder().PreemphState()
	stateErr2 := math.Abs(goState2[0] - float64(libMem2))

	t.Logf("With 60 frames of history:")
	t.Logf("  SNR: %.1f dB, State err: %.6f", snr1, stateErr1)
	t.Logf("  go state: %.8f, lib state: %.8f", goState1[0], libMem1)

	t.Logf("With fresh decoder (no history):")
	t.Logf("  SNR: %.1f dB, State err: %.6f", snr2, stateErr2)
	t.Logf("  go state: %.8f, lib state: %.8f", goState2[0], libMem2)

	// The key insight: if fresh decoder gives better results, the issue is state accumulation
	if snr2 > snr1+5 {
		t.Logf("INSIGHT: Fresh decoder has %.1f dB better SNR - state accumulation is the issue!", snr2-snr1)
	}
}

// TestComparePreAndPostDeemphasis attempts to isolate pre-deemphasis error
func TestComparePreAndPostDeemphasis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Create decoders and sync state up to packet 60
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Get initial state
	libMemBefore, _ := libDec.GetPreemphState()
	goStateBefore := goDec.GetCELTDecoder().PreemphState()

	t.Logf("Before packet 61:")
	t.Logf("  go state: %.10f", goStateBefore[0])
	t.Logf("  lib state: %.10f", libMemBefore)
	t.Logf("  diff: %.10f", math.Abs(goStateBefore[0]-float64(libMemBefore)))

	// Decode packet 61
	goPcm, _ := goDec.DecodeFloat32(packets[61])
	libPcm, libN := libDec.DecodeFloat(packets[61], 5760)

	// Get final state
	libMemAfter, _ := libDec.GetPreemphState()
	goStateAfter := goDec.GetCELTDecoder().PreemphState()

	t.Logf("After packet 61:")
	t.Logf("  go state: %.10f", goStateAfter[0])
	t.Logf("  lib state: %.10f", libMemAfter)
	t.Logf("  diff: %.10f", math.Abs(goStateAfter[0]-float64(libMemAfter)))

	// Compute what the state SHOULD be based on the output samples
	// state = coef * last_output_sample (pre-scale)
	// The output is scaled by 1/32768, so pre-scale sample = output * 32768
	const coef = 0.85000610

	n := minInt(len(goPcm), libN*2)
	if n >= 2 {
		// Last L sample
		lastGoL := float64(goPcm[n-2]) * 32768
		lastLibL := float64(libPcm[n-2]) * 32768

		expectedGoState := coef * lastGoL
		expectedLibState := coef * lastLibL

		t.Logf("Expected state from last sample:")
		t.Logf("  go expected: %.10f (last sample %.6f, pre-scale %.6f)", expectedGoState, goPcm[n-2], lastGoL)
		t.Logf("  lib expected: %.10f (last sample %.6f, pre-scale %.6f)", expectedLibState, libPcm[n-2], lastLibL)
		t.Logf("  Actual go state: %.10f", goStateAfter[0])
		t.Logf("  Actual lib state: %.10f", libMemAfter)

		// Check if our expected matches actual
		goStateMatch := math.Abs(expectedGoState-goStateAfter[0]) < 0.001
		libStateMatch := math.Abs(expectedLibState-float64(libMemAfter)) < 0.001

		t.Logf("  go state matches expected: %v", goStateMatch)
		t.Logf("  lib state matches expected: %v", libStateMatch)
	}
}
