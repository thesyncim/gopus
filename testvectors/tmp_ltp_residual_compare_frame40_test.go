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

func TestTmpLTPResidualCompareFrame40(t *testing.T) {
	const (
		sampleRate  = 48000
		channels    = 1
		frameSize   = 960
		bitrate     = 32000
		targetFrame = 40
	)

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	nlsfTrace := &silk.NLSFTrace{CaptureLTPRes: true}
	gainTrace := &silk.GainLoopTrace{}
	nsqTrace := &silk.NSQTrace{CaptureInputs: true}
	goEnc.SetSilkTrace(&silk.EncoderTrace{
		NLSF:     nlsfTrace,
		GainLoop: gainTrace,
		NSQ:      nsqTrace,
	})

	var nlsfSnap silk.NLSFTrace
	var gainSnap silk.GainLoopTrace
	var nsqSnap silk.NSQTrace

	samplesPerFrame := frameSize * channels
	for i := 0; i <= targetFrame; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		if i == targetFrame {
			nlsfSnap = *nlsfTrace
			nlsfSnap.LTPRes = append([]float32(nil), nlsfTrace.LTPRes...)
			gainSnap = *gainTrace
			nsqSnap = cloneNSQTrace(nsqTrace)
		}
	}

	lib, ok := cgowrap.CaptureOpusResidualEnergyAtFrame(original, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture lib residual_energy frame %d", targetFrame)
	}
	libNSQ, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture lib NSQ inputs frame %d", targetFrame)
	}

	t.Logf("lib frame=%d calls=%d subfr=%d subfrLen=%d lpcOrder=%d xLen=%d",
		lib.EncodeFrame, lib.CallsInFrame, lib.NumSubframes, lib.SubframeLength, lib.LPCOrder, len(lib.X))
	t.Logf("go  subfr=%d subfrLen=%d lpcOrder=%d xLen=%d",
		gainSnap.NumSubframes, gainSnap.SubframeSamples, len(nsqSnap.PredCoefQ12)/2, len(nlsfSnap.LTPRes))

	if idx, gv, lv, ok := firstFloat32BitsDiff(nlsfSnap.LTPRes, lib.X); ok {
		t.Logf("LTP_res first diff idx=%d go=%.9f lib=%.9f abs=%.9f", idx, gv, lv, float32(math.Abs(float64(gv-lv))))
	} else {
		t.Log("LTP_res: identical")
	}

	order := len(nsqSnap.PredCoefQ12) / 2
	if order > len(lib.A0) {
		order = len(lib.A0)
	}
	goA0 := make([]float32, order)
	goA1 := make([]float32, order)
	for i := 0; i < order; i++ {
		goA0[i] = float32(nsqSnap.PredCoefQ12[i]) / 4096.0
		goA1[i] = float32(nsqSnap.PredCoefQ12[16+i]) / 4096.0
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goA0, lib.A0[:order]); ok {
		t.Logf("a0 first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("a0: identical")
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goA1, lib.A1[:order]); ok {
		t.Logf("a1 first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("a1: identical")
	}
	libNSQA0 := make([]float32, order)
	libNSQA1 := make([]float32, order)
	for i := 0; i < order; i++ {
		libNSQA0[i] = float32(libNSQ.PredCoefQ12[i]) / 4096.0
		libNSQA1[i] = float32(libNSQ.PredCoefQ12[16+i]) / 4096.0
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goA0, libNSQA0); ok {
		t.Logf("go a0-vs-lib NSQ a0 first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("go a0 matches lib NSQ a0")
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goA1, libNSQA1); ok {
		t.Logf("go a1-vs-lib NSQ a1 first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("go a1 matches lib NSQ a1")
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(libNSQA1, lib.A1[:order]); ok {
		t.Logf("lib a1(residual)-vs-lib a1(NSQ) first diff idx=%d residual=%.9f nsq=%.9f", idx, lv, gv)
	} else {
		t.Log("lib a1(residual) matches lib a1(NSQ)")
	}

	goGains := make([]float32, gainSnap.NumSubframes)
	for i := 0; i < gainSnap.NumSubframes; i++ {
		goGains[i] = gainSnap.GainsBefore[i]
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goGains, lib.Gains); ok {
		t.Logf("gains first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("gains: identical")
	}

	goRes := make([]float32, gainSnap.NumSubframes)
	for i := 0; i < gainSnap.NumSubframes; i++ {
		goRes[i] = gainSnap.ResNrgBefore[i]
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goRes, lib.ResNrg); ok {
		t.Logf("resNrg first diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("resNrg: identical")
	}
}
