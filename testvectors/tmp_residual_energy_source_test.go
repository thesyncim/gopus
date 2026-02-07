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

func TestTmpResidualEnergySourceFrame40(t *testing.T) {
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

	subfr := gainSnap.NumSubframes
	subfrLen := gainSnap.SubframeSamples
	if subfr <= 0 || subfrLen <= 0 {
		t.Fatalf("missing gain trace metadata: subfr=%d subfrLen=%d", subfr, subfrLen)
	}
	if len(nlsfSnap.LTPRes) == 0 {
		t.Fatalf("missing captured LTP residual")
	}

	lpcOrder := len(nsqSnap.PredCoefQ12) / 2
	if lpcOrder <= 0 {
		t.Fatalf("missing pred coef")
	}
	a0 := make([]float32, lpcOrder)
	a1 := make([]float32, lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		a0[i] = float32(nsqSnap.PredCoefQ12[i]) / 4096.0
		a1[i] = float32(nsqSnap.PredCoefQ12[16+i]) / 4096.0
	}
	gains := make([]float32, subfr)
	goRes := make([]float32, subfr)
	for i := 0; i < subfr; i++ {
		gains[i] = gainSnap.GainsBefore[i]
		goRes[i] = gainSnap.ResNrgBefore[i]
	}

	libResFromGoInputs, ok := cgowrap.SilkResidualEnergyFLP(nlsfSnap.LTPRes, a0, a1, gains, subfrLen, subfr, lpcOrder)
	if !ok {
		t.Fatalf("lib residual energy wrapper failed")
	}

	libFull, ok := captureLibopusProcessGainsAtFrame(original, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture libopus process_gains frame %d", targetFrame)
	}
	libResFull := make([]float32, subfr)
	for i := 0; i < subfr; i++ {
		libResFull[i] = libFull.ResNrgBefore[i]
	}

	for i := 0; i < subfr; i++ {
		t.Logf(
			"sf=%d go=%.6f lib(goInputs)=%.6f lib(full)=%.6f d(go-libGo)=%.6f d(go-libFull)=%.6f",
			i,
			goRes[i],
			libResFromGoInputs[i],
			libResFull[i],
			goRes[i]-libResFromGoInputs[i],
			goRes[i]-libResFull[i],
		)
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(goRes, libResFromGoInputs); ok {
		t.Logf("first go-vs-lib(goInputs) diff idx=%d go=%.9f lib=%.9f", idx, gv, lv)
	} else {
		t.Log("go residual energies match lib wrapper on Go inputs")
	}
	if idx, gv, lv, ok := firstFloat32BitsDiff(libResFromGoInputs, libResFull); ok {
		t.Logf("first lib(goInputs)-vs-lib(full) diff idx=%d goInputs=%.9f full=%.9f", idx, gv, lv)
	} else {
		t.Log("lib wrapper on Go inputs matches lib full residual energies")
	}

	// Keep test non-failing; this is diagnostic only.
	_ = math.Abs
}
