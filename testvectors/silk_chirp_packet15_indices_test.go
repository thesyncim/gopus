package testvectors

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILKChirpPacket15Indices decodes the gopus and libopus packet 15 from the
// SILK-WB-40ms-mono-32k chirp_sweep fixture through the gopus SILK decoder,
// captures the per-internal-frame indices via FrameParamsHook, and prints a
// side-by-side comparison. Packets 0-14 match byte-for-byte, so any divergence
// at packet 15 narrows directly to the first symbol(s) the encoders disagreed
// on for the same input.
//
// Run with: go test ./testvectors -run TestSILKChirpPacket15Indices -v
func TestSILKChirpPacket15Indices(t *testing.T) {
	requireTestTier(t, testTierParity)

	const targetPacket = 15
	cc := struct {
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		channels  int
		bitrate   int
	}{encoder.ModeSILK, types.BandwidthWideband, 1920, 1, 32000}

	fc, ok := findEncoderVariantsFixtureCase(cc.mode, cc.bandwidth, cc.frameSize, cc.channels, cc.bitrate, testsignal.EncoderVariantChirpSweepV1)
	if !ok {
		t.Fatalf("missing fixture for SILK-WB-40ms-mono-32k chirp_sweep_v1")
	}
	totalSamples := fc.SignalFrames * fc.FrameSize * fc.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(fc.Variant, 48000, totalSamples, fc.Channels)
	if err != nil {
		t.Fatalf("gen signal: %v", err)
	}
	libPackets, _, err := decodeEncoderVariantsFixturePackets(fc)
	if err != nil {
		t.Fatalf("decode lib packets: %v", err)
	}
	goPackets, err := encodeGopusForVariantsCase(fc, signal)
	if err != nil {
		t.Fatalf("encode gopus packets: %v", err)
	}

	if targetPacket >= len(libPackets) || targetPacket >= len(goPackets) {
		t.Fatalf("packet %d out of range (lib=%d, go=%d)", targetPacket, len(libPackets), len(goPackets))
	}

	// Dump the leading bytes of packet targetPacket (TOC + first SILK bytes) for both
	// encoders so we can confirm framing (code 0 vs code 3) and the patched
	// VAD/LBRR header byte.
	t.Logf("packet %d: lib leading bytes = %s", targetPacket, hexFirstN(libPackets[targetPacket], 8))
	t.Logf("packet %d: go  leading bytes = %s", targetPacket, hexFirstN(goPackets[targetPacket], 8))
	t.Logf("packet %d: lib TOC=0x%02x (code=%d) go TOC=0x%02x (code=%d)",
		targetPacket,
		libPackets[targetPacket][0], libPackets[targetPacket][0]&0x03,
		goPackets[targetPacket][0], goPackets[targetPacket][0]&0x03,
	)
	libSilk := stripOpusFraming(libPackets[targetPacket])
	goSilk := stripOpusFraming(goPackets[targetPacket])
	t.Logf("packet %d: lib SILK payload = %d bytes; go SILK payload = %d bytes",
		targetPacket, len(libSilk), len(goSilk))

	// Sanity: confirm packets 0..targetPacket-1 match byte-for-byte.
	for i := 0; i < targetPacket; i++ {
		if !bytesEq(libPackets[i], goPackets[i]) {
			t.Fatalf("expected packets 0..%d to match; mismatch at %d", targetPacket-1, i)
		}
	}

	libDec := silk.NewDecoder()
	goDec := silk.NewDecoder()

	// Replay all packets up to targetPacket through each decoder so the SILK
	// decoder state at the start of packet targetPacket matches what the live
	// encoders saw.
	for i := 0; i <= targetPacket; i++ {
		var capturedLib, capturedGo []silk.DebugFrameParams
		libDec.SetFrameParamsHook(func(channel, frame int, p silk.DebugFrameParams) {
			if i == targetPacket {
				capturedLib = append(capturedLib, p)
			}
		})
		goDec.SetFrameParamsHook(func(channel, frame int, p silk.DebugFrameParams) {
			if i == targetPacket {
				capturedGo = append(capturedGo, p)
			}
		})

		libBytes := libPackets[i]
		goBytes := goPackets[i]
		// Strip TOC + any code-3 framing bytes so the SILK payload is left.
		libPayload := stripOpusFraming(libBytes)
		goPayload := stripOpusFraming(goBytes)

		var rdLib, rdGo rangecoding.Decoder
		rdLib.Init(libPayload)
		rdGo.Init(goPayload)
		if _, err := libDec.DecodeFrame(&rdLib, silk.BandwidthWideband, silk.Frame40ms, true); err != nil {
			t.Fatalf("lib decode packet %d: %v", i, err)
		}
		if _, err := goDec.DecodeFrame(&rdGo, silk.BandwidthWideband, silk.Frame40ms, true); err != nil {
			t.Fatalf("go decode packet %d: %v", i, err)
		}

		if i != targetPacket {
			continue
		}

		if len(capturedLib) != 2 || len(capturedGo) != 2 {
			t.Fatalf("expected 2 internal frames captured (40ms WB); lib=%d go=%d",
				len(capturedLib), len(capturedGo))
		}

		t.Logf("=== packet %d (lib len=%d go len=%d) ===", i, len(libBytes), len(goBytes))
		for k := 0; k < 2; k++ {
			lp := capturedLib[k]
			gp := capturedGo[k]
			t.Logf("--- internal frame %d ---", k)
			compareIdx(t, "SignalType", lp.SignalType, gp.SignalType)
			compareIdx(t, "QuantOffset", lp.QuantOffset, gp.QuantOffset)
			compareIdx(t, "NLSFInterpCoefQ2", lp.NLSFInterpCoefQ2, gp.NLSFInterpCoefQ2)
			compareIdx(t, "LTPScaleIndex", lp.LTPScaleIndex, gp.LTPScaleIndex)
			compareIdx(t, "PERIndex", lp.PERIndex, gp.PERIndex)
			compareIdx(t, "LagIndex", lp.LagIndex, gp.LagIndex)
			compareIdx(t, "ContourIndex", lp.ContourIndex, gp.ContourIndex)
			compareIdx(t, "Seed", lp.Seed, gp.Seed)
			compareIdxSlice(t, "GainIndices", lp.GainIndices, gp.GainIndices)
			compareIdxSlice(t, "LTPIndices", lp.LTPIndices, gp.LTPIndices)
			compareIdxSlice(t, "NLSFIndices", lp.NLSFIndices, gp.NLSFIndices)
		}
	}
}

func compareIdx(t *testing.T, name string, lib, go_ int) {
	t.Helper()
	marker := ""
	if lib != go_ {
		marker = " ***"
	}
	t.Logf("  %-18s lib=%-4d go=%-4d%s", name, lib, go_, marker)
}

func compareIdxSlice(t *testing.T, name string, lib, go_ []int) {
	t.Helper()
	marker := ""
	if !intSliceEq(lib, go_) {
		marker = " ***"
	}
	t.Logf("  %-18s lib=%v go=%v%s", name, lib, go_, marker)
}

func intSliceEq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ = fmt.Sprintf

// stripOpusFraming returns just the SILK frame payload from an Opus packet,
// stripping the TOC byte and (for code 3) the count byte plus any padding.
// This handles the only forms our SILK encoder emits: code 0 and code 3 with
// a single frame.
func stripOpusFraming(pkt []byte) []byte {
	if len(pkt) == 0 {
		return pkt
	}
	toc := pkt[0]
	code := toc & 0x03
	switch code {
	case 0:
		return pkt[1:]
	case 3:
		if len(pkt) < 2 {
			return pkt[1:]
		}
		fc := pkt[1]
		// Bits: vbr(1) | padding(1) | frame_count(6).
		hasPadding := fc&0x40 != 0
		hasVBR := fc&0x80 != 0
		_ = hasVBR
		offset := 2
		padBytes := 0
		if hasPadding {
			// Padding length encoding: 0..254 → that many bytes; 255 → 254 + next byte (recursive).
			for offset < len(pkt) {
				b := pkt[offset]
				offset++
				if b < 255 {
					padBytes += int(b)
					break
				}
				padBytes += 254
			}
		}
		end := len(pkt) - padBytes
		if end < offset {
			end = offset
		}
		return pkt[offset:end]
	}
	return pkt
}
