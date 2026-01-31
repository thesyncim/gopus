// Package testvectors provides detailed debugging for packet 31 from testvector07.
// This test analyzes the divergence between gopus and libopus decoding.
package testvectors

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Packet31DebugTrace holds traced values from the decode pipeline
type Packet31DebugTrace struct {
	// Frame header
	FrameSize   int
	Channels    int
	LM          int
	Intra       bool
	Transient   bool
	ShortBlocks int

	// Postfilter
	PostfilterPeriod int
	PostfilterGain   float64
	PostfilterTapset int

	// Energy values
	CoarseEnergies []float64
	FineEnergies   []float64
	FinalEnergies  []float64

	// TF decode
	TFRes []int

	// Allocation
	Pulses       []int
	FineQuant    []int
	FinePriority []int

	// PVQ coefficients per band
	PVQCoeffs [][]float64

	// Frequency domain coefficients
	FreqDomainL []float64
	FreqDomainR []float64

	// Post-IMDCT time domain
	PostIMDCT []float64

	// Post-overlap time domain
	PostOverlap []float64

	// Final PCM output
	FinalPCM []float64
}

// loadTestPackets loads packets from a testvector .bit file
func loadTestPackets(t *testing.T, name string, maxPackets int) [][]byte {
	t.Helper()
	bitFile := filepath.Join(testVectorDir, name+".bit")

	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return nil
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:]) // finalRange
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets
}

// TestPacket31GopusDetailedAnalysis provides detailed gopus-only analysis for packet 31
func TestPacket31GopusDetailedAnalysis(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	if targetPacket >= len(packets) {
		t.Fatalf("Packet %d not available (only %d packets)", targetPacket, len(packets))
	}

	// Create decoder and process packets up to target
	dec, _ := gopus.NewDecoderDefault(48000, channels)

	// Process packets before target
	for i := 0; i < targetPacket; i++ {
		decodeFloat32(dec, packets[i])
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("=== Packet %d Analysis ===", targetPacket)
	t.Logf("Packet length: %d bytes", len(pkt))
	t.Logf("TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)
	t.Logf("First 16 bytes (hex): %x", pkt[:minInt16(16, len(pkt))])

	// Decode packet 31
	pcm, err := decodeFloat32(dec, pkt)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	t.Logf("Output samples: %d (stereo pairs: %d)", len(pcm), len(pcm)/channels)

	// Log first few output samples
	t.Logf("\n--- First 30 output samples (interleaved L/R) ---")
	t.Logf("Idx\tL\t\tR")
	for i := 0; i < minInt16(30, len(pcm)/2); i++ {
		t.Logf("%d\t%.6f\t%.6f", i, pcm[i*2], pcm[i*2+1])
	}

	// Check if L and R are identical (mono duplicated to stereo)
	identicalCount := 0
	for i := 0; i < len(pcm)/2; i++ {
		if pcm[i*2] == pcm[i*2+1] {
			identicalCount++
		}
	}
	t.Logf("\nL==R samples: %d/%d (%.1f%%)", identicalCount, len(pcm)/2, 100.0*float64(identicalCount)/float64(len(pcm)/2))
}

// TestPacket31BitExactAnalysis performs bit-level frame header analysis
func TestPacket31BitExactAnalysis(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	if targetPacket >= len(packets) {
		t.Fatalf("Packet %d not available", targetPacket)
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("=== Packet %d Bit-Level Analysis ===", targetPacket)
	t.Logf("TOC byte: 0x%02x", pkt[0])
	t.Logf("  Config: %d", pkt[0]>>3)
	t.Logf("  Stereo: %v", (pkt[0]&0x04) != 0)
	t.Logf("  Frame code: %d", pkt[0]&0x03)
	t.Logf("Decoded TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)

	// Extract CELT frame data (skip TOC byte)
	frameData := pkt[1:]

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(frameData)

	t.Logf("\n--- Range Decoder Initial State ---")
	t.Logf("Range: %d (0x%08x)", rd.Range(), rd.Range())
	t.Logf("Val: %d (0x%08x)", rd.Val(), rd.Val())
	t.Logf("Storage bits: %d", rd.StorageBits())

	// Get mode config
	mode := celt.GetModeConfig(toc.FrameSize)
	lm := mode.LM
	end := celt.EffectiveBandsForFrameSize(celt.CELTFullband, toc.FrameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}

	t.Logf("\n--- Frame Mode Configuration ---")
	t.Logf("LM (log2 of frame multiplier): %d", lm)
	t.Logf("Effective bands: %d", end)
	t.Logf("Short blocks: %d", mode.ShortBlocks)

	// Decode header flags
	totalBits := len(frameData) * 8
	tell := rd.Tell()

	t.Logf("\n--- Header Decoding ---")
	t.Logf("Total bits: %d, tell: %d", totalBits, tell)

	// Check silence flag
	silence := false
	if tell >= totalBits {
		silence = true
		t.Logf("Silence (insufficient bits)")
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
		t.Logf("Silence flag: %v (tell after: %d)", silence, rd.Tell())
	}

	if silence {
		t.Logf("*** Silence frame - no further decoding ***")
		return
	}

	// Postfilter (start=0 for this frame)
	start := 0
	postfilterPeriod := 0
	postfilterGain := 0.0
	postfilterTapset := 0

	tell = rd.Tell()
	if start == 0 && tell+16 <= totalBits {
		hasPostfilter := rd.DecodeBit(1) == 1
		t.Logf("Has postfilter: %v (tell after: %d)", hasPostfilter, rd.Tell())

		if hasPostfilter {
			octave := int(rd.DecodeUniform(6))
			t.Logf("  Octave: %d", octave)
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			t.Logf("  Period: %d, qg: %d", postfilterPeriod, qg)
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF([]byte{18, 35, 64, 64}, 2)
				t.Logf("  Tapset: %d", postfilterTapset)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
	}
	t.Logf("Postfilter: period=%d, gain=%.4f, tapset=%d", postfilterPeriod, postfilterGain, postfilterTapset)
	t.Logf("Tell after postfilter: %d", rd.Tell())

	// Transient flag
	tell = rd.Tell()
	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		t.Logf("Transient: %v (tell after: %d)", transient, rd.Tell())
	} else {
		t.Logf("Transient: skipped (lm=%d, tell=%d, totalBits=%d)", lm, tell, totalBits)
	}

	// Intra flag
	tell = rd.Tell()
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
		t.Logf("Intra: %v (tell after: %d)", intra, rd.Tell())
	} else {
		t.Logf("Intra: skipped (tell=%d, totalBits=%d)", tell, totalBits)
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("\n--- Summary ---")
	t.Logf("Frame type: %s", map[bool]string{true: "TRANSIENT", false: "NORMAL"}[transient])
	t.Logf("Energy prediction: %s", map[bool]string{true: "INTRA", false: "INTER"}[intra])
	t.Logf("Short blocks: %d", shortBlocks)
	t.Logf("Remaining bits for energy+bands: %d", totalBits-rd.Tell())
}

// TestPacket31EnergyDecode traces energy decoding
func TestPacket31EnergyDecode(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	// Create CELT decoder for detailed state inspection
	celtDec := celt.NewDecoder(channels)

	// Process packets 0..30 to build state
	for i := 0; i < targetPacket; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		frameData := pkt[1:]

		if toc.Mode != gopus.ModeCELT {
			continue
		}

		if toc.Stereo {
			celtDec.DecodeFrame(frameData, toc.FrameSize)
		} else {
			celtDec.DecodeFrameWithPacketStereo(frameData, toc.FrameSize, false)
		}
	}

	// Get state before packet 31
	t.Logf("=== State before packet %d ===", targetPacket)
	prevEnergies := celtDec.PrevEnergy()
	t.Logf("Previous energies (first 10 bands, L channel):")
	for i := 0; i < minInt16(10, len(prevEnergies)); i++ {
		t.Logf("  Band %2d: %.4f", i, prevEnergies[i])
	}
	t.Logf("Overlap buffer (first 10): %v", celtDec.OverlapBuffer()[:minInt16(10, len(celtDec.OverlapBuffer()))])
	t.Logf("Preemph state: %v", celtDec.PreemphState())
	t.Logf("RNG seed: %d", celtDec.RNG())

	// Now decode packet 31
	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("\n=== Decoding packet %d ===", targetPacket)
	t.Logf("TOC: stereo=%v, frameSize=%d", toc.Stereo, toc.FrameSize)

	frameData := pkt[1:]
	var pcm []float64
	var err error

	if toc.Stereo {
		pcm, err = celtDec.DecodeFrame(frameData, toc.FrameSize)
	} else {
		pcm, err = celtDec.DecodeFrameWithPacketStereo(frameData, toc.FrameSize, false)
	}

	if err != nil {
		t.Fatalf("CELT decode failed: %v", err)
	}

	// Get state after packet 31
	t.Logf("\n=== State after packet %d ===", targetPacket)
	newEnergies := celtDec.PrevEnergy()
	t.Logf("New energies (first 10 bands, L channel):")
	for i := 0; i < minInt16(10, len(newEnergies)); i++ {
		delta := newEnergies[i] - prevEnergies[i]
		t.Logf("  Band %2d: %.4f (delta: %+.4f)", i, newEnergies[i], delta)
	}

	t.Logf("\nFirst 20 output samples:")
	for i := 0; i < minInt16(20, len(pcm)); i++ {
		t.Logf("  [%2d] %.6f", i, pcm[i])
	}
}

// TestPacket31NeighboringPackets analyzes packets around 31 to find patterns
func TestPacket31NeighboringPackets(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	t.Logf("=== Packets 28-35 Analysis ===")
	t.Logf("Pkt\tLen\tStereo\tFrame\tMode\tFirst 8 bytes (hex)")

	for i := 28; i <= 35 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		marker := ""
		if i == 31 {
			marker = " <-- TARGET"
		}
		t.Logf("%d\t%d\t%v\t%d\t%v\t%x%s",
			i, len(pkt), toc.Stereo, toc.FrameSize, toc.Mode,
			pkt[:minInt16(8, len(pkt))], marker)
	}

	// Check for mono/stereo transitions
	t.Logf("\n--- Mono/Stereo transition check ---")
	for i := 28; i <= 35 && i < len(packets); i++ {
		toc := gopus.ParseTOC(packets[i][0])
		if i > 0 && i < len(packets) {
			prevToc := gopus.ParseTOC(packets[i-1][0])
			if toc.Stereo != prevToc.Stereo {
				t.Logf("Transition at packet %d: %v -> %v", i,
					map[bool]string{true: "stereo", false: "mono"}[prevToc.Stereo],
					map[bool]string{true: "stereo", false: "mono"}[toc.Stereo])
			}
		}
	}
}

// TestPacket31DivergenceWindow focuses on the specific divergence window (samples 64-72)
func TestPacket31DivergenceWindow(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	// Use gopus decoder
	dec, _ := gopus.NewDecoderDefault(48000, channels)

	// Process packets up to target
	for i := 0; i < targetPacket; i++ {
		decodeFloat32(dec, packets[i])
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])
	pcm, err := decodeFloat32(dec, pkt)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	t.Logf("=== Divergence Window Analysis for Packet %d ===", targetPacket)
	t.Logf("TOC: stereo=%v, frameSize=%d", toc.Stereo, toc.FrameSize)
	t.Logf("Output samples: %d stereo pairs", len(pcm)/channels)

	// Focus on samples 60-80 where divergence reportedly starts at sample 68
	t.Logf("\n--- Samples 60-80 (reported divergence starts ~68) ---")
	t.Logf("Sample\tL\t\tR\t\tL==R")

	startSample := 60
	endSample := minInt16(80, len(pcm)/2)

	for s := startSample; s < endSample; s++ {
		L := pcm[s*2]
		R := pcm[s*2+1]
		identical := "Y"
		if L != R {
			identical = "N"
		}
		marker := ""
		if s == 68 {
			marker = " <-- reported divergence"
		}
		t.Logf("%d\t%.6f\t%.6f\t%s%s", s, L, R, identical, marker)
	}

	// Analyze the overlap region (first 120 samples)
	t.Logf("\n--- Overlap region analysis (0-120) ---")
	overlapSamples := 120
	var maxAbs float64
	maxAbsIdx := 0
	for i := 0; i < minInt16(overlapSamples, len(pcm)/2); i++ {
		L := math.Abs(float64(pcm[i*2]))
		if L > maxAbs {
			maxAbs = L
			maxAbsIdx = i
		}
	}
	t.Logf("Max absolute value in overlap region: %.6f at sample %d", maxAbs, maxAbsIdx)

	// Calculate RMS in different regions
	calcRMS := func(start, end int) float64 {
		var sum float64
		count := 0
		for i := start; i < end && i*2 < len(pcm); i++ {
			sum += float64(pcm[i*2]) * float64(pcm[i*2])
			count++
		}
		if count == 0 {
			return 0
		}
		return math.Sqrt(sum / float64(count))
	}

	t.Logf("\n--- RMS by region ---")
	t.Logf("Overlap [0:120]: RMS=%.6f", calcRMS(0, 120))
	t.Logf("Post-overlap [120:240]: RMS=%.6f", calcRMS(120, 240))
	t.Logf("Middle [240:480]: RMS=%.6f", calcRMS(240, 480))
}

// TestPacket31CompareWithReference compares against the reference .dec file
func TestPacket31CompareWithReference(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	// Load reference file
	decFile := filepath.Join(testVectorDir, "testvector07.dec")
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	targetPacket := 31
	channels := 2

	// Create decoder
	dec, _ := gopus.NewDecoderDefault(48000, channels)

	// Decode all packets up to and including target, tracking sample position
	sampleOffset := 0
	for i := 0; i <= targetPacket; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		pcm, err := decodeFloat32(dec, pkt)
		if err != nil {
			t.Logf("Packet %d decode error: %v", i, err)
			continue
		}

		if i == targetPacket {
			// Compare this packet's output with reference
			t.Logf("=== Packet %d vs Reference ===", targetPacket)
			t.Logf("Sample offset in reference: %d", sampleOffset)
			t.Logf("Decoded samples: %d", len(pcm)/channels)

			// Compare sample by sample
			t.Logf("\n--- Sample comparison (samples 60-80) ---")
			t.Logf("Samp\tgopus_L\t\tref_L\t\tdiff_L")

			for s := 60; s < 80 && sampleOffset+s*2+1 < len(reference); s++ {
				refIdx := sampleOffset + s*2
				goL := pcm[s*2]
				// Reference is int16, convert to float
				refL := float64(reference[refIdx]) / 32768.0

				diff := float64(goL) - refL
				marker := ""
				if math.Abs(diff) > 0.001 {
					marker = " ***"
				}
				if s == 68 {
					marker += " <-- divergence point"
				}

				t.Logf("%d\t%.6f\t%.6f\t%.6f%s", s, goL, refL, diff, marker)
			}

			// Calculate SNR for this packet
			var sigPow, noisePow float64
			for s := 0; s < len(pcm)/channels && sampleOffset+s*2+1 < len(reference); s++ {
				goL := float64(pcm[s*2])
				refL := float64(reference[sampleOffset+s*2]) / 32768.0
				sigPow += refL * refL
				noisePow += (goL - refL) * (goL - refL)
			}
			snr := 10 * math.Log10(sigPow/noisePow)
			t.Logf("\nPacket %d SNR vs reference: %.1f dB", targetPacket, snr)

			// Find first significant difference
			const threshold = 0.001
			firstDiff := -1
			for s := 0; s < len(pcm)/channels && sampleOffset+s*2+1 < len(reference); s++ {
				goL := float64(pcm[s*2])
				refL := float64(reference[sampleOffset+s*2]) / 32768.0
				if math.Abs(goL-refL) > threshold && firstDiff == -1 {
					firstDiff = s
					break
				}
			}
			t.Logf("First difference > %.4f: sample %d", threshold, firstDiff)
		}

		sampleOffset += toc.FrameSize * channels
	}
}

// TestPacket31SurroundingPacketSNR calculates SNR for packets around 31
func TestPacket31SurroundingPacketSNR(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	packets := loadTestPackets(t, "testvector07", 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	// Load reference
	decFile := filepath.Join(testVectorDir, "testvector07.dec")
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	channels := 2
	dec, _ := gopus.NewDecoderDefault(48000, channels)

	t.Logf("=== SNR Analysis for Packets 25-40 ===")
	t.Logf("Pkt\tStereo\tFrame\tSNR(dB)\tFirstDiff\tNote")

	sampleOffset := 0
	for i := 0; i <= minInt16(40, len(packets)-1); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		pcm, err := decodeFloat32(dec, pkt)
		if err != nil {
			if i >= 25 {
				t.Logf("%d\t%v\t%d\tERROR", i, toc.Stereo, toc.FrameSize)
			}
			sampleOffset += toc.FrameSize * channels
			continue
		}

		if i >= 25 {
			// Calculate SNR against reference
			var sigPow, noisePow float64
			firstDiff := -1
			const threshold = 0.001

			for s := 0; s < len(pcm)/channels && sampleOffset+s*2+1 < len(reference); s++ {
				goL := float64(pcm[s*2])
				refL := float64(reference[sampleOffset+s*2]) / 32768.0
				sigPow += refL * refL
				noise := goL - refL
				noisePow += noise * noise
				if math.Abs(noise) > threshold && firstDiff == -1 {
					firstDiff = s
				}
			}

			snr := 10 * math.Log10(sigPow/noisePow)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999
			}

			note := ""
			if snr < 10 {
				note = "*** VERY LOW ***"
			} else if snr < 40 {
				note = "* LOW *"
			}
			if i == 31 {
				note += " <-- TARGET"
			}

			stereoStr := map[bool]string{true: "Y", false: "N"}[toc.Stereo]
			t.Logf("%d\t%s\t%d\t%.1f\t%d\t\t%s",
				i, stereoStr, toc.FrameSize, snr, firstDiff, note)
		}

		sampleOffset += toc.FrameSize * channels
	}
}

func minInt16(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt16(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// formatHexBytes formats bytes as hex string
func formatHexBytes(data []byte, max int) string {
	if len(data) > max {
		data = data[:max]
	}
	return fmt.Sprintf("%x", data)
}
