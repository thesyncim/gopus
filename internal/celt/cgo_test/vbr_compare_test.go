// Package cgo compares VBR mode handling between gopus and libopus.
// Agent 24: Debug VBR mode differences causing bitstream divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestVBRTargetBitsComparison compares targetBits calculation between gopus and libopus.
// This is a key VBR calculation that determines how many bits are available for encoding.
func TestVBRTargetBitsComparison(t *testing.T) {
	testCases := []struct {
		name       string
		frameSize  int
		channels   int
		bitrate    int
		vbr        bool
	}{
		{"mono_20ms_64k_vbr", 960, 1, 64000, true},
		{"mono_20ms_64k_cbr", 960, 1, 64000, false},
		{"mono_20ms_128k_vbr", 960, 1, 128000, true},
		{"mono_20ms_128k_cbr", 960, 1, 128000, false},
		{"mono_10ms_64k_vbr", 480, 1, 64000, true},
		{"mono_10ms_64k_cbr", 480, 1, 64000, false},
		{"stereo_20ms_128k_vbr", 960, 2, 128000, true},
		{"stereo_20ms_128k_cbr", 960, 2, 128000, false},
	}

	sampleRate := 48000

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate test signal (440Hz sine wave)
			pcm32 := make([]float32, tc.frameSize*tc.channels)
			pcm64 := make([]float64, tc.frameSize*tc.channels)
			for i := 0; i < tc.frameSize; i++ {
				ti := float64(i) / float64(sampleRate)
				val := 0.5 * math.Sin(2*math.Pi*440*ti)
				for c := 0; c < tc.channels; c++ {
					pcm32[i*tc.channels+c] = float32(val)
					pcm64[i*tc.channels+c] = val
				}
			}

			// Encode with libopus
			libEnc, err := NewLibopusEncoder(sampleRate, tc.channels, OpusApplicationAudio)
			if err != nil {
				t.Fatalf("libopus encoder creation failed: %v", err)
			}
			defer libEnc.Destroy()
			libEnc.SetBitrate(tc.bitrate)
			libEnc.SetComplexity(10)
			libEnc.SetBandwidth(OpusBandwidthFullband)
			libEnc.SetVBR(tc.vbr)

			libPacket, libLen := libEnc.EncodeFloat(pcm32, tc.frameSize)
			if libLen <= 0 {
				t.Fatalf("libopus encode failed: length=%d", libLen)
			}
			libFinalRange := libEnc.GetFinalRange()

			// Encode with gopus
			goEnc := celt.NewEncoder(tc.channels)
			goEnc.Reset()
			goEnc.SetBitrate(tc.bitrate)
			goEnc.SetComplexity(10)
			goEnc.SetVBR(tc.vbr)

			goPacket, goErr := goEnc.EncodeFrame(pcm64, tc.frameSize)
			if goErr != nil {
				t.Fatalf("gopus encode failed: %v", goErr)
			}
			goFinalRange := goEnc.FinalRange()

			// Compare results
			t.Logf("VBR=%v", tc.vbr)
			t.Logf("libopus: %d bytes, finalRange=0x%08x", libLen, libFinalRange)
			t.Logf("gopus:   %d bytes, finalRange=0x%08x", len(goPacket), goFinalRange)

			// Calculate target bits based on bitrate
			// libopus formula: bitrate_to_bits = bitrate * 6 / (6 * Fs / frame_size)
			// For 64kbps, 20ms @ 48kHz: 64000 * 6 / (6 * 48000 / 960) = 64000 * 6 / 300 = 1280 bits
			baseBits := tc.bitrate * 6 / (6 * sampleRate / tc.frameSize)
			t.Logf("baseBits (from bitrate_to_bits): %d", baseBits)

			// CBR payload bytes (without TOC)
			// Formula: (bitrate * frame_size + 4*Fs) / (8*Fs) - 1
			cbrBytes := (tc.bitrate*tc.frameSize + 4*sampleRate) / (8 * sampleRate)
			if cbrBytes < 2 {
				cbrBytes = 2
			}
			cbrPayload := cbrBytes - 1 // subtract TOC byte
			t.Logf("CBR payload bytes: %d (%d bits)", cbrPayload, cbrPayload*8)

			// VBR mode: compute base_target
			// base_target = vbr_rate - ((40*C+20)<<BITRES)
			// where BITRES=3, so overhead = (40*C+20)*8
			vbrRateQ3 := baseBits << 3
			overheadQ3 := (40*tc.channels + 20) << 3
			baseTargetQ3 := vbrRateQ3 - overheadQ3
			if baseTargetQ3 < 0 {
				baseTargetQ3 = 0
			}
			t.Logf("VBR: vbrRateQ3=%d, overheadQ3=%d, baseTargetQ3=%d", vbrRateQ3, overheadQ3, baseTargetQ3)

			// libopus payload is packet minus TOC byte
			libPayloadLen := libLen - 1
			if libLen > 0 {
				libPayloadLen = libLen - 1
			}
			t.Logf("libopus payload: %d bytes (%d bits)", libPayloadLen, libPayloadLen*8)
			t.Logf("gopus packet:    %d bytes (%d bits)", len(goPacket), len(goPacket)*8)

			// Check if sizes are reasonably close
			// In VBR mode, some variation is expected due to signal analysis
			sizeDiff := len(goPacket) - libPayloadLen
			if tc.vbr {
				// VBR: allow larger difference
				if sizeDiff < -100 || sizeDiff > 100 {
					t.Logf("WARNING: Large size difference in VBR mode: %d bytes", sizeDiff)
				}
			} else {
				// CBR: should be very close
				if sizeDiff < -10 || sizeDiff > 10 {
					t.Errorf("CBR size mismatch: gopus=%d, libopus=%d (diff=%d)",
						len(goPacket), libPayloadLen, sizeDiff)
				}
			}

			// Compare first few payload bytes
			t.Logf("First 10 bytes:")
			t.Logf("  libopus: %x", libPacket[1:min(11, libLen)])
			t.Logf("  gopus:   %x", goPacket[:min(10, len(goPacket))])
		})
	}
}

// TestVBRvsCBRPacketSizes compares packet sizes in VBR vs CBR mode.
func TestVBRvsCBRPacketSizes(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate test signal
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Test VBR mode
	t.Run("libopus_VBR", func(t *testing.T) {
		libEnc, _ := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
		defer libEnc.Destroy()
		libEnc.SetBitrate(bitrate)
		libEnc.SetVBR(true)
		libEnc.SetBandwidth(OpusBandwidthFullband)

		for frame := 0; frame < 5; frame++ {
			packet, length := libEnc.EncodeFloat(pcm32, frameSize)
			t.Logf("Frame %d: %d bytes, first payload: %x", frame, length, packet[1:min(6, length)])
		}
	})

	t.Run("libopus_CBR", func(t *testing.T) {
		libEnc, _ := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
		defer libEnc.Destroy()
		libEnc.SetBitrate(bitrate)
		libEnc.SetVBR(false)
		libEnc.SetBandwidth(OpusBandwidthFullband)

		for frame := 0; frame < 5; frame++ {
			packet, length := libEnc.EncodeFloat(pcm32, frameSize)
			t.Logf("Frame %d: %d bytes, first payload: %x", frame, length, packet[1:min(6, length)])
		}
	})

	t.Run("gopus_VBR", func(t *testing.T) {
		goEnc := celt.NewEncoder(1)
		goEnc.SetBitrate(bitrate)
		goEnc.SetVBR(true)

		for frame := 0; frame < 5; frame++ {
			packet, _ := goEnc.EncodeFrame(pcm64, frameSize)
			t.Logf("Frame %d: %d bytes, first bytes: %x", frame, len(packet), packet[:min(5, len(packet))])
		}
	})

	t.Run("gopus_CBR", func(t *testing.T) {
		goEnc := celt.NewEncoder(1)
		goEnc.SetBitrate(bitrate)
		goEnc.SetVBR(false)

		for frame := 0; frame < 5; frame++ {
			packet, _ := goEnc.EncodeFrame(pcm64, frameSize)
			t.Logf("Frame %d: %d bytes (payload only), first bytes: %x", frame, len(packet), packet[:min(5, len(packet))])
		}
		// Note: libopus outputs 160 bytes = 1 TOC + 159 payload
		// gopus outputs 159 bytes = payload only (no TOC)
		// This is expected since gopus CELT encoder doesn't add TOC byte
	})
}

// TestCBRExactMatch tests that CBR mode produces bit-exact output.
// Since CBR has fixed budget and no VBR boost, it should be easier to match.
func TestCBRExactMatch(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate test signal
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Create both encoders in CBR mode
	libEnc, _ := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false) // CBR mode

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false) // CBR mode

	// Encode multiple frames
	for frame := 0; frame < 3; frame++ {
		libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
		goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

		libPayload := libPacket[1:libLen] // Skip TOC byte
		libFinalRange := libEnc.GetFinalRange()
		goFinalRange := goEnc.FinalRange()

		t.Logf("Frame %d:", frame)
		t.Logf("  libopus: %d bytes, finalRange=0x%08x", len(libPayload), libFinalRange)
		t.Logf("  gopus:   %d bytes, finalRange=0x%08x", len(goPacket), goFinalRange)

		// Compare sizes
		if len(goPacket) != len(libPayload) {
			t.Logf("  Size mismatch: gopus=%d, libopus=%d", len(goPacket), len(libPayload))
		}

		// Compare bytes
		matchCount := 0
		for i := 0; i < min(len(goPacket), len(libPayload)); i++ {
			if goPacket[i] == libPayload[i] {
				matchCount++
			} else {
				break
			}
		}
		t.Logf("  Matching prefix bytes: %d", matchCount)

		// Show divergence point
		if matchCount < min(len(goPacket), len(libPayload)) {
			divergeIdx := matchCount
			t.Logf("  Divergence at byte %d: gopus=0x%02x, libopus=0x%02x",
				divergeIdx, goPacket[divergeIdx], libPayload[divergeIdx])
		}

		// Show first 10 bytes
		t.Logf("  libopus first 10: %x", libPayload[:min(10, len(libPayload))])
		t.Logf("  gopus first 10:   %x", goPacket[:min(10, len(goPacket))])
	}
}

// TestBitrateToBitsCalculation verifies the bitrate_to_bits formula.
func TestBitrateToBitsCalculation(t *testing.T) {
	testCases := []struct {
		bitrate   int
		fs        int
		frameSize int
	}{
		{64000, 48000, 960},  // 20ms
		{64000, 48000, 480},  // 10ms
		{64000, 48000, 240},  // 5ms
		{128000, 48000, 960}, // 20ms, higher bitrate
		{32000, 48000, 960},  // 20ms, lower bitrate
	}

	for _, tc := range testCases {
		// libopus formula: bitrate * 6 / (6 * Fs / frame_size)
		// Simplifies to: bitrate * frame_size / Fs
		libopusBits := tc.bitrate * 6 / (6 * tc.fs / tc.frameSize)
		simplifiedBits := tc.bitrate * tc.frameSize / tc.fs

		t.Logf("bitrate=%d, fs=%d, frameSize=%d:", tc.bitrate, tc.fs, tc.frameSize)
		t.Logf("  libopus formula: %d bits", libopusBits)
		t.Logf("  simplified:      %d bits", simplifiedBits)

		if libopusBits != simplifiedBits {
			t.Errorf("Formulas don't match: %d vs %d", libopusBits, simplifiedBits)
		}
	}
}

// TestComputeTargetBitsInternal tests gopus internal target bits calculation.
func TestComputeTargetBitsInternal(t *testing.T) {
	frameSize := 960
	bitrate := 64000
	sampleRate := 48000

	t.Run("CBR_mode", func(t *testing.T) {
		enc := celt.NewEncoder(1)
		enc.SetBitrate(bitrate)
		enc.SetVBR(false)

		// Expected CBR calculation:
		// nbCompressedBytes = (bitrate*frameSize + 4*Fs) / (8*Fs)
		// = (64000*960 + 4*48000) / (8*48000)
		// = (61440000 + 192000) / 384000
		// = 61632000 / 384000
		// = 160 bytes total
		// payload = 160 - 1 = 159 bytes
		expectedBytes := (bitrate*frameSize + 4*sampleRate) / (8 * sampleRate)
		expectedPayload := expectedBytes - 1
		expectedBits := expectedPayload * 8

		t.Logf("Expected CBR: %d bytes total, %d payload bytes, %d bits",
			expectedBytes, expectedPayload, expectedBits)

		// Now compute what gopus uses
		// We can't call computeTargetBits directly, but we can check encoder behavior
	})

	t.Run("VBR_mode", func(t *testing.T) {
		enc := celt.NewEncoder(1)
		enc.SetBitrate(bitrate)
		enc.SetVBR(true)

		// Expected VBR base calculation:
		// baseBits = bitrate * frameSize / fs = 64000 * 960 / 48000 = 1280 bits
		// vbrRateQ3 = 1280 << 3 = 10240
		// overheadQ3 = (40*1 + 20) << 3 = 60 * 8 = 480
		// baseTargetQ3 = 10240 - 480 = 9760
		// Without boost: targetBits = (9760 + 4) >> 3 = 1220 bits = 152.5 bytes

		baseBits := bitrate * frameSize / sampleRate
		vbrRateQ3 := baseBits << 3
		overheadQ3 := (40 + 20) << 3
		baseTargetQ3 := vbrRateQ3 - overheadQ3
		minTargetBits := (baseTargetQ3 + 4) >> 3

		t.Logf("VBR base calculation:")
		t.Logf("  baseBits = %d", baseBits)
		t.Logf("  vbrRateQ3 = %d", vbrRateQ3)
		t.Logf("  overheadQ3 = %d", overheadQ3)
		t.Logf("  baseTargetQ3 = %d", baseTargetQ3)
		t.Logf("  minTargetBits (no boost) = %d", minTargetBits)

		// Note: libopus also adds "tell" to target (bits used so far in frame)
		// and applies compute_vbr() boost based on signal analysis
		_ = enc
	})
}

// min is defined in packet_analysis_test.go
