//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

func frameParamsForDuration(d silk.FrameDuration) (framesPerPacket, nbSubfr int) {
	switch d {
	case silk.Frame10ms:
		return 1, 2
	case silk.Frame20ms:
		return 1, 4
	case silk.Frame40ms:
		return 2, 4
	case silk.Frame60ms:
		return 3, 4
	default:
		return 0, 0
	}
}

func TestSilkNativeCorePacket1AfterPacket0(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load enough packets")
	}

	goDec := silk.NewDecoder()
	libState := NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	for pktIdx := 0; pktIdx <= 4; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Skip("Not SILK mode")
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("invalid bandwidth")
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		framesPerPacket, nbSubfr := frameParamsForDuration(duration)
		if framesPerPacket == 0 {
			t.Fatalf("unexpected duration: %v", duration)
		}
		fsKHz := silk.GetBandwidthConfig(silkBW).SampleRate / 1000
		frameLength := nbSubfr * 5 * fsKHz

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		// Use DecodeFrameRaw to get raw core output without delay compensation
		goNative, err := goDec.DecodeFrameRaw(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("gopus DecodeFrameRaw failed: %v", err)
		}

		libNative, err := libState.DecodePacketNativeCore(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil {
			t.Fatalf("libopus native core decode failed: %v", err)
		}

		if len(goNative) != len(libNative) {
			t.Fatalf("length mismatch pkt=%d go=%d lib=%d", pktIdx, len(goNative), len(libNative))
		}

		// Compare native samples (int16)
		firstDiff := -1
		firstAnyDiff := -1
		diffCount := 0
		anyDiffCount := 0
		lastSampleDiffs := 0
		for i := 0; i < len(libNative); i++ {
			scaled := math.Round(float64(goNative[i]) * 32768.0)
			if scaled > 32767 {
				scaled = 32767
			} else if scaled < -32768 {
				scaled = -32768
			}
			goInt := int16(scaled)
			diff := int(goInt) - int(libNative[i])
			if diff != 0 {
				anyDiffCount++
				if firstAnyDiff < 0 {
					firstAnyDiff = i
				}
			}
			if diff < -1 || diff > 1 {
				if firstDiff < 0 {
					firstDiff = i
				}
				diffCount++
				if frameLength > 0 && (i+1)%frameLength == 0 {
					lastSampleDiffs++
				}
			}
		}

		if anyDiffCount > 0 {
			t.Logf("pkt=%d firstAnyDiff=%d anyDiffCount=%d firstDiff(>1)=%d diffCount(>1)=%d frameLen=%d",
				pktIdx, firstAnyDiff, anyDiffCount, firstDiff, diffCount, frameLength)
			// Log window around first any diff
			start := firstAnyDiff - 2
			if start < 0 {
				start = 0
			}
			end := firstAnyDiff + 12
			if end > len(libNative) {
				end = len(libNative)
			}
			t.Logf("Samples around first diff (sample %d):", firstAnyDiff)
			for i := start; i < end; i++ {
				scaled := math.Round(float64(goNative[i]) * 32768.0)
				if scaled > 32767 {
					scaled = 32767
				} else if scaled < -32768 {
					scaled = -32768
				}
				goInt := int16(scaled)
				diff := int(goInt) - int(libNative[i])
				marker := ""
				if diff != 0 {
					marker = " *"
				}
				t.Logf("  [%d] go=%d lib=%d diff=%d%s", i, goInt, libNative[i], diff, marker)
			}
		}
		if diffCount > 0 {
			t.Logf("pkt=%d has %d samples with |diff| > 1 (first at %d)",
				pktIdx, diffCount, firstDiff)
			// Log a small window around first diff.
			start := firstDiff - 5
			if start < 0 {
				start = 0
			}
			end := firstDiff + 6
			if end > len(libNative) {
				end = len(libNative)
			}
			for i := start; i < end; i++ {
				scaled := math.Round(float64(goNative[i]) * 32768.0)
				if scaled > 32767 {
					scaled = 32767
				} else if scaled < -32768 {
					scaled = -32768
				}
				goInt := int16(scaled)
				t.Logf("[%d] go=%d lib=%d diff=%d", i, goInt, libNative[i], int(goInt)-int(libNative[i]))
			}
			t.Fatalf("native core mismatch pkt=%d", pktIdx)
		}
	}
}

// TestSilkNativeCoreFirstMismatch scans packets for the first native-core mismatch.
func TestSilkNativeCoreFirstMismatch(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 0)
	if err != nil || len(packets) == 0 {
		t.Skip("Could not load packets")
	}

	goDec := silk.NewDecoder()
	libState := NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	for pktIdx, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		if toc.Stereo {
			// Mono-only core compare for now.
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("invalid bandwidth")
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		framesPerPacket, nbSubfr := frameParamsForDuration(duration)
		if framesPerPacket == 0 {
			t.Fatalf("unexpected duration: %v", duration)
		}
		fsKHz := silk.GetBandwidthConfig(silkBW).SampleRate / 1000

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		// Use DecodeFrameRaw to get raw core output without delay compensation
		goNative, err := goDec.DecodeFrameRaw(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("gopus DecodeFrameRaw failed: %v", err)
		}
		libNative, err := libState.DecodePacketNativeCore(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil {
			t.Fatalf("libopus native core decode failed: %v", err)
		}
		if len(goNative) != len(libNative) {
			t.Fatalf("length mismatch pkt=%d go=%d lib=%d", pktIdx, len(goNative), len(libNative))
		}

		firstDiff := -1
		for i := 0; i < len(libNative); i++ {
			scaled := math.Round(float64(goNative[i]) * 32768.0)
			if scaled > 32767 {
				scaled = 32767
			} else if scaled < -32768 {
				scaled = -32768
			}
			goInt := int16(scaled)
			diff := int(goInt) - int(libNative[i])
			if diff < -1 || diff > 1 {
				firstDiff = i
				break
			}
		}
		if firstDiff >= 0 {
			t.Logf("first native-core mismatch at pkt=%d sample=%d", pktIdx, firstDiff)
			start := firstDiff - 5
			if start < 0 {
				start = 0
			}
			end := firstDiff + 6
			if end > len(libNative) {
				end = len(libNative)
			}
			for i := start; i < end; i++ {
				scaled := math.Round(float64(goNative[i]) * 32768.0)
				if scaled > 32767 {
					scaled = 32767
				} else if scaled < -32768 {
					scaled = -32768
				}
				goInt := int16(scaled)
				t.Logf("[%d] go=%d lib=%d diff=%d", i, goInt, libNative[i], int(goInt)-int(libNative[i]))
			}
			t.Fatalf("native core mismatch at pkt=%d", pktIdx)
		}
	}
}
