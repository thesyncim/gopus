//go:build cgo_libopus

package testvectors

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestTmpFindLPCFrameDiag(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)
	targets := map[int]struct{}{
		28: {},
		40: {},
	}

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	nlsfTrace := &silk.NLSFTrace{CaptureLTPRes: true}
	goEnc.SetSilkTrace(&silk.EncoderTrace{NLSF: nlsfTrace})

	type snap struct {
		ltpRes      []float32
		prevNLSFQ15 []int16
		minInvGain  float64
		interpIdx   int
		nbSubfr     int
		subfrLen    int
	}
	goSnaps := map[int]snap{}

	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		if _, ok := targets[i]; ok {
			s := snap{
				ltpRes:      append([]float32(nil), nlsfTrace.LTPRes...),
				prevNLSFQ15: append([]int16(nil), nlsfTrace.PrevNLSFQ15...),
				minInvGain:  nlsfTrace.MinInvGain,
				interpIdx:   nlsfTrace.InterpIdx,
				nbSubfr:     nlsfTrace.NbSubfr,
				subfrLen:    nlsfTrace.SubfrLen,
			}
			goSnaps[i] = s
		}
	}

	for frameIdx := range targets {
		goSnap, ok := goSnaps[frameIdx]
		if !ok {
			t.Fatalf("missing go snapshot for frame %d", frameIdx)
		}
		libSnap, ok := cgowrap.CaptureOpusFindLPCAtFrame(original, sampleRate, channels, bitrate, frameSize, frameIdx)
		if !ok {
			t.Fatalf("failed to capture lib find_lpc frame %d", frameIdx)
		}

		t.Logf("frame=%d go interp=%d lib interp=%d", frameIdx, goSnap.interpIdx, libSnap.InterpQ2)
		t.Logf("frame=%d go minInvGain=%.9f lib minInvGain=%.9f abs=%.9f",
			frameIdx, goSnap.minInvGain, libSnap.MinInvGain, math.Abs(goSnap.minInvGain-float64(libSnap.MinInvGain)))
		t.Logf("frame=%d go nbSubfr=%d subfrLen=%d | lib nbSubfr=%d subfrLen=%d",
			frameIdx, goSnap.nbSubfr, goSnap.subfrLen, libSnap.NumSubframes, libSnap.SubframeLength)

		if idx, gv, lv, ok := firstFloat32BitsDiff(goSnap.ltpRes, libSnap.X); ok {
			t.Logf("frame=%d ltpRes first diff idx=%d go=%.9f lib=%.9f abs=%.9f",
				frameIdx, idx, gv, lv, float32(math.Abs(float64(gv-lv))))
		} else {
			t.Logf("frame=%d ltpRes identical", frameIdx)
		}
		diffCount := 0
		maxAbs := float32(0)
		maxIdx := -1
		n := len(goSnap.ltpRes)
		if len(libSnap.X) < n {
			n = len(libSnap.X)
		}
		for i := 0; i < n; i++ {
			if math.Float32bits(goSnap.ltpRes[i]) != math.Float32bits(libSnap.X[i]) {
				diffCount++
				d := goSnap.ltpRes[i] - libSnap.X[i]
				if d < 0 {
					d = -d
				}
				if d > maxAbs {
					maxAbs = d
					maxIdx = i
				}
			}
		}
		t.Logf("frame=%d ltpRes diffs=%d/%d maxAbs=%.9f idx=%d", frameIdx, diffCount, n, maxAbs, maxIdx)
		if idx, gv, lv, ok := firstInt16Diff(goSnap.prevNLSFQ15, libSnap.PrevNLSFQ15); ok {
			t.Logf("frame=%d prevNLSF first diff idx=%d go=%d lib=%d", frameIdx, idx, gv, lv)
		} else {
			t.Logf("frame=%d prevNLSF identical", frameIdx)
		}

		goDbg := silk.TmpLibopusFindLPCInterpDebug(
			goSnap.ltpRes,
			goSnap.nbSubfr,
			goSnap.subfrLen,
			len(goSnap.prevNLSFQ15),
			true,
			frameIdx == 0,
			goSnap.prevNLSFQ15,
			float32(goSnap.minInvGain),
		)
		libDbg := silk.TmpLibopusFindLPCInterpDebug(
			libSnap.X,
			libSnap.NumSubframes,
			libSnap.SubframeLength,
			libSnap.LPCOrder,
			libSnap.UseInterp,
			libSnap.FirstFrameAfterReset,
			libSnap.PrevNLSFQ15,
			libSnap.MinInvGain,
		)
		t.Logf("frame=%d cgo-debug on go inputs: interp=%d base=%.9f k3=%.9f k2=%.9f k1=%.9f k0=%.9f",
			frameIdx,
			goDbg.InterpQ2,
			goDbg.ResNrg,
			goDbg.ResNrgInterp[3],
			goDbg.ResNrgInterp[2],
			goDbg.ResNrgInterp[1],
			goDbg.ResNrgInterp[0],
		)
		t.Logf("frame=%d cgo-debug on lib inputs: interp=%d base=%.9f k3=%.9f k2=%.9f k1=%.9f k0=%.9f",
			frameIdx,
			libDbg.InterpQ2,
			libDbg.ResNrg,
			libDbg.ResNrgInterp[3],
			libDbg.ResNrgInterp[2],
			libDbg.ResNrgInterp[1],
			libDbg.ResNrgInterp[0],
		)
	}
}
