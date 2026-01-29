// postfilter_state_compare_test.go - Compare postfilter state between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestPostfilterStateCompare compares the state of decoders around the critical packets 59-60
func TestPostfilterStateCompare(t *testing.T) {
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

	t.Log("Tracing state divergence around packets 55-65:")
	t.Log("Pkt | Mode   | Postfilter | PreemphState Err | Output SNR")
	t.Log("----+--------+------------+------------------+-----------")

	for i := 0; i < 70 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := goDec.DecodeFloat32(pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		// Compute SNR
		n := minInt(len(goPcm), libN*2)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		// Get preemph state
		libMem0, _ := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()
		stateErr := math.Abs(goState[0] - float64(libMem0))

		// Check if postfilter is enabled in this packet
		postfilterStr := "  -  "
		if len(pkt) > 1 && toc.Mode == gopus.ModeCELT {
			postfilterStr = " off "
			// Simple check: postfilter flag is early in the CELT payload
			// For a proper check we'd need to parse the range coder
			// But we know from earlier tests that packets 59-60 have it on
			if i >= 59 && i <= 60 {
				postfilterStr = " ON  "
			}
		}

		modeStr := "CELT"
		if toc.Mode != gopus.ModeCELT {
			modeStr = "SILK"
		}

		// Log frames around the critical region
		if i >= 55 && i <= 70 {
			t.Logf(" %2d | %s   |%s| %.10f   | %7.1f dB",
				i, modeStr, postfilterStr, stateErr, snr)
		}
	}
}

// TestPostfilterMemoryCompare - compare the actual postfilter memory buffers
func TestPostfilterMemoryCompare(t *testing.T) {
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

	// Decode up to packet 58 (before postfilter packets)
	t.Log("Comparing state before postfilter packets (0-58):")
	goDec1, _ := gopus.NewDecoder(48000, 2)
	libDec1, _ := NewLibopusDecoder(48000, 2)
	defer libDec1.Destroy()

	for i := 0; i < 58; i++ {
		goDec1.DecodeFloat32(packets[i])
		libDec1.DecodeFloat(packets[i], 5760)
	}

	libMem58, _ := libDec1.GetPreemphState()
	goState58 := goDec1.GetCELTDecoder().PreemphState()
	t.Logf("  After 58 packets: go=%.10f, lib=%.10f, err=%.10e",
		goState58[0], libMem58, math.Abs(goState58[0]-float64(libMem58)))

	// Decode packet 59 (postfilter enabled)
	goPcm59, _ := goDec1.DecodeFloat32(packets[59])
	libPcm59, libN59 := libDec1.DecodeFloat(packets[59], 5760)

	n59 := minInt(len(goPcm59), libN59*2)
	var sig59, noise59 float64
	for j := 0; j < n59; j++ {
		s := float64(libPcm59[j])
		d := float64(goPcm59[j]) - s
		sig59 += s * s
		noise59 += d * d
	}
	snr59 := 10 * math.Log10(sig59/noise59)

	libMem59, _ := libDec1.GetPreemphState()
	goState59 := goDec1.GetCELTDecoder().PreemphState()
	t.Logf("  After packet 59: go=%.10f, lib=%.10f, err=%.10e, SNR=%.1f dB",
		goState59[0], libMem59, math.Abs(goState59[0]-float64(libMem59)), snr59)

	// Decode packet 60 (postfilter enabled)
	goPcm60, _ := goDec1.DecodeFloat32(packets[60])
	libPcm60, libN60 := libDec1.DecodeFloat(packets[60], 5760)

	n60 := minInt(len(goPcm60), libN60*2)
	var sig60, noise60 float64
	for j := 0; j < n60; j++ {
		s := float64(libPcm60[j])
		d := float64(goPcm60[j]) - s
		sig60 += s * s
		noise60 += d * d
	}
	snr60 := 10 * math.Log10(sig60/noise60)

	libMem60, _ := libDec1.GetPreemphState()
	goState60 := goDec1.GetCELTDecoder().PreemphState()
	t.Logf("  After packet 60: go=%.10f, lib=%.10f, err=%.10e, SNR=%.1f dB",
		goState60[0], libMem60, math.Abs(goState60[0]-float64(libMem60)), snr60)

	// Decode packet 61 (transient)
	goPcm61, _ := goDec1.DecodeFloat32(packets[61])
	libPcm61, libN61 := libDec1.DecodeFloat(packets[61], 5760)

	n61 := minInt(len(goPcm61), libN61*2)
	var sig61, noise61 float64
	for j := 0; j < n61; j++ {
		s := float64(libPcm61[j])
		d := float64(goPcm61[j]) - s
		sig61 += s * s
		noise61 += d * d
	}
	snr61 := 10 * math.Log10(sig61/noise61)

	libMem61, _ := libDec1.GetPreemphState()
	goState61 := goDec1.GetCELTDecoder().PreemphState()
	t.Logf("  After packet 61 (transient): go=%.10f, lib=%.10f, err=%.10e, SNR=%.1f dB",
		goState61[0], libMem61, math.Abs(goState61[0]-float64(libMem61)), snr61)
}

// TestFloat32VsFloat64Deemphasis - Compare deemphasis with float32 state vs float64
func TestFloat32VsFloat64Deemphasis(t *testing.T) {
	// Create test signal
	samples := make([]float64, 960)
	for i := range samples {
		samples[i] = 0.1 * math.Sin(2*math.Pi*float64(i)*440/48000)
	}

	// Method 1: float64 deemphasis
	state64 := 0.0
	out64 := make([]float64, len(samples))
	coef := 0.85000610
	for i, x := range samples {
		tmp := x + state64
		state64 = coef * tmp
		out64[i] = tmp
	}

	// Method 2: float32 deemphasis (matching libopus)
	state32 := float32(0.0)
	out32 := make([]float64, len(samples))
	coef32 := float32(0.85000610)
	for i, x := range samples {
		x32 := float32(x)
		tmp := x32 + state32
		state32 = coef32 * tmp
		out32[i] = float64(tmp)
	}

	// Compare
	var maxDiff float64
	for i := range out64 {
		diff := math.Abs(out64[i] - out32[i])
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	t.Logf("After 960 samples:")
	t.Logf("  float64 state: %.15f", state64)
	t.Logf("  float32 state: %.15f", float64(state32))
	t.Logf("  State diff: %.15e", math.Abs(state64-float64(state32)))
	t.Logf("  Max output diff: %.15e", maxDiff)

	// After many frames (simulate 60 frames)
	for frame := 1; frame < 60; frame++ {
		for i, x := range samples {
			tmp64 := x + state64
			state64 = coef * tmp64
			out64[i] = tmp64

			x32 := float32(x)
			tmp32 := x32 + state32
			state32 = coef32 * tmp32
			out32[i] = float64(tmp32)
		}
	}

	var maxDiff60 float64
	for i := range out64 {
		diff := math.Abs(out64[i] - out32[i])
		if diff > maxDiff60 {
			maxDiff60 = diff
		}
	}

	t.Logf("\nAfter 60 frames of 960 samples:")
	t.Logf("  float64 state: %.15f", state64)
	t.Logf("  float32 state: %.15f", float64(state32))
	t.Logf("  State diff: %.15e", math.Abs(state64-float64(state32)))
	t.Logf("  Max output diff: %.15e", maxDiff60)
}
