// sample_diff_trace_test.go - Find exact samples with large differences
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"sort"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestFindLargeSampleDiffs finds samples with the largest differences between gopus and libopus
func TestFindLargeSampleDiffs(t *testing.T) {
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

	// Decode up to packet 60 to build state
	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 61 (first transient)
	pkt61 := packets[61]
	goPcm, _ := goDec.DecodeFloat32(pkt61)
	libPcm, libN := libDec.DecodeFloat(pkt61, 5760)

	n := minInt(len(goPcm), libN*2)

	// Collect all differences with their indices
	type sampleDiff struct {
		idx       int
		goval     float32
		libval    float32
		diff      float64
		channel   string
		sampleIdx int // sample index within channel
	}

	var diffs []sampleDiff
	for i := 0; i < n; i++ {
		d := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		ch := "L"
		if i%2 == 1 {
			ch = "R"
		}
		diffs = append(diffs, sampleDiff{
			idx:       i,
			goval:     goPcm[i],
			libval:    libPcm[i],
			diff:      d,
			channel:   ch,
			sampleIdx: i / 2,
		})
	}

	// Sort by difference (descending)
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].diff > diffs[j].diff
	})

	t.Log("Top 20 largest sample differences in packet 61:")
	for i := 0; i < 20 && i < len(diffs); i++ {
		d := diffs[i]
		block := d.sampleIdx / 120 // which short block (0-7)
		posInBlock := d.sampleIdx % 120
		t.Logf("  [%d] idx=%d (block=%d pos=%d %s): go=%.8f lib=%.8f diff=%.2e",
			i+1, d.idx, block, posInBlock, d.channel, d.goval, d.libval, d.diff)
	}

	// Compute statistics
	var sumDiff, sumAbsDiff float64
	var maxDiff float64
	for _, d := range diffs {
		sumDiff += float64(d.goval) - float64(d.libval)
		sumAbsDiff += d.diff
		if d.diff > maxDiff {
			maxDiff = d.diff
		}
	}

	t.Logf("\nStatistics for %d samples:", n)
	t.Logf("  Max diff: %.2e", maxDiff)
	t.Logf("  Avg abs diff: %.2e", sumAbsDiff/float64(n))
	t.Logf("  Sum diff (bias): %.2e", sumDiff)

	// Show diff distribution by block
	t.Log("\nDiff distribution by block:")
	for block := 0; block < 8; block++ {
		var blockSumDiff, blockMaxDiff float64
		for _, d := range diffs {
			if d.sampleIdx/120 == block {
				blockSumDiff += d.diff
				if d.diff > blockMaxDiff {
					blockMaxDiff = d.diff
				}
			}
		}
		t.Logf("  Block %d: sum=%.6f max=%.2e", block, blockSumDiff, blockMaxDiff)
	}

	// Compute cumulative error through de-emphasis simulation
	t.Log("\nSimulating de-emphasis error accumulation:")
	const coef = 0.85000610
	stateErr := 0.000035 // approximate initial state error

	for i := 0; i < n; i += 2 { // left channel only
		dx := float64(goPcm[i]) - float64(libPcm[i])
		// De-emphasis: state = coef * (x + state)
		// Error propagation: stateErr = coef * (dx + stateErr) ≈ coef * stateErr + coef * dx
		stateErr = coef*stateErr + coef*dx
	}

	t.Logf("Simulated final state error (L channel): %.6f", stateErr)
	t.Logf("Actual state error from test: 0.022")
}

// TestDeemphasisErrorGrowth traces how errors grow through de-emphasis
func TestDeemphasisErrorGrowth(t *testing.T) {
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

	const coef = 0.85000610

	// Track preemph state error over multiple packets
	t.Log("Tracking preemph state error growth across packets 55-70:")

	for i := 0; i < 70 && i < len(packets); i++ {
		goPcm, _ := goDec.DecodeFloat32(packets[i])
		libPcm, libN := libDec.DecodeFloat(packets[i], 5760)

		libMem0, _ := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()

		stateErr := math.Abs(goState[0] - float64(libMem0))

		if i >= 55 {
			// Compute synthesis error for this packet
			n := minInt(len(goPcm), libN*2)
			var synthSumErr float64
			for j := 0; j < n; j += 2 {
				synthSumErr += math.Abs(float64(goPcm[j]) - float64(libPcm[j]))
			}
			avgSynthErr := synthSumErr / float64(n/2)

			// Check if transient
			toc := gopus.ParseTOC(packets[i][0])
			marker := ""
			if stateErr > 0.001 {
				marker = " ***"
			}

			t.Logf("Pkt %d: frame=%d | state_err=%.6f synth_avg_err=%.2e%s",
				i, toc.FrameSize, stateErr, avgSynthErr, marker)
		}
	}
}
