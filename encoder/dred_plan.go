package encoder

import (
	"math"
	"math/bits"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/types"
)

var dredBitsTable = [...]float64{73.2, 68.1, 62.5, 57.0, 51.5, 45.7, 39.9, 32.4, 26.4, 20.4, 16.3, 13.0, 9.3, 8.2, 7.2, 6.4}

type dredEmissionPlan struct {
	q0           int
	dQ           int
	qmax         int
	targetChunks int
	targetBytes  int
	bitrate      int
}

func dredBitsToBitrate(bitCount, frameSize int) int {
	durationMs := frameDurationMs(frameSize)
	if durationMs <= 0 || bitCount <= 0 {
		return 0
	}
	return bitCount * 1000 / durationMs
}

func estimateDREDBits(q0, dQ, qmax, duration, targetBits int) (int, int) {
	bitsUsed := 8.0 * float64(3+internaldred.ExperimentalHeaderBytes)
	bitsUsed += 50.0 + dredBitsTable[q0]

	dredChunks := (duration + 5) / 4
	if dredChunks > internaldred.NumRedundancyFrames/2 {
		dredChunks = internaldred.NumRedundancyFrames / 2
	}
	targetChunks := 0
	header := internaldred.Header{Q0: q0, DQ: dQ, QMax: qmax}
	for i := 0; i < dredChunks; i++ {
		q := header.QuantizerLevel(i)
		bitsUsed += dredBitsTable[q]
		if int(bitsUsed) < targetBits {
			targetChunks = i + 1
		}
	}
	return int(math.Floor(0.5 + bitsUsed)), targetChunks
}

func (e *Encoder) computeDREDEmissionPlan(frameSize int) (dredEmissionPlan, bool) {
	if e.dred == nil || e.dred.duration <= 0 || !e.dredModelsLoaded() || e.bitrate <= 0 {
		return dredEmissionPlan{}, false
	}

	packetLoss := e.packetLoss
	if packetLoss < 0 {
		packetLoss = 0
	}
	if packetLoss > 100 {
		packetLoss = 100
	}

	var dredFrac float64
	bitrateOffset := 12000
	if e.fecEnabled && e.lbrrCoded {
		dredFrac = math.Min(0.7, 3.0*float64(packetLoss)/100.0)
		bitrateOffset = 20000
	} else if packetLoss > 5 {
		dredFrac = math.Min(0.8, 0.55+float64(packetLoss)/100.0)
	} else {
		dredFrac = 12.0 * float64(packetLoss) / 100.0
	}

	frameRateScale := float64(frameSize*50) / float64(e.sampleRate)
	dredFrac = dredFrac / (dredFrac + (1.0-dredFrac)*frameRateScale)

	rateBudget := e.bitrate - bitrateOffset
	if rateBudget < 1 {
		rateBudget = 1
	}
	q0 := 51 - 3*bits.Len(uint(rateBudget))
	if q0 < 4 {
		q0 = 4
	}
	if q0 > 15 {
		q0 = 15
	}
	dQ := 5
	if e.bitrate-bitrateOffset > 36000 {
		dQ = 3
	}
	qmax := 15
	targetDREDBitrate := int(dredFrac * float64(e.bitrate-bitrateOffset))
	if targetDREDBitrate < 0 {
		targetDREDBitrate = 0
	}
	maxBits, targetChunks := estimateDREDBits(q0, dQ, qmax, e.dred.duration, bitrateToBits(targetDREDBitrate, frameSize))
	if targetChunks < 2 {
		return dredEmissionPlan{}, false
	}
	dredBitrate := dredBitsToBitrate(maxBits, frameSize)
	if targetDREDBitrate < dredBitrate {
		dredBitrate = targetDREDBitrate
	}
	if dredBitrate <= 0 {
		return dredEmissionPlan{}, false
	}
	return dredEmissionPlan{
		q0:           q0,
		dQ:           dQ,
		qmax:         qmax,
		targetChunks: targetChunks,
		targetBytes:  bitrateToBits(dredBitrate, frameSize) / 8,
		bitrate:      dredBitrate,
	}, true
}

func (e *Encoder) maybeBuildSingleFrameDREDPacket(frameData []byte, actualMode Mode, packetBW types.Bandwidth, frameSize int, stereo bool) ([]byte, bool, error) {
	if actualMode == ModeCELT {
		return nil, false, nil
	}
	plan, ok := e.computeDREDEmissionPlan(frameSize)
	if !ok {
		return nil, false, nil
	}

	targetSize := targetBytesForBitrate(e.bitrate, frameSize)
	baseLen := 1 + len(frameData)
	if targetSize < baseLen+1 {
		return nil, false, nil
	}
	withPadding := e.bitrateMode == ModeCBR

	dredBytesLeft := targetSize - baseLen - 3
	if dredBytesLeft > internaldred.MaxDataSize {
		dredBytesLeft = internaldred.MaxDataSize
	}
	dredBytesLeft -= (dredBytesLeft + 1 + internaldred.ExperimentalHeaderBytes) / 255
	if plan.targetBytes > 0 && dredBytesLeft > plan.targetBytes {
		dredBytesLeft = plan.targetBytes
	}
	if dredBytesLeft < internaldred.MinBytes+internaldred.ExperimentalHeaderBytes {
		return nil, false, nil
	}

	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil {
		return nil, false, nil
	}

	maxChunks := (e.dred.duration + 5) / 4
	if maxChunks > internaldred.NumRedundancyFrames/2 {
		maxChunks = internaldred.NumRedundancyFrames / 2
	}
	if maxChunks > plan.targetChunks {
		maxChunks = plan.targetChunks
	}
	if maxChunks < 1 {
		return nil, false, nil
	}

	n := e.buildDREDExperimentalPayload(runtime.payload[:dredBytesLeft], maxChunks, plan.q0, plan.dQ, plan.qmax)
	if n == 0 {
		return nil, false, nil
	}

	packetLen, err := buildPacketWithSingleExtensionInto(
		e.scratchPacket,
		frameData,
		modeToTypes(actualMode),
		packetBW,
		frameSize,
		stereo,
		internaldred.ExtensionID,
		runtime.payload[:n],
		targetSize,
		withPadding,
	)
	if err != nil {
		return nil, false, err
	}
	return e.scratchPacket[:packetLen], true, nil
}

func (e *Encoder) maybeBuildMultiFrameDREDPacket(frames [][]byte, actualMode Mode, packetBW types.Bandwidth, packetFrameSize, packetTOCFrameSize int, stereo bool, vbr bool) ([]byte, bool, error) {
	if actualMode == ModeCELT {
		return nil, false, nil
	}
	plan, ok := e.computeDREDEmissionPlan(packetFrameSize)
	if !ok {
		return nil, false, nil
	}

	targetSize := targetBytesForBitrate(e.bitrate, packetFrameSize)
	baseLen := 2
	if vbr {
		for i := 0; i < len(frames)-1; i++ {
			baseLen += frameLengthBytes(len(frames[i]))
		}
	}
	for _, frame := range frames {
		baseLen += len(frame)
	}
	if targetSize < baseLen+1 {
		return nil, false, nil
	}
	withPadding := e.bitrateMode == ModeCBR

	dredBytesLeft := targetSize - baseLen - 3
	if dredBytesLeft > internaldred.MaxDataSize {
		dredBytesLeft = internaldred.MaxDataSize
	}
	dredBytesLeft -= (dredBytesLeft + 1 + internaldred.ExperimentalHeaderBytes) / 255
	if plan.targetBytes > 0 && dredBytesLeft > plan.targetBytes {
		dredBytesLeft = plan.targetBytes
	}
	if dredBytesLeft < internaldred.MinBytes+internaldred.ExperimentalHeaderBytes {
		return nil, false, nil
	}

	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil {
		return nil, false, nil
	}

	maxChunks := (e.dred.duration + 5) / 4
	if maxChunks > internaldred.NumRedundancyFrames/2 {
		maxChunks = internaldred.NumRedundancyFrames / 2
	}
	if maxChunks > plan.targetChunks {
		maxChunks = plan.targetChunks
	}
	if maxChunks < 1 {
		return nil, false, nil
	}

	n := e.buildDREDExperimentalPayload(runtime.payload[:dredBytesLeft], maxChunks, plan.q0, plan.dQ, plan.qmax)
	if n == 0 {
		return nil, false, nil
	}

	packetLen, err := buildMultiFramePacketWithSingleFrame0ExtensionInto(
		e.scratchPacket,
		frames,
		modeToTypes(actualMode),
		packetBW,
		packetTOCFrameSize,
		stereo,
		vbr,
		internaldred.ExtensionID,
		runtime.payload[:n],
		targetSize,
		withPadding,
	)
	if err != nil {
		return nil, false, err
	}
	return e.scratchPacket[:packetLen], true, nil
}
