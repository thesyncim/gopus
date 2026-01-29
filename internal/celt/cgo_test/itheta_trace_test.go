// Package cgo traces itheta decoding to find the source of M/S split error
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTraceThetaDecode traces the theta/imid/iside values during decoding
func TestTraceThetaDecode(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
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

	// Decode packet 14 with both decoders
	pkt := packets[14]
	t.Logf("Packet 14: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	// First, let's just decode and compare outputs
	goDec, _ := gopus.NewDecoder(48000, 2)
	goSamplesF32, _ := goDec.DecodeFloat32(pkt)

	libDec, _ := NewLibopusDecoder(48000, 2)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	libDec.Destroy()

	t.Logf("Go samples: %d stereo pairs, Lib samples: %d", len(goSamplesF32)/2, libSamples)

	// Compute M/S energy distribution
	var libMEnergy, libSEnergy float64
	var goMEnergy, goSEnergy float64

	for i := 0; i < libSamples && i*2+1 < len(goSamplesF32); i++ {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := float64(goSamplesF32[i*2])
		goR := float64(goSamplesF32[i*2+1])

		libM := (libL + libR) / 2
		libS := (libL - libR) / 2
		goM := (goL + goR) / 2
		goS := (goL - goR) / 2

		libMEnergy += libM * libM
		libSEnergy += libS * libS
		goMEnergy += goM * goM
		goSEnergy += goS * goS
	}

	t.Logf("Lib M energy: %.6f, S energy: %.6f, ratio: %.4f",
		libMEnergy, libSEnergy, libMEnergy/(libSEnergy+1e-10))
	t.Logf("Go  M energy: %.6f, S energy: %.6f, ratio: %.4f",
		goMEnergy, goSEnergy, goMEnergy/(goSEnergy+1e-10))

	// The M/S energy ratio tells us if the theta angle is decoded correctly
	libTheta := math.Atan2(math.Sqrt(libSEnergy), math.Sqrt(libMEnergy)) * 180 / math.Pi
	goTheta := math.Atan2(math.Sqrt(goSEnergy), math.Sqrt(goMEnergy)) * 180 / math.Pi
	t.Logf("Lib effective theta: %.2f°, Go effective theta: %.2f°", libTheta, goTheta)
}

// TestCheckStereoMergeFormula verifies the stereoMerge formula against reference
func TestCheckStereoMergeFormula(t *testing.T) {
	// Test with known values
	// For a simple case where M and S are orthogonal unit vectors:
	n := 4
	x := []float64{1.0, 0.0, 0.0, 0.0} // "mid" direction
	y := []float64{0.0, 1.0, 0.0, 0.0} // "side" direction
	mid := 0.7071                      // cos(45°) - equal energy split

	// Compute expected lgain/rgain
	xp := 0.0
	side := 0.0
	for i := 0; i < n; i++ {
		xp += y[i] * x[i]
		side += y[i] * y[i]
	}
	xp *= mid
	mid2 := mid * mid
	el := mid2 + side - 2.0*xp
	er := mid2 + side + 2.0*xp

	t.Logf("Input: mid=%.4f, xp=%.4f, side=%.4f", mid, xp, side)
	t.Logf("Energy: el=%.4f, er=%.4f", el, er)

	lgain := 1.0 / math.Sqrt(el)
	rgain := 1.0 / math.Sqrt(er)
	t.Logf("Gains: lgain=%.4f, rgain=%.4f", lgain, rgain)

	// Simulate stereoMerge inline
	xCopy := make([]float64, n)
	yCopy := make([]float64, n)
	for i := 0; i < n; i++ {
		l := mid * x[i]
		r := y[i]
		xCopy[i] = (l - r) * lgain
		yCopy[i] = (l + r) * rgain
	}

	t.Logf("After stereoMerge: x=%v, y=%v", xCopy, yCopy)

	// Verify that x and y now represent L and R with correct energies
	lEnergy := 0.0
	rEnergy := 0.0
	for i := 0; i < n; i++ {
		lEnergy += xCopy[i] * xCopy[i]
		rEnergy += yCopy[i] * yCopy[i]
	}
	t.Logf("Output energies: L=%.4f, R=%.4f (should both be ~1.0)", lEnergy, rEnergy)
}
