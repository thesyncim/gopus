//go:build cgo_libopus

package testvectors

import (
    "testing"

    "github.com/thesyncim/gopus/encoder"
    "github.com/thesyncim/gopus/silk"
    "github.com/thesyncim/gopus/types"
)

func TestTmpPerFramePacketLenTrace(t *testing.T) {
    const (
        sampleRate = 48000
        channels   = 1
        frameSize  = 960
        bitrate    = 32000
    )

    numFrames := sampleRate / frameSize
    totalSamples := numFrames * frameSize * channels
    original := generateEncoderTestSignal(totalSamples, channels)

    goEnc := encoder.NewEncoder(sampleRate, channels)
    goEnc.SetMode(encoder.ModeSILK)
    goEnc.SetBandwidth(types.BandwidthWideband)
    goEnc.SetBitrate(bitrate)
    framePre := &silk.FrameStateTrace{}
    framePost := &silk.FrameStateTrace{}
    goEnc.SetSilkTrace(&silk.EncoderTrace{FramePre: framePre, Frame: framePost})

    // capture libopus packets for same signal
    libPkts := encodeWithLibopusFloat(original, sampleRate, channels, bitrate, frameSize, 2052)
    if len(libPkts) < numFrames {
        t.Fatalf("lib packets too short: got=%d want>=%d", len(libPkts), numFrames)
    }

    samplesPerFrame := frameSize * channels
    for i := 0; i < numFrames; i++ {
        start := i * samplesPerFrame
        end := start + samplesPerFrame
        pcm := float32ToFloat64(original[start:end])
        pkt, err := goEnc.Encode(pcm, frameSize)
        if err != nil {
            t.Fatalf("gopus encode failed at frame %d: %v", i, err)
        }
        glen := len(pkt)
        llen := len(libPkts[i].data)
        if i >= 28 && i <= 44 {
            t.Logf("frame=%d len go=%d lib=%d diff=%d", i, glen, llen, glen-llen)
            if snap, ok := captureLibopusOpusSilkStateBeforeFrame(original, sampleRate, channels, bitrate, frameSize, i+1); ok {
                t.Logf("  pre(next) nBitsExceeded go=%d lib=%d targetRate go=%d lib=%d snr go=%d lib=%d",
                    framePre.NBitsExceeded, snap.NBitsExceeded, framePre.TargetRateBps, snap.TargetRateBps, framePre.SNRDBQ7, snap.SNRDBQ7)
            }
        }
    }
}
