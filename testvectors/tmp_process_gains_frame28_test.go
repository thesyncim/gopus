//go:build cgo_libopus

package testvectors

import (
    "testing"

    "github.com/thesyncim/gopus/encoder"
    "github.com/thesyncim/gopus/silk"
    "github.com/thesyncim/gopus/types"
)

func TestTmpProcessGainsFrame28(t *testing.T) {
    const (
        sampleRate = 48000
        channels   = 1
        frameSize  = 960
        bitrate    = 32000
        frameIdx   = 28
    )

    numFrames := sampleRate / frameSize
    totalSamples := numFrames * frameSize * channels
    original := generateEncoderTestSignal(totalSamples, channels)

    goEnc := encoder.NewEncoder(sampleRate, channels)
    goEnc.SetMode(encoder.ModeSILK)
    goEnc.SetBandwidth(types.BandwidthWideband)
    goEnc.SetBitrate(bitrate)

    gainTrace := &silk.GainLoopTrace{}
    goEnc.SetSilkTrace(&silk.EncoderTrace{GainLoop: gainTrace})

    samplesPerFrame := frameSize * channels
    for i := 0; i <= frameIdx; i++ {
        start := i * samplesPerFrame
        end := start + samplesPerFrame
        pcm := float32ToFloat64(original[start:end])
        if _, err := goEnc.Encode(pcm, frameSize); err != nil {
            t.Fatalf("gopus encode failed at frame %d: %v", i, err)
        }
    }

    lib, ok := captureLibopusProcessGainsAtFrame(original, sampleRate, channels, bitrate, frameSize, frameIdx)
    if !ok {
        t.Fatalf("failed to capture libopus process_gains at frame %d", frameIdx)
    }

    tr := *gainTrace
    n := tr.NumSubframes
    if n <= 0 {
        t.Fatalf("invalid go numSubframes: %d", n)
    }
    t.Logf("go signal=%d snrQ7=%d tilt=%d speech=%d qOffBefore=%d qOffAfter=%d predGainQ7=%d", tr.SignalType, tr.SNRDBQ7, tr.InputTiltQ15, tr.SpeechActivityQ8, tr.QuantOffsetBefore, tr.QuantOffsetAfter, tr.PredGainQ7)
    t.Logf("lib signal=%d snrQ7=%d tilt=%d speech=%d qOffBefore=%d qOffAfter=%d predGain=%.6f", lib.SignalType, lib.SNRDBQ7, lib.InputTiltQ15, lib.SpeechActivityQ8, lib.QuantOffsetBefore, lib.QuantOffsetAfter, lib.LTPPredCodGain)

    for k := 0; k < n && k < lib.NumSubframes; k++ {
        t.Logf("sf=%d gainsBefore go=%.9f lib=%.9f | resNrg go=%.9f lib=%.9f", k, tr.GainsBefore[k], lib.GainsBefore[k], tr.ResNrgBefore[k], lib.ResNrgBefore[k])
    }
}
