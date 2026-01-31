// Package cgo traces where errors start in TV12
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ErrorOrigin finds where errors first start in TV12.
func TestTV12ErrorOrigin(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 48kHz
	libDec48k, _ := NewLibopusDecoder(48000, 1)
	if libDec48k == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec48k.Destroy()

	t.Log("Tracing all SILK packets to find error origin...")

	firstBadPacket := -1
	prevBW := silk.BandwidthNarrowband

	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Check for bandwidth change
		bwChanged := (i > 0) && (silkBW != prevBW)
		prevBW = silkBW

		// Decode with gopus SILK decoder
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Decode with libopus at 48kHz
		expectedSamples := (len(goNative) * 48000) / silk.GetBandwidthConfig(silkBW).SampleRate
		libPcm48k, libSamples := libDec48k.DecodeFloat(pkt, expectedSamples*2)

		// Downsample libopus output
		nativeRate := silk.GetBandwidthConfig(silkBW).SampleRate
		resampleRatio := 48000 / nativeRate

		minLen := len(goNative)
		if libSamples/resampleRatio < minLen {
			minLen = libSamples / resampleRatio
		}

		// Calculate SNR
		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			libSample := libPcm48k[j*resampleRatio]
			diff := goNative[j] - libSample
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libSample * libSample)
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Report bandwidth changes and bad packets
		if bwChanged {
			t.Logf("  Packet %3d: BW=%s->%s (TRANSITION), SNR=%.1f dB", i, prevBW, silkBW, snr)
		}

		// Report first 5 SILK packets
		if i < 5 {
			t.Logf("  Packet %3d: BW=%s, SNR=%.1f dB", i, silkBW, snr)
		}

		// Track first bad packet
		if snr < 20 && firstBadPacket < 0 {
			firstBadPacket = i
			t.Logf("  *** FIRST BAD PACKET %d: BW=%s, SNR=%.1f dB ***", i, silkBW, snr)
		}

		// Report every 100 packets
		if i%100 == 0 && i > 0 {
			t.Logf("  Packet %3d: BW=%s, SNR=%.1f dB", i, silkBW, snr)
		}

		// Stop after packet 830
		if i >= 828 {
			break
		}
	}

	if firstBadPacket >= 0 {
		t.Logf("\nFirst error appears at packet %d", firstBadPacket)
	} else {
		t.Log("\nNo errors found - all packets have SNR >= 20 dB")
	}
}
