//go:build cgo_libopus

package silk

import (
    "math"
    "testing"

    cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func genDiagSignal(samples int) []float32 {
    out := make([]float32, samples)
    freqs := []float64{440, 1000, 2000}
    mod := []float64{1.3, 2.7, 0.9}
    amp := 0.3
    for i := 0; i < samples; i++ {
        t := float64(i) / 48000.0
        var v float64
        for fi, f := range freqs {
            md := 0.5 + 0.5*math.Sin(2*math.Pi*mod[fi]*t)
            v += amp * md * math.Sin(2*math.Pi*f*t)
        }
        if i < 480 {
            x := float64(i) / 480.0
            v *= x * x * x
        }
        out[i] = float32(v)
    }
    return out
}

func TestTmpNoiseShapeDiag(t *testing.T) {
    e := NewEncoder(BandwidthWideband)
    e.SetComplexity(10)
    e.SetBitrate(31600)
    e.SetVBR(true)
    e.controlSNR(31600, 4)
    e.SetVADState(255, 32766, [4]int{32767, 32767, 32767, 32767})

    cfg := GetBandwidthConfig(BandwidthWideband)
    frameSamples := cfg.SubframeSamples * 4
    pcm := genDiagSignal(frameSamples)
    pcm = e.quantizePCMToInt16(pcm)
    frame := e.updateShapeBuffer(pcm, frameSamples)

    residual, _, resStart, _ := e.computePitchResidual(4)

    signalType := typeUnvoiced
    pitchLags := []int{0, 0, 0, 0}
    quantOffset := 0

    // Keep smoothing state snapshot so wrapper gets identical inputs.
    harmIn := float32(0)
    tiltIn := float32(0)
    if e.noiseShapeState != nil {
        harmIn = e.noiseShapeState.HarmShapeGainSmth
        tiltIn = e.noiseShapeState.TiltSmth
    }

    params, gainsGo, qoffGo := e.noiseShapeAnalysis(frame, residual, resStart, signalType, e.speechActivityQ8, e.lastLPCGain, pitchLags, quantOffset, 4, cfg.SubframeSamples)
    _ = gainsGo

    fsKHz := cfg.SampleRate / 1000
    laShape := e.laShape
    ltpMem := ltpMemLengthMs * fsKHz
    xLen := frameSamples + 2*laShape
    xWithLA := make([]float32, xLen)
    start := ltpMem - laShape
    for i := 0; i < xLen; i++ {
        idx := start + i
        if idx >= 0 && idx < len(e.inputBuffer) {
            xWithLA[i] = e.inputBuffer[idx] * float32(silkSampleScale)
        }
    }

    pitchRes := make([]float32, frameSamples)
    for i := 0; i < frameSamples && resStart+i < len(residual); i++ {
        pitchRes[i] = float32(residual[resStart+i])
    }

    snap, ok := cgowrap.SilkNoiseShapeAnalysisFLP(
        xWithLA,
        pitchRes,
        laShape,
        fsKHz,
        4,
        cfg.SubframeSamples,
        e.shapeWinLength,
        e.shapingLPCOrder,
        e.warpingQ16,
        e.snrDBQ7,
        e.useCBR,
        e.speechActivityQ8,
        signalType,
        qoffGo,
        e.inputQualityBandsQ15,
        pitchLags,
        e.ltpCorr,
        float32(e.lastLPCGain),
        harmIn,
        tiltIn,
    )
    if !ok {
        t.Fatalf("wrapper failed")
    }

    // Compare AR Q13 conversion.
    maxAbs := 0
    maxIdx := -1
    diffs := 0
    for i := 0; i < len(params.ARShpQ13) && i < len(snap.AR); i++ {
        lib := float64ToInt32Round(float64(snap.AR[i] * 8192.0))
        goV := int32(params.ARShpQ13[i])
        d := int(goV - int32(lib))
        if d < 0 {
            d = -d
        }
        if d != 0 {
            diffs++
            if d > maxAbs {
                maxAbs = d
                maxIdx = i
            }
        }
    }

    t.Logf("ARQ13 diff: %d/%d max=%d idx=%d", diffs, len(params.ARShpQ13), maxAbs, maxIdx)
    if maxIdx >= 0 {
        t.Logf("sample mismatch: go=%d lib=%d", params.ARShpQ13[maxIdx], float64ToInt32Round(float64(snap.AR[maxIdx]*8192.0)))
    }

    // Basic scalar checks.
    if qoffGo != snap.QuantOffsetType {
        t.Logf("quantOffset go=%d lib=%d", qoffGo, snap.QuantOffsetType)
    }
    t.Logf("lambda go=%d lib=%d", params.LambdaQ10, int(float64ToInt32Round(float64(snap.Lambda*1024.0))))
}
