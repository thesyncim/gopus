// Package cgo compares fresh decoder on packet 826.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Fresh826 compares fresh vs stateful decoder at packet 826.
func TestTV12Fresh826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Fresh SILK decoder - only decode packet 826
	freshDec := silk.NewDecoder()

	// Stateful SILK decoder - process all packets 0-825 first
	statefulDec := silk.NewDecoder()

	// Libopus decoders (fresh and stateful)
	libFresh, _ := NewLibopusDecoder(48000, 1)
	libStateful, _ := NewLibopusDecoder(48000, 1)
	if libFresh == nil || libStateful == nil {
		t.Skip("Could not create libopus decoders")
	}
	defer libFresh.Destroy()
	defer libStateful.Destroy()

	t.Log("=== Building stateful decoder state ===")
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// libopus stateful
		libStateful.DecodeFloat(pkt, 1920)

		// gopus SILK stateful
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				statefulDec.DecodeFrame(&rd, silkBW, duration, true)
			}
		}
	}

	// Decode packet 826 with all decoders
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("Packet 826: Mode=%v BW=%d (NB)", toc.Mode, toc.Bandwidth)

	// Fresh gopus decode
	var rdFresh rangecoding.Decoder
	rdFresh.Init(pkt[1:])
	goFresh, _ := freshDec.DecodeFrame(&rdFresh, silkBW, duration, true)

	// Stateful gopus decode
	var rdStateful rangecoding.Decoder
	rdStateful.Init(pkt[1:])
	goStateful, _ := statefulDec.DecodeFrame(&rdStateful, silkBW, duration, true)

	// Fresh libopus decode
	libFreshOut, libFreshN := libFresh.DecodeFloat(pkt, 1920)

	// Stateful libopus decode
	libStatefulOut, libStatefulN := libStateful.DecodeFloat(pkt, 1920)

	// Compare outputs
	goFreshMax := maxAbs(goFresh)
	goStatefulMax := maxAbs(goStateful)
	libFreshMax := maxAbsSlice(libFreshOut[:libFreshN])
	libStatefulMax := maxAbsSlice(libStatefulOut[:libStatefulN])

	t.Logf("\n=== Output comparison ===")
	t.Logf("Gopus FRESH (8kHz):     %d samples, max=%.6f", len(goFresh), goFreshMax)
	t.Logf("Gopus STATEFUL (8kHz):  %d samples, max=%.6f", len(goStateful), goStatefulMax)
	t.Logf("Libopus FRESH (48kHz):  %d samples, max=%.6f", libFreshN, libFreshMax)
	t.Logf("Libopus STATEFUL (48kHz): %d samples, max=%.6f", libStatefulN, libStatefulMax)

	// Show first 10 samples for each
	t.Log("\nGopus FRESH native (first 10):")
	for i := 0; i < 10 && i < len(goFresh); i++ {
		t.Logf("  [%d] %.6f", i, goFresh[i])
	}

	t.Log("\nGopus STATEFUL native (first 10):")
	for i := 0; i < 10 && i < len(goStateful); i++ {
		t.Logf("  [%d] %.6f", i, goStateful[i])
	}

	t.Log("\nLibopus FRESH 48kHz (first 10):")
	for i := 0; i < 10 && i < libFreshN; i++ {
		t.Logf("  [%d] %.6f", i, libFreshOut[i])
	}

	t.Log("\nLibopus STATEFUL 48kHz (first 10):")
	for i := 0; i < 10 && i < libStatefulN; i++ {
		t.Logf("  [%d] %.6f", i, libStatefulOut[i])
	}

	// Key finding: does fresh produce same as stateful for gopus?
	var freshStatefulDiff float32
	for i := 0; i < len(goFresh) && i < len(goStateful); i++ {
		diff := goFresh[i] - goStateful[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > freshStatefulDiff {
			freshStatefulDiff = diff
		}
	}
	t.Logf("\nMax diff between gopus fresh vs stateful: %.6f", freshStatefulDiff)
}

func maxAbs(s []float32) float32 {
	var max float32
	for _, v := range s {
		if abs := float32(math.Abs(float64(v))); abs > max {
			max = abs
		}
	}
	return max
}

func maxAbsSlice(s []float32) float32 {
	return maxAbs(s)
}
