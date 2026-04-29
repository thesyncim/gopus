package gopus

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/silk"
)

type plcDecodeState struct {
	packetFrameSize    int
	mode               Mode
	bandwidth          Bandwidth
	packetStereo       bool
	useDecoderPLCState bool
	queueCachedDRED    bool
}

func nextPLCChunkSamples(sampleRate int, mode Mode, remaining int) int {
	if sampleRate <= 0 || remaining <= 0 {
		return 0
	}
	f20 := sampleRate / 50
	f10 := f20 / 2
	f5 := f10 / 2
	if remaining >= f20 {
		return f20
	}
	if remaining > f10 {
		return f10
	}
	if mode != ModeSILK && remaining > f5 && remaining < f10 {
		return f5
	}
	return remaining
}

func (d *Decoder) decodePLCChunksInto(out []float32, frameSize int, state plcDecodeState) (int, error) {
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	if frameSize > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	needed := frameSize * d.channels
	if len(out) < needed {
		return 0, ErrBufferTooSmall
	}

	remaining := frameSize
	offset := 0
	for remaining > 0 {
		// The packet/frame-size plumbing in the decoder core is still tracked in
		// Opus's 48 kHz sample domain, even when the public decoder was created
		// for a lower output rate. Chunk PLC in that same domain so CELT/Hybrid
		// PLC continues to see valid 120/240/480/960 frame sizes.
		chunk := nextPLCChunkSamples(48000, state.mode, remaining)
		if chunk <= 0 {
			break
		}
		n, err := d.decodeOpusFrameIntoWithStatePolicy(
			out[offset*d.channels:],
			nil,
			chunk,
			state.packetFrameSize,
			state.mode,
			state.bandwidth,
			state.packetStereo,
			state.useDecoderPLCState,
		)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			break
		}
		offset += n
		remaining -= n
	}

	return frameSize, nil
}

func (d *Decoder) decodeHybridDRED48kInto(out []float32, frameSize int, state plcDecodeState) (int, bool, error) {
	useDRED := d.dredCachedPayloadActive()
	var lowbandSnapshot *silk.DeepPLCLowbandSnapshot
	cleanupHook := func() {}
	usedHook := func() bool { return false }
	queued := internaldred.FeatureWindow{}
	if useDRED {
		queued = d.queueActiveDREDRecovery(frameSize)
	}
	if useDRED && d.dredNeuralConcealmentAvailable() && d.silkDecoder != nil {
		lowbandSnapshot = d.silkDecoder.SnapshotDeepPLCLowbandMono()
		cleanupHook, usedHook = d.beginHybridDREDLowbandHook()
	}
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(out, frameSize, state)
	if err != nil {
		return 0, false, err
	}
	if !useDRED {
		if d.dredNeuralConcealmentAvailable() {
			d.prepareDRED48kNeuralEntry(frameSize, state.mode, false)
		}
		if !d.dredNeuralConcealmentAvailable() || n <= 0 || n%3 != 0 {
			return n, false, nil
		}
		r := d.dredRecoveryState()
		neural := d.dredNeuralState()
		b := d.dred48kBridgeState()
		if r == nil || neural == nil || b == nil || d.celtDecoder == nil {
			return n, false, nil
		}
		generate := func(frame []float32) bool {
			if len(frame) < lpcnetplc.FrameSize {
				return false
			}
			if r.dredPLC.Blend() == 0 {
				return r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&neural.dredAnalysis, &neural.dredPredictor, &neural.dredFARGAN, frame[:lpcnetplc.FrameSize])
			}
			return r.dredPLC.GenerateConcealedFrameFloat(&neural.dredPredictor, &neural.dredFARGAN, frame[:lpcnetplc.FrameSize])
		}
		ok := d.celtDecoder.ConcealPLCNeural48kMonoStateOnly(
			n,
			&b.dredLastNeural,
			b.dredPLCPCM[:],
			&b.dredPLCFill,
			&b.dredPLCPreemphMem,
			generate,
		)
		if !ok {
			return n, false, nil
		}
		return n, true, nil
	}
	if usedHook() {
		d.finishActiveDREDRecovery(n)
		return n, true, nil
	}
	if queued.NeededFeatureFrames > 0 || d.dredRecoveryState() != nil {
		if d.advanceHybridDREDLowbandState(n, lowbandSnapshot) {
			return n, true, nil
		}
	}
	return n, true, nil
}

func (d *Decoder) decodeDRED48kNeuralPLCInto(out []float32, frameSize int, state plcDecodeState) (int, bool, error) {
	if d == nil {
		return 0, false, ErrInvalidArgument
	}
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	if frameSize > d.maxPacketSamples {
		return 0, false, ErrPacketTooLarge
	}

	needed := frameSize * d.channels
	if len(out) < needed {
		return 0, false, ErrBufferTooSmall
	}
	if !d.dredNeuralConcealmentAvailable() {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	if d.sampleRate != 48000 || d.channels != 1 || (state.mode != ModeCELT && state.mode != ModeHybrid) {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	if state.mode == ModeHybrid {
		return d.decodeHybridDRED48kInto(out, frameSize, state)
	}
	if !d.applyDREDNeuralConcealment(out[:needed], frameSize) {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	return frameSize, true, nil
}
