//go:build cgo_libopus

package silk_test

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

func loadPacketsNativeCore(t *testing.T, bitFile string, maxPackets int) [][]byte {
	t.Helper()
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("failed to read bitstream: %v", err)
	}
	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}
	return packets
}

func TestSilkNativeCoreMatchesLibopusVector02(t *testing.T) {
	bitFile := filepath.Join("..", "testvectors", "testdata", "opus_testvectors", "testvector02.bit")
	packets := loadPacketsNativeCore(t, bitFile, 50)
	if len(packets) == 0 {
		t.Skip("no packets")
	}

	goDec := silk.NewDecoder()
	libState := cgowrap.NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	totalDiffs := 0
	maxDiff := 0
	firstDiffPkt := -1
	firstDiffIdx := -1

	for pktIdx, pkt := range packets {
		if len(pkt) < 1 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("invalid bandwidth")
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		framesPerPacket, nbSubfr := frameParamsForDuration(duration)
		if framesPerPacket == 0 {
			t.Fatalf("frame params: %v", duration)
		}
		fsKHz := silk.GetBandwidthConfig(silkBW).SampleRate / 1000

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNativeF32, err := goDec.DecodeFrameRaw(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("gopus DecodeFrameRaw failed (pkt %d): %v", pktIdx, err)
		}
		goNative := make([]int16, len(goNativeF32))
		for i, v := range goNativeF32 {
			goNative[i] = float32ToInt16Local(v)
		}

		libNative, err := libState.DecodePacketNativeCore(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil {
			t.Fatalf("libopus native decode failed (pkt %d): %v", pktIdx, err)
		}

		if len(goNative) != len(libNative) {
			t.Fatalf("length mismatch pkt=%d go=%d lib=%d", pktIdx, len(goNative), len(libNative))
		}

		for i := range goNative {
			diff := int(goNative[i]) - int(libNative[i])
			if diff < 0 {
				diff = -diff
			}
			if diff != 0 {
				totalDiffs++
				if firstDiffPkt < 0 {
					firstDiffPkt = pktIdx
					firstDiffIdx = i
				}
				if diff > maxDiff {
					maxDiff = diff
				}
			}
		}
	}

	t.Logf("native core diffs: total=%d maxDiff=%d firstPkt=%d firstIdx=%d", totalDiffs, maxDiff, firstDiffPkt, firstDiffIdx)
	if totalDiffs > 0 {
		t.Fatalf("native core mismatch: total=%d maxDiff=%d", totalDiffs, maxDiff)
	}
}

// TestSilkNativeCoreFindFirstMismatchVector02 scans packets to find the first native core mismatch.
func TestSilkNativeCoreFindFirstMismatchVector02(t *testing.T) {
	bitFile := filepath.Join("..", "testvectors", "testdata", "opus_testvectors", "testvector02.bit")
	packets := loadPacketsNativeCore(t, bitFile, 700)
	if len(packets) == 0 {
		t.Skip("no packets")
	}

	goDec := silk.NewDecoder()
	libState := cgowrap.NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	for pktIdx, pkt := range packets {
		if len(pkt) < 1 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("invalid bandwidth")
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		framesPerPacket, nbSubfr := frameParamsForDuration(duration)
		if framesPerPacket == 0 {
			t.Fatalf("frame params: %v", duration)
		}
		fsKHz := silk.GetBandwidthConfig(silkBW).SampleRate / 1000

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNativeF32, err := goDec.DecodeFrameRaw(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("gopus DecodeFrameRaw failed (pkt %d): %v", pktIdx, err)
		}
		goNative := make([]int16, len(goNativeF32))
		for i, v := range goNativeF32 {
			goNative[i] = float32ToInt16Local(v)
		}

		libNative, err := libState.DecodePacketNativeCore(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil {
			t.Fatalf("libopus native decode failed (pkt %d): %v", pktIdx, err)
		}

		if len(goNative) != len(libNative) {
			t.Fatalf("length mismatch pkt=%d go=%d lib=%d", pktIdx, len(goNative), len(libNative))
		}

		for i := range goNative {
			if goNative[i] != libNative[i] {
				t.Fatalf("first mismatch: pkt=%d idx=%d go=%d lib=%d", pktIdx, i, goNative[i], libNative[i])
			}
		}
	}
}

func float32ToInt16Local(sample float32) int16 {
	scaled := float64(sample) * 32768.0
	if scaled > 32767.0 {
		return 32767
	}
	if scaled < -32768.0 {
		return -32768
	}
	return int16(math.RoundToEven(scaled))
}
