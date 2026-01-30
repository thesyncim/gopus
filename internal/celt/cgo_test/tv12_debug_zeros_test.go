// Package cgo debugs the zero-output issue in SILK decoder.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12DebugZeros traces why stateful SILK decoder produces zeros at 48kHz.
func TestTV12DebugZeros(t *testing.T) {
	testDebugZeros(t)
}

// TestTV12DebugResamplerStep tests the resampler step by step
func TestTV12DebugResamplerStep(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Debug: Tracing resampler step ===")

	silkDec := silk.NewDecoder()
	pkt826 := packets[826]
	toc := gopus.ParseTOC(pkt826[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	// Decode at native rate
	var rd rangecoding.Decoder
	rd.Init(pkt826[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("Native samples: %d", len(nativeSamples))
	t.Logf("First 5 native: %v", nativeSamples[:5])

	// Get resampler
	resampler := silkDec.GetResampler(silkBW)

	// Build resampler input manually
	resamplerInput := silkDec.BuildMonoResamplerInput(nativeSamples)
	t.Logf("Resampler input: %d samples", len(resamplerInput))
	t.Logf("First 5 resampler input: %v", resamplerInput[:5])

	// Process through resampler
	output := resampler.Process(resamplerInput)
	t.Logf("Resampler output: %d samples", len(output))
	if len(output) >= 20 {
		t.Logf("First 20 output: %v", output[:20])
		// Find first non-zero
		for i, s := range output {
			if s != 0 {
				t.Logf("First non-zero at index %d: %v", i, s)
				break
			}
		}
	} else if len(output) > 0 {
		t.Logf("First 5 output: %v", output[:5])
	} else {
		t.Log("Output is EMPTY!")
	}

	// Check non-zeros
	nonZeroInput := 0
	for _, s := range resamplerInput {
		if s != 0 {
			nonZeroInput++
		}
	}
	nonZeroOutput := 0
	for _, s := range output {
		if s != 0 {
			nonZeroOutput++
		}
	}
	t.Logf("Non-zero input: %d/%d, Non-zero output: %d/%d",
		nonZeroInput, len(resamplerInput), nonZeroOutput, len(output))
}

// TestTV12DebugCELTSilence tests CELT silence decode
func TestTV12DebugCELTSilence(t *testing.T) {
	t.Log("=== Debug: CELT silence decode ===")

	// Create CELT decoder like Opus decoder does
	celtDec := celt.NewDecoder(1)

	// Decode silence like in decodeOpusFrame lines 362-371
	silence := []byte{0xFF, 0xFF}
	F2_5 := 48000 / 50 / 4 // 240 samples (2.5ms at 48kHz)

	samples, err := celtDec.DecodeFrameWithPacketStereo(silence, F2_5, false)
	if err != nil {
		t.Fatalf("CELT decode error: %v", err)
	}
	t.Logf("CELT silence output: len=%d", len(samples))
	t.Logf("First 10 CELT silence: %v", samples[:10])

	// Find first non-zero
	for i, s := range samples {
		if s != 0 {
			t.Logf("First non-zero at index %d: %v", i, s)
			break
		}
	}
}

// TestTV12DebugDecodePath compares the exact decode paths
func TestTV12DebugDecodePath(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Log("=== Debug: Compare decode paths ===")
	t.Logf("toc.FrameSize=%d, silkBW=%v", toc.FrameSize, silkBW)

	// Path 1: Use silk.Decode (takes raw bytes, creates own range decoder)
	silkDec1 := silk.NewDecoder()
	out1, err := silkDec1.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	t.Logf("silk.Decode: len=%d, first 10: %v", len(out1), out1[:10])

	// Path 2: Use silk.DecodeWithDecoder (takes range decoder)
	silkDec2 := silk.NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	out2, err := silkDec2.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("DecodeWithDecoder error: %v", err)
	}
	t.Logf("silk.DecodeWithDecoder: len=%d, first 10: %v", len(out2), out2[:10])

	// Path 3: Via gopus.Decoder.DecodeFloat32
	opusDec, _ := gopus.NewDecoder(48000, 1)
	out3, err := opusDec.DecodeFloat32(pkt)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}
	t.Logf("gopus.DecodeFloat32: len=%d, first 10: %v", len(out3), out3[:10])

	// Check first non-zero
	findFirstNonZero := func(samples []float32) int {
		for i, s := range samples {
			if s != 0 {
				return i
			}
		}
		return -1
	}
	t.Logf("\nFirst non-zero: Decode=%d, DecodeWithDecoder=%d, gopus=%d",
		findFirstNonZero(out1), findFirstNonZero(out2), findFirstNonZero(out3))
}

// TestTV12DebugPacketParsing tests packet parsing for 826
func TestTV12DebugPacketParsing(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	pkt := packets[826]
	t.Logf("Packet 826: len=%d", len(pkt))
	t.Logf("First 10 bytes: %v", pkt[:10])

	toc := gopus.ParseTOC(pkt[0])
	t.Logf("TOC: Mode=%v, Bandwidth=%v, FrameSize=%d, Stereo=%v",
		toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo)

	pktInfo, err := gopus.ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	t.Logf("PacketInfo: FrameCount=%d, FrameSizes=%v, Padding=%d",
		pktInfo.FrameCount, pktInfo.FrameSizes, pktInfo.Padding)

	// Simulate extractFrameData
	var totalFrameBytes int
	for _, size := range pktInfo.FrameSizes {
		totalFrameBytes += size
	}
	frameDataStart := len(pkt) - pktInfo.Padding - totalFrameBytes
	t.Logf("Calculated frameDataStart=%d, totalFrameBytes=%d", frameDataStart, totalFrameBytes)

	// The frame data
	if frameDataStart >= 0 && frameDataStart < len(pkt) {
		frameData := pkt[frameDataStart : frameDataStart+pktInfo.FrameSizes[0]]
		t.Logf("Frame data: len=%d, first bytes: %v", len(frameData), frameData[:10])

		// Compare with pkt[1:]
		pktSkipTOC := pkt[1:]
		t.Logf("pkt[1:]: len=%d, first bytes: %v", len(pktSkipTOC), pktSkipTOC[:10])

		if frameDataStart == 1 {
			t.Log("frameDataStart == 1, so frameData == pkt[1:]")
		} else {
			t.Logf("DIFFERENT! frameDataStart=%d != 1", frameDataStart)
		}
	}
}

// TestTV12DebugOpusVsSilk tests Opus decoder vs direct SILK decoder
func TestTV12DebugOpusVsSilk(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Debug: Opus decoder vs direct SILK decoder for fresh packet 826 ===")

	// Opus decoder (fresh)
	opusDec, _ := gopus.NewDecoder(48000, 1)
	opusOut, err := opusDec.DecodeFloat32(packets[826])
	if err != nil {
		t.Fatalf("Opus decode error: %v", err)
	}
	t.Logf("Opus output: %d samples", len(opusOut))
	t.Logf("First 10 Opus: %v", opusOut[:10])

	// SILK decoder (fresh)
	silkDec := silk.NewDecoder()
	toc := gopus.ParseTOC(packets[826][0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	var rd rangecoding.Decoder
	rd.Init(packets[826][1:])
	silkOut, err := silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("SILK decode error: %v", err)
	}
	t.Logf("SILK output: %d samples", len(silkOut))
	t.Logf("First 10 SILK: %v", silkOut[:10])

	// libopus (fresh)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()
	libOut, libSamples := libDec.DecodeFloat(packets[826], 960*2)
	t.Logf("libopus output: %d samples", libSamples)
	t.Logf("First 10 libopus: %v", libOut[:10])

	// Find first non-zero for each
	findFirstNonZero := func(samples []float32) int {
		for i, s := range samples {
			if s != 0 {
				return i
			}
		}
		return -1
	}

	t.Logf("\nFirst non-zero index: Opus=%d, SILK=%d, libopus=%d",
		findFirstNonZero(opusOut), findFirstNonZero(silkOut), findFirstNonZero(libOut))
}

// TestTV12DebugLoopParams tests the loop parameters in DecodeWithDecoder
func TestTV12DebugLoopParams(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Debug: Loop parameters ===")

	silkDec := silk.NewDecoder()
	pkt826 := packets[826]
	toc := gopus.ParseTOC(pkt826[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	t.Logf("Packet 826: toc.FrameSize=%d, silkBW=%v", toc.FrameSize, silkBW)

	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	t.Logf("Duration from TOC: %v", duration)

	config := silk.GetBandwidthConfig(silkBW)
	t.Logf("Config: SampleRate=%d, SubframeSamples=%d", config.SampleRate, config.SubframeSamples)

	// Simulate the loop calculation
	framesPerPacket := 1  // for 20ms
	nbSubfr := 4          // maxNbSubfr for 20ms
	subFrameLengthMs := 5 // SILK subframe is 5ms
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz

	t.Logf("Calculated: framesPerPacket=%d, nbSubfr=%d, fsKHz=%d, frameLength=%d",
		framesPerPacket, nbSubfr, fsKHz, frameLength)

	// Now decode and check
	var rd rangecoding.Decoder
	rd.Init(pkt826[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("Actual nativeSamples length: %d", len(nativeSamples))

	// Check if frameLength matches
	if frameLength != len(nativeSamples) {
		t.Logf("MISMATCH: calculated frameLength=%d != nativeSamples len=%d", frameLength, len(nativeSamples))
		// Use actual length
		frameLength = len(nativeSamples) / framesPerPacket
		t.Logf("Adjusted frameLength: %d", frameLength)
	}

	// Now process loop
	t.Log("\nLoop iteration:")
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		t.Logf("  f=%d: start=%d, end=%d, len(native)=%d", f, start, end, len(nativeSamples))
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			t.Log("  BREAK condition met!")
			break
		}
		frame := nativeSamples[start:end]
		t.Logf("  Frame slice: len=%d, first 3: %v", len(frame), frame[:3])
	}
}

func testDebugZeros(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()

	t.Log("=== Debug: Tracing zero-output issue ===")

	// Build state by decoding 0-825
	for i := 0; i < 826; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
	}

	// Now decode packet 826 and trace each step
	pkt826 := packets[826]
	toc := gopus.ParseTOC(pkt826[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	t.Logf("Packet 826: TOC bandwidth=%d, SILK BW=%v", toc.Bandwidth, silkBW)

	// Step 1: Decode at native rate using DecodeFrame
	var rd rangecoding.Decoder
	rd.Init(pkt826[1:])
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("Step 1: DecodeFrame returned %d samples", len(nativeSamples))
	t.Log("  First 10 native samples:")
	for i := 0; i < 10 && i < len(nativeSamples); i++ {
		t.Logf("    [%2d] %+9.6f", i, nativeSamples[i])
	}

	// Check if samples are zero
	nonZeroCount := 0
	for _, s := range nativeSamples {
		if s != 0 {
			nonZeroCount++
		}
	}
	t.Logf("  Non-zero samples: %d/%d", nonZeroCount, len(nativeSamples))

	// Step 2: Now try full DecodeWithDecoder on a FRESH decoder
	t.Log("\nStep 2: Fresh DecodeWithDecoder")
	freshDec := silk.NewDecoder()
	var rd2 rangecoding.Decoder
	rd2.Init(pkt826[1:])
	freshOut, err := freshDec.DecodeWithDecoder(&rd2, silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Fresh DecodeWithDecoder error: %v", err)
	}
	t.Logf("  Fresh output: %d samples", len(freshOut))
	t.Log("  First 10 samples:")
	for i := 0; i < 10 && i < len(freshOut); i++ {
		t.Logf("    [%2d] %+9.6f", i, freshOut[i])
	}

	// Step 3: Test DecodeWithDecoder on stateful decoder with fresh packet
	t.Log("\nStep 3: Stateful decoder, fresh packet data (re-initialize rd)")
	silkDec2 := silk.NewDecoder()
	// Build same state
	for i := 0; i < 826; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW2, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		silkDec2.DecodeWithDecoder(&rd, silkBW2, toc.FrameSize, true)
	}

	// Decode 826 with full DecodeWithDecoder
	var rd3 rangecoding.Decoder
	rd3.Init(pkt826[1:])
	statefulOut, err := silkDec2.DecodeWithDecoder(&rd3, silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Stateful DecodeWithDecoder error: %v", err)
	}
	t.Logf("  Stateful output: %d samples", len(statefulOut))
	t.Log("  First 10 samples:")
	for i := 0; i < 10 && i < len(statefulOut); i++ {
		t.Logf("    [%2d] %+9.6f", i, statefulOut[i])
	}

	// Step 4: Check native samples from stateful decoder
	t.Log("\nStep 4: Native DecodeFrame on stateful decoder")
	silkDec3 := silk.NewDecoder()
	// Build same state
	for i := 0; i < 826; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW3, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		// Use DecodeFrame only (no resampling state)
		duration3 := silk.FrameDurationFromTOC(toc.FrameSize)
		silkDec3.DecodeFrame(&rd, silkBW3, duration3, true)
	}

	var rd4 rangecoding.Decoder
	rd4.Init(pkt826[1:])
	native826, err := silkDec3.DecodeFrame(&rd4, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Native DecodeFrame error: %v", err)
	}
	t.Logf("  Native output: %d samples", len(native826))
	t.Log("  First 10 samples:")
	for i := 0; i < 10 && i < len(native826); i++ {
		t.Logf("    [%2d] %+9.6f", i, native826[i])
	}
}
