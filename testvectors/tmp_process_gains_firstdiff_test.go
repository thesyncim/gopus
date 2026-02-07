//go:build cgo_libopus

package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestTmpProcessGainsFirstDiff(t *testing.T) {
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

	gainTrace := &silk.GainLoopTrace{}
	goEnc.SetSilkTrace(&silk.EncoderTrace{GainLoop: gainTrace})

	gainTraces := make([]silk.GainLoopTrace, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		snap := *gainTrace
		gainTraces = append(gainTraces, snap)
	}

	found := false
	for i := 0; i < len(gainTraces); i++ {
		lib, ok := captureLibopusProcessGainsAtFrame(original, sampleRate, channels, bitrate, frameSize, i)
		if !ok {
			continue
		}
		tr := gainTraces[i]
		n := tr.NumSubframes
		if n <= 0 {
			continue
		}
		goGains := make([]float32, n)
		goRes := make([]float32, n)
		for k := 0; k < n; k++ {
			goGains[k] = tr.GainsBefore[k]
			goRes[k] = tr.ResNrgBefore[k]
		}
		libGains := make([]float32, lib.NumSubframes)
		libRes := make([]float32, lib.NumSubframes)
		for k := 0; k < lib.NumSubframes; k++ {
			libGains[k] = lib.GainsBefore[k]
			libRes[k] = lib.ResNrgBefore[k]
		}
		if i >= 38 && i <= 42 {
			t.Logf("frame=%d go: signal=%d snrQ7=%d tilt=%d speech=%d gains=%v res=%v",
				i, tr.SignalType, tr.SNRDBQ7, tr.InputTiltQ15, tr.SpeechActivityQ8, goGains, goRes)
			t.Logf("frame=%d lib: signal=%d snrQ7=%d tilt=%d speech=%d gains=%v res=%v",
				i, lib.SignalType, lib.SNRDBQ7, lib.InputTiltQ15, lib.SpeechActivityQ8, libGains, libRes)
		}
		if idx, goVal, libVal, ok := firstFloat32BitsDiff(goGains, libGains); ok {
			if math.Abs(float64(goVal-libVal)) < 1e-3 {
				// Ignore tiny float32 round-off.
			} else {
				t.Logf("first gainsIn diff frame=%d idx=%d go=%.8f lib=%.8f", i, idx, goVal, libVal)
				t.Logf("frame=%d go gains=%v", i, goGains)
				t.Logf("frame=%d lib gains=%v", i, libGains)
				found = true
				break
			}
		}
		if idx, goVal, libVal, ok := firstFloat32BitsDiff(goRes, libRes); ok {
			if math.Abs(float64(goVal-libVal)) < 10.0 {
				// Ignore tiny residual-energy round-off.
			} else {
				t.Logf("first resNrg diff frame=%d idx=%d go=%.8f lib=%.8f", i, idx, goVal, libVal)
				t.Logf("frame=%d go res=%v", i, goRes)
				t.Logf("frame=%d lib res=%v", i, libRes)
				found = true
				break
			}
		}
	}
	if !found {
		t.Log("no process_gains input diffs found")
	}
}
