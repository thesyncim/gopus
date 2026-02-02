package silk

import (
    "math"
    "testing"

    "github.com/thesyncim/gopus/rangecoding"
)

func TestDiagnosticVoicedSignal(t *testing.T) {
    config := GetBandwidthConfig(BandwidthWideband)
    frameSamples := config.SampleRate * 20 / 1000 // 20ms = 320 samples at 16kHz

    // Generate a 300 Hz sine wave (voiced-like signal)
    amplitude := float32(0.3)
    pcm := make([]float32, frameSamples)
    for i := range pcm {
        tm := float64(i) / float64(config.SampleRate)
        pcm[i] = amplitude * float32(math.Sin(2*math.Pi*300*tm))
    }

    // Input RMS
    var inputSumSq float64
    for _, s := range pcm {
        inputSumSq += float64(s) * float64(s)
    }
    inputRMS := math.Sqrt(inputSumSq / float64(len(pcm)))

    t.Logf("=== INPUT ===")
    t.Logf("Sample rate: %d Hz", config.SampleRate)
    t.Logf("Frame samples: %d", frameSamples)
    t.Logf("Input amplitude: %.3f", amplitude)
    t.Logf("Input RMS: %.4f", inputRMS)

    // Encode
    encoded, err := Encode(pcm, BandwidthWideband, true)
    if err != nil {
        t.Fatalf("Encode failed: %v", err)
    }

    t.Logf("\n=== ENCODED ===")
    t.Logf("Encoded bytes: %d", len(encoded))

    // Decode
    decoder := NewDecoder()
    var rd rangecoding.Decoder
    rd.Init(encoded)
    decoded, err := decoder.DecodeFrame(&rd, BandwidthWideband, Frame20ms, true)
    if err != nil {
        t.Fatalf("Decode failed: %v", err)
    }

    t.Logf("\n=== DECODER STATE ===")
    t.Logf("Decoded samples: %d", len(decoded))
    
    st := &decoder.state[0]
    t.Logf("Decoder signal type: %d (0=inactive, 1=unvoiced, 2=voiced)", st.indices.signalType)
    t.Logf("Decoder LPC order: %d", st.lpcOrder)
    t.Logf("Decoder gain indices: %v", st.indices.GainsIndices[:4])

    // Compute decoded gains
    var ctrl decoderControl
    var lastGain int8 = 10
    silkGainsDequant(&ctrl.GainsQ16, &st.indices.GainsIndices, &lastGain, false, 4)
    t.Logf("Decoded gains (linear): %.2f, %.2f, %.2f, %.2f",
        float64(ctrl.GainsQ16[0])/65536.0, float64(ctrl.GainsQ16[1])/65536.0,
        float64(ctrl.GainsQ16[2])/65536.0, float64(ctrl.GainsQ16[3])/65536.0)

    // Log output samples (every 10th)
    t.Logf("\n=== OUTPUT SAMPLES (every 10th) ===")
    for i := 0; i < len(decoded) && i < 100; i += 10 {
        t.Logf("decoded[%d] = %.4f  (input was %.4f)", i, decoded[i], pcm[i])
    }

    // Compute output RMS
    var outputSumSq float64
    for _, s := range decoded {
        outputSumSq += float64(s) * float64(s)
    }
    outputRMS := math.Sqrt(outputSumSq / float64(len(decoded)))

    // Compute correlation - use internal function from test_helpers
    skip := frameSamples / 10
    n := float64(len(pcm[skip:frameSamples-skip]))
    var sumA, sumB float64
    inSlice := pcm[skip:frameSamples-skip]
    outSlice := decoded[skip:frameSamples-skip]
    for i := range inSlice {
        sumA += float64(inSlice[i])
        sumB += float64(outSlice[i])
    }
    meanA := sumA / n
    meanB := sumB / n
    var num, denomA, denomB float64
    for i := range inSlice {
        da := float64(inSlice[i]) - meanA
        db := float64(outSlice[i]) - meanB
        num += da * db
        denomA += da * da
        denomB += db * db
    }
    denom := math.Sqrt(denomA * denomB)
    var corr float64
    if denom > 0 {
        corr = num / denom
    }

    t.Logf("\n=== COMPARISON ===")
    t.Logf("Input RMS: %.4f", inputRMS)
    t.Logf("Output RMS: %.4f", outputRMS)
    t.Logf("RMS Ratio: %.4f", outputRMS/inputRMS)
    t.Logf("Correlation: %.4f", corr)

    if outputRMS < inputRMS*0.05 {
        t.Errorf("Output is only %.1f%% of input - something is wrong", outputRMS/inputRMS*100)
    }
}
