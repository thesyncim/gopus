// Package cgo tests force transient to verify the root cause.
package cgo

import (
    "math"
    "testing"

    "github.com/thesyncim/gopus/internal/celt"
    "github.com/thesyncim/gopus/internal/rangecoding"
)

// TestForceTransientMatchesLibopus verifies that forcing transient=1 
// produces output closer to libopus for Frame 0.
func TestForceTransientMatchesLibopus(t *testing.T) {
    frameSize := 960
    sampleRate := 48000
    bitrate := 64000

    // Generate 440Hz sine wave
    pcm64 := make([]float64, frameSize)
    pcm32 := make([]float32, frameSize)
    for i := range pcm64 {
        ti := float64(i) / float64(sampleRate)
        val := 0.5 * math.Sin(2*math.Pi*440*ti)
        pcm64[i] = val
        pcm32[i] = float32(val)
    }

    t.Log("=== Test: Force Transient to Match Libopus ===")
    t.Log("")

    // Encode with gopus WITHOUT forcing transient
    gopusEncNormal := celt.NewEncoder(1)
    gopusEncNormal.SetBitrate(bitrate)
    gopusPacketNormal, err := gopusEncNormal.EncodeFrame(pcm64, frameSize)
    if err != nil {
        t.Fatalf("gopus encode (normal) failed: %v", err)
    }

    // Encode with gopus WITH forcing transient
    gopusEncForced := celt.NewEncoder(1)
    gopusEncForced.SetBitrate(bitrate)
    gopusEncForced.SetForceTransient(true)
    gopusPacketForced, err := gopusEncForced.EncodeFrame(pcm64, frameSize)
    if err != nil {
        t.Fatalf("gopus encode (forced transient) failed: %v", err)
    }

    // Encode with libopus
    libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
    if err != nil {
        t.Fatalf("libopus encoder creation failed: %v", err)
    }
    defer libEnc.Destroy()
    libEnc.SetBitrate(bitrate)
    libEnc.SetComplexity(10)
    libEnc.SetBandwidth(OpusBandwidthFullband)
    libEnc.SetVBR(true)

    libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
    if libLen <= 0 {
        t.Fatalf("libopus encode failed: length=%d", libLen)
    }

    // Skip TOC byte for comparison
    libPayload := libPacket[1:libLen]

    t.Logf("Gopus (normal):  %d bytes, first 8: %02x", len(gopusPacketNormal), gopusPacketNormal[:8])
    t.Logf("Gopus (forced):  %d bytes, first 8: %02x", len(gopusPacketForced), gopusPacketForced[:8])
    t.Logf("Libopus:         %d bytes, first 8: %02x", len(libPayload), libPayload[:8])
    t.Log("")

    // Decode flags from each packet
    t.Log("=== Flag Comparison ===")
    
    // Normal gopus
    rdNormal := &rangecoding.Decoder{}
    rdNormal.Init(gopusPacketNormal)
    normalSilence := rdNormal.DecodeBit(15)
    normalPostfilter := rdNormal.DecodeBit(1)
    normalTransient := rdNormal.DecodeBit(3)
    normalIntra := rdNormal.DecodeBit(3)
    t.Logf("Gopus (normal):  silence=%d postfilter=%d transient=%d intra=%d", 
        normalSilence, normalPostfilter, normalTransient, normalIntra)

    // Forced gopus
    rdForced := &rangecoding.Decoder{}
    rdForced.Init(gopusPacketForced)
    forcedSilence := rdForced.DecodeBit(15)
    forcedPostfilter := rdForced.DecodeBit(1)
    forcedTransient := rdForced.DecodeBit(3)
    forcedIntra := rdForced.DecodeBit(3)
    t.Logf("Gopus (forced):  silence=%d postfilter=%d transient=%d intra=%d", 
        forcedSilence, forcedPostfilter, forcedTransient, forcedIntra)

    // Libopus
    rdLib := &rangecoding.Decoder{}
    rdLib.Init(libPayload)
    libSilence := rdLib.DecodeBit(15)
    libPostfilter := rdLib.DecodeBit(1)
    libTransient := rdLib.DecodeBit(3)
    libIntra := rdLib.DecodeBit(3)
    t.Logf("Libopus:         silence=%d postfilter=%d transient=%d intra=%d", 
        libSilence, libPostfilter, libTransient, libIntra)

    // Count matching bytes
    t.Log("")
    t.Log("=== Byte Matching Analysis ===")
    
    normalMatches := 0
    for i := 0; i < len(gopusPacketNormal) && i < len(libPayload); i++ {
        if gopusPacketNormal[i] == libPayload[i] {
            normalMatches++
        } else {
            break
        }
    }
    
    forcedMatches := 0
    for i := 0; i < len(gopusPacketForced) && i < len(libPayload); i++ {
        if gopusPacketForced[i] == libPayload[i] {
            forcedMatches++
        } else {
            break
        }
    }

    t.Logf("Gopus (normal) first divergence:  byte %d", normalMatches)
    t.Logf("Gopus (forced) first divergence:  byte %d", forcedMatches)

    // Compare QI values
    t.Log("")
    t.Log("=== QI Comparison (first 5 bands) ===")
    
    mode := celt.GetModeConfig(frameSize)
    nbBands := mode.EffBands
    lm := mode.LM
    probModel := celt.GetEProbModel()

    // Reset decoders for QI
    rdNormal.Init(gopusPacketNormal)
    rdNormal.DecodeBit(15) // silence
    rdNormal.DecodeBit(1)  // postfilter
    rdNormal.DecodeBit(3)  // transient
    rdNormal.DecodeBit(3)  // intra
    
    rdForced.Init(gopusPacketForced)
    rdForced.DecodeBit(15)
    rdForced.DecodeBit(1)
    rdForced.DecodeBit(3)
    rdForced.DecodeBit(3)
    
    rdLib.Init(libPayload)
    rdLib.DecodeBit(15)
    rdLib.DecodeBit(1)
    rdLib.DecodeBit(3)
    rdLib.DecodeBit(3)

    gopusNormalDec := celt.NewDecoder(1)
    gopusNormalDec.SetRangeDecoder(rdNormal)
    
    gopusForcedDec := celt.NewDecoder(1)
    gopusForcedDec.SetRangeDecoder(rdForced)
    
    libDec := celt.NewDecoder(1)
    libDec.SetRangeDecoder(rdLib)

    prob := probModel[lm][0] // inter mode

    t.Log("Band | Normal | Forced | Libopus")
    t.Log("-----|--------|--------|--------")
    
    normalQIMatches := 0
    forcedQIMatches := 0
    
    for band := 0; band < 5 && band < nbBands; band++ {
        pi := 2 * band
        if pi > 40 {
            pi = 40
        }
        fs := int(prob[pi]) << 7
        decay := int(prob[pi+1]) << 6

        qiNormal := gopusNormalDec.DecodeLaplaceTest(fs, decay)
        qiForced := gopusForcedDec.DecodeLaplaceTest(fs, decay)
        qiLib := libDec.DecodeLaplaceTest(fs, decay)

        normalMatch := ""
        forcedMatch := ""
        if qiNormal == qiLib {
            normalMatch = " (match)"
            normalQIMatches++
        }
        if qiForced == qiLib {
            forcedMatch = " (match)"
            forcedQIMatches++
        }

        t.Logf("  %d  |   %3d  |   %3d  |   %3d%s%s", band, qiNormal, qiForced, qiLib, normalMatch, forcedMatch)
    }
    
    t.Log("")
    t.Logf("QI matches: Normal=%d/5, Forced=%d/5", normalQIMatches, forcedQIMatches)
    
    if forcedMatches > normalMatches {
        t.Log("")
        t.Log("SUCCESS: Forcing transient=1 produces more matching bytes!")
        t.Logf("Improvement: %d -> %d bytes", normalMatches, forcedMatches)
    } else if forcedMatches == normalMatches && forcedQIMatches > normalQIMatches {
        t.Log("")
        t.Log("PARTIAL SUCCESS: Same byte divergence point but more QI matches with forced transient")
    } else {
        t.Log("")
        t.Log("NOTE: Forcing transient did not improve byte matching")
        t.Log("This suggests additional issues beyond transient flag")
    }
}
