package silk

import (
    "math"
    "testing"

    "github.com/thesyncim/gopus/rangecoding"
)

func TestDiagnosticRoundTrip(t *testing.T) {
    config := GetBandwidthConfig(BandwidthNarrowband)
    frameSamples := config.SampleRate * 20 / 1000 // 20ms = 160 samples at 8kHz

    // Generate a constant amplitude signal (easier to trace)
    amplitude := float32(0.5)
    pcm := make([]float32, frameSamples)
    for i := range pcm {
        pcm[i] = amplitude
    }

    t.Logf("=== INPUT ===")
    t.Logf("Sample rate: %d Hz", config.SampleRate)
    t.Logf("Frame samples: %d", frameSamples)
    t.Logf("Input amplitude: %.3f (%.0f in int16)", amplitude, amplitude*32768)

    // Encode
    encoded, err := Encode(pcm, BandwidthNarrowband, true)
    if err != nil {
        t.Fatalf("Encode failed: %v", err)
    }

    t.Logf("\n=== ENCODED ===")
    t.Logf("Encoded bytes: %d", len(encoded))
    t.Logf("First 10 bytes: %v", encoded[:10])

    // Decode
    decoder := NewDecoder()
    var rd rangecoding.Decoder
    rd.Init(encoded)
    decoded, err := decoder.DecodeFrame(&rd, BandwidthNarrowband, Frame20ms, true)
    if err != nil {
        t.Fatalf("Decode failed: %v", err)
    }

    t.Logf("\n=== DECODER STATE ===")
    t.Logf("Decoded samples: %d", len(decoded))
    
    // Get decoder state
    st := &decoder.state[0]
    t.Logf("Decoder signal type: %d", st.indices.signalType)
    t.Logf("Decoder quant offset: %d", st.indices.quantOffsetType)
    t.Logf("Decoder LPC order: %d", st.lpcOrder)
    t.Logf("Decoder gain indices: %v", st.indices.GainsIndices[:4])
    
    // Compute decoded gains
    var ctrl decoderControl
    var lastGain int8 = 10
    silkGainsDequant(&ctrl.GainsQ16, &st.indices.GainsIndices, &lastGain, false, 4)
    t.Logf("Decoded gainsQ16: %v", ctrl.GainsQ16[:4])
    t.Logf("Decoded gains (linear): %.2f, %.2f, %.2f, %.2f",
        float64(ctrl.GainsQ16[0])/65536.0, float64(ctrl.GainsQ16[1])/65536.0,
        float64(ctrl.GainsQ16[2])/65536.0, float64(ctrl.GainsQ16[3])/65536.0)

    // Log first few output samples
    t.Logf("\n=== OUTPUT SAMPLES ===")
    for i := 0; i < 20 && i < len(decoded); i++ {
        t.Logf("decoded[%d] = %.6f", i, decoded[i])
    }

    // Compute RMS
    var sumSq float64
    for _, s := range decoded {
        sumSq += float64(s) * float64(s)
    }
    rms := math.Sqrt(sumSq / float64(len(decoded)))
    
    t.Logf("\n=== COMPARISON ===")
    t.Logf("Input amplitude: %.3f", amplitude)
    t.Logf("Output RMS: %.6f", rms)
    t.Logf("Ratio: %.4f (should be ~1.0 for good recovery)", rms/float64(amplitude))
}
