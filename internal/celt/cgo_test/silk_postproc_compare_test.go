package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func floatToInt16Slice(in []float32) []int16 {
	out := make([]int16, len(in))
	for i, v := range in {
		scaled := math.Round(float64(v) * 32768.0)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		out[i] = int16(scaled)
	}
	return out
}

func TestSilkPostprocMatchesLibopus(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil || len(packets) < 2 {
		t.Skip("Could not load enough packets")
	}

	goDec := silk.NewDecoder()
	var goResampler *silk.LibopusResampler
	var cPostproc *SilkPostprocState
	var sMidGo [2]float32
	var lastFsKHz int

	// Use packet 0 to set state, then compare packet 1
	for pktIdx := 0; pktIdx <= 1; pktIdx++ {
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
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("gopus DecodeFrame failed: %v", err)
		}

		// Initialize or reset postproc states when input rate changes.
		if goResampler == nil || fsKHz != lastFsKHz {
			goResampler = silk.NewLibopusResampler(fsKHz*1000, 48000)
			if cPostproc != nil {
				cPostproc.Free()
			}
			cPostproc = NewSilkPostprocState(fsKHz, 48000)
			if cPostproc == nil {
				t.Fatalf("failed to create libopus postproc state")
			}
			sMidGo = [2]float32{}
			lastFsKHz = fsKHz
		}

		// Process each frame within the SILK packet
		for f := 0; f < framesPerPacket; f++ {
			start := f * frameLength
			end := start + frameLength
			if end > len(goNative) {
				t.Fatalf("frame slice out of range: %d..%d of %d", start, end, len(goNative))
			}
			frame := goNative[start:end]

			// Go-side postproc (matches silk.Decode)
			resIn := make([]float32, frameLength)
			resIn[0] = sMidGo[1]
			if frameLength > 1 {
				copy(resIn[1:], frame[:frameLength-1])
				sMidGo[0] = frame[frameLength-2]
				sMidGo[1] = frame[frameLength-1]
			} else {
				sMidGo[0] = sMidGo[1]
				sMidGo[1] = frame[0]
			}
			goOut := goResampler.Process(resIn)

			// Libopus postproc reference
			frameInt16 := floatToInt16Slice(frame)
			refOut, n := cPostproc.ProcessFrame(frameInt16)
			if n <= 0 {
				t.Fatalf("libopus postproc failed")
			}

			if len(goOut) != len(refOut) {
				t.Fatalf("postproc length mismatch pkt=%d frame=%d go=%d ref=%d", pktIdx, f, len(goOut), len(refOut))
			}

			// Compare
			firstDiff := -1
			for i := 0; i < len(goOut); i++ {
				ref := float32(refOut[i]) / 32768.0
				diff := goOut[i] - ref
				if diff < -1e-6 || diff > 1e-6 {
					firstDiff = i
					break
				}
			}
			if firstDiff >= 0 {
				t.Logf("pkt=%d frame=%d firstDiff=%d go=%.8f ref=%.8f",
					pktIdx, f, firstDiff, goOut[firstDiff], float32(refOut[firstDiff])/32768.0)
				t.Fatalf("postproc mismatch pkt=%d frame=%d", pktIdx, f)
			}
		}
	}
	if cPostproc != nil {
		cPostproc.Free()
	}
}
