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
	"github.com/thesyncim/gopus/silk"
)

func loadPacketsForPostproc(t *testing.T, bitFile string, maxPackets int) [][]byte {
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

// TestSilkPostprocMatchesLibopusVector02 compares post-processing (sMid + resampler)
// against libopus for SILK vector 02 using libopus native core output as input.
func TestSilkPostprocMatchesLibopusVector02(t *testing.T) {
	bitFile := filepath.Join("..", "testvectors", "testdata", "opus_testvectors", "testvector02.bit")
	packets := loadPacketsForPostproc(t, bitFile, 50)
	if len(packets) == 0 {
		t.Skip("no packets")
	}

	libState := cgowrap.NewSilkDecoderState()
	if libState == nil {
		t.Skip("libopus SILK state unavailable")
	}
	defer libState.Free()

	var libPost *cgowrap.SilkPostprocState
	defer func() {
		if libPost != nil {
			libPost.Free()
		}
	}()

	goDec := silk.NewDecoder()
	var prevBW silk.Bandwidth = 255

	var maxDiff int
	var sumSq float64
	var sampleCount int

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
		frameLen := nbSubfr * 5 * fsKHz

		if prevBW != silkBW {
			goDec.NotifyBandwidthChange(silkBW)
			if libPost == nil {
				libPost = cgowrap.NewSilkPostprocState(fsKHz, 48000)
			} else {
				libPost.ResetResampler(fsKHz, 48000)
			}
			prevBW = silkBW
		}

		native, err := libState.DecodePacketNativeCore(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
		if err != nil {
			t.Fatalf("libopus native decode failed (pkt %d): %v", pktIdx, err)
		}
		if len(native) < frameLen*framesPerPacket {
			t.Fatalf("native length too short: %d", len(native))
		}

		for f := 0; f < framesPerPacket; f++ {
			start := f * frameLen
			end := start + frameLen
			frame := native[start:end]

			libOut, n := libPost.ProcessFrame(frame)
			if n <= 0 || len(libOut) == 0 {
				t.Fatalf("libopus postproc failed")
			}

			frameF32 := make([]float32, len(frame))
			for i, v := range frame {
				frameF32[i] = float32(v) / 32768.0
			}
			in := goDec.BuildMonoResamplerInput(frameF32)
			goOutF32 := goDec.GetResampler(silkBW).Process(in)
			goOut := make([]int16, len(goOutF32))
			for i, v := range goOutF32 {
				goOut[i] = float32ToInt16Local(v)
			}

			if len(goOut) != len(libOut) {
				t.Fatalf("output length mismatch: go=%d lib=%d", len(goOut), len(libOut))
			}

			for i := 0; i < len(goOut); i++ {
				diff := int(goOut[i]) - int(libOut[i])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
				}
				sumSq += float64(diff * diff)
			}
			sampleCount += len(goOut)
		}
	}

	rms := 0.0
	if sampleCount > 0 {
		rms = math.Sqrt(sumSq / float64(sampleCount))
	}
	t.Logf("postproc diff: max=%d rms=%.6f (samples=%d)", maxDiff, rms, sampleCount)

	if maxDiff > 1 {
		t.Fatalf("postproc mismatch: max diff %d", maxDiff)
	}
}

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
