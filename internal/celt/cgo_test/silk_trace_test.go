// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkFirstSampleTrace traces the first sample calculation to identify divergence.
func TestSilkFirstSampleTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 1)
	if err != nil || len(packets) < 1 {
		t.Skip("Could not load packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}

	t.Logf("Packet 0: TOC=0x%02X, Bandwidth=%d, FrameSize=%d", pkt[0], toc.Bandwidth, toc.FrameSize)

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	t.Logf("SILK Bandwidth=%d (NB=0, MB=1, WB=2), Duration=%dms", silkBW, duration)

	// Get bandwidth config
	config := silk.GetBandwidthConfig(silkBW)
	t.Logf("Native sample rate: %d Hz", config.SampleRate)

	// gopus native decode
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus native decode failed: %v", err)
	}

	// libopus decode at native rate
	libDec, err := NewLibopusDecoder(config.SampleRate, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder at %d Hz", config.SampleRate)
	}
	defer libDec.Destroy()

	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	t.Logf("gopus samples: %d, libopus samples: %d", len(goNative), libSamples)

	// Compare int16 representations
	t.Log("\nFirst 10 samples as int16 equivalents:")
	for i := 0; i < 10 && i < len(goNative) && i < libSamples; i++ {
		goInt16 := int16(goNative[i] * 32768)
		libInt16 := int16(libPcm[i] * 32768)
		diff := goInt16 - libInt16
		t.Logf("  [%d] go_i16=%6d lib_i16=%6d diff=%4d", i, goInt16, libInt16, diff)
	}

	// Check if first sample is 0 in libopus but non-zero in gopus
	if len(goNative) > 0 && libSamples > 0 {
		goFirst := goNative[0]
		libFirst := libPcm[0]
		t.Logf("\nFirst sample analysis:")
		t.Logf("  gopus float32:  %.10f", goFirst)
		t.Logf("  libopus float32: %.10f", libFirst)
		t.Logf("  gopus as int16:  %d", int16(goFirst*32768))
		t.Logf("  libopus as int16: %d", int16(libFirst*32768))
	}

	// Find first non-zero sample in each
	goFirstNonZero := -1
	libFirstNonZero := -1
	for i := 0; i < len(goNative); i++ {
		if goFirstNonZero < 0 && goNative[i] != 0 {
			goFirstNonZero = i
		}
	}
	for i := 0; i < libSamples; i++ {
		if libFirstNonZero < 0 && libPcm[i] != 0 {
			libFirstNonZero = i
		}
	}
	t.Logf("\nFirst non-zero sample: gopus=%d, libopus=%d", goFirstNonZero, libFirstNonZero)
}
