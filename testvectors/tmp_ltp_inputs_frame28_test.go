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

func TestTmpLTPInputsFrame28(t *testing.T) {
	const (
		sampleRate  = 48000
		channels    = 1
		frameSize   = 960
		bitrate     = 32000
		targetFrame = 28
	)

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	framePre := &silk.FrameStateTrace{}
	pitchTrace := &silk.PitchTrace{CaptureXBuf: true}
	ltpTrace := &silk.LTPTrace{}
	gainTrace := &silk.GainLoopTrace{}
	goEnc.SetSilkTrace(&silk.EncoderTrace{
		FramePre: framePre,
		Pitch:    pitchTrace,
		LTP:      ltpTrace,
		GainLoop: gainTrace,
	})

	var framePreSnap silk.FrameStateTrace
	var pitchSnap silk.PitchTrace
	var ltpSnap silk.LTPTrace
	var gainSnap silk.GainLoopTrace

	samplesPerFrame := frameSize * channels
	for i := 0; i <= targetFrame; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		if i == targetFrame {
			framePreSnap = *framePre
			framePreSnap.PitchBuf = append([]float32(nil), framePre.PitchBuf...)
			pitchSnap = *pitchTrace
			pitchSnap.XBuf = append([]float32(nil), pitchTrace.XBuf...)
			ltpSnap = *ltpTrace
			ltpSnap.PitchLags = append([]int(nil), ltpTrace.PitchLags...)
			ltpSnap.BQ14 = append([]int16(nil), ltpTrace.BQ14...)
			gainSnap = *gainTrace
		}
	}

	lib, ok := cgowrap.CaptureOpusLTPAnalysisAtFrame(original, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture lib LTP analysis frame %d", targetFrame)
	}

	fsKHz := 16 // WB internal rate
	ltpMem := 20 * fsKHz
	preLen := lib.PreLength
	start := ltpMem - preLen
	xGo := make([]float32, len(lib.X))
	for i := 0; i < len(xGo); i++ {
		idx := start + i
		if idx >= 0 && idx < len(pitchSnap.XBuf) {
			xGo[i] = pitchSnap.XBuf[idx]
		}
	}

	bGo := make([]float32, len(lib.B))
	for i := 0; i < len(bGo) && i < len(ltpSnap.BQ14); i++ {
		bGo[i] = float32(ltpSnap.BQ14[i]) / 16384.0
	}
	pGo := make([]int, len(lib.Pitch))
	copy(pGo, ltpSnap.PitchLags)
	invGo := make([]float32, len(lib.InvGains))
	for i := 0; i < len(invGo) && i < gainSnap.NumSubframes; i++ {
		g := gainSnap.GainsBefore[i]
		if g > 0 {
			invGo[i] = 1.0 / g
		}
	}

	if idx, gv, lv, ok := firstFloat32BitsDiff(xGo, lib.X); ok {
		t.Logf("x first diff idx=%d go=%.9f lib=%.9f abs=%.9f", idx, gv, lv, float32(math.Abs(float64(gv-lv))))
	} else {
		t.Log("x: identical")
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(bGo, lib.B); ok {
		t.Logf("B first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("B: identical")
	}
	pDiff := false
	for i := 0; i < len(pGo) && i < len(lib.Pitch); i++ {
		if pGo[i] != lib.Pitch[i] {
			t.Logf("pitch first diff idx=%d go=%d lib=%d", i, pGo[i], lib.Pitch[i])
			pDiff = true
			break
		}
	}
	if !pDiff {
		t.Log("pitch: identical")
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(invGo, lib.InvGains); ok {
		t.Logf("invGains first diff idx=%d go=%.9f lib=%.9f abs=%.9f", idx, gv, lv, float32(math.Abs(float64(gv-lv))))
	} else {
		t.Log("invGains: identical")
	}
	_ = framePreSnap
	_ = ltpMem
}
