//go:build cgo_libopus

package testvectors

import cgowrap "github.com/thesyncim/gopus/celt/cgo_test"

type libopusProcessGainsSnapshot struct {
	GainsIndices     []int8
	GainsUnqQ16      []int32
	QuantOffsetType  int
	LastGainIndexOut int8
	Lambda           float32
}

type libopusProcessGainsFrameSnapshot = cgowrap.OpusProcessGainsFrameSnapshot

func libopusProcessGainsFromTraceInputs(
	gainsIn []float32,
	resNrgIn []float32,
	nbSubfr int,
	subfrLength int,
	signalType int,
	predGainQ7 int32,
	inputTiltQ15 int,
	snrDBQ7 int,
	speechActivityQ8 int,
	nStatesDelayedDecision int,
	lastGainIndexIn int8,
	conditional bool,
) (libopusProcessGainsSnapshot, bool) {
	snap, ok := cgowrap.SilkProcessGainsFLP(
		gainsIn,
		resNrgIn,
		nbSubfr,
		subfrLength,
		signalType,
		float32(predGainQ7)/128.0,
		inputTiltQ15,
		snrDBQ7,
		speechActivityQ8,
		nStatesDelayedDecision,
		lastGainIndexIn,
		conditional,
	)
	if !ok {
		return libopusProcessGainsSnapshot{}, false
	}

	out := libopusProcessGainsSnapshot{
		GainsIndices:     make([]int8, nbSubfr),
		GainsUnqQ16:      make([]int32, nbSubfr),
		QuantOffsetType:  snap.QuantOffsetType,
		LastGainIndexOut: snap.LastGainIndexOut,
		Lambda:           snap.Lambda,
	}
	for i := 0; i < nbSubfr && i < 4; i++ {
		out.GainsIndices[i] = snap.GainsIndices[i]
		out.GainsUnqQ16[i] = snap.GainsUnqQ16[i]
	}
	return out, true
}

func captureLibopusProcessGainsAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusProcessGainsFrameSnapshot, bool) {
	return cgowrap.CaptureOpusProcessGainsAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}
