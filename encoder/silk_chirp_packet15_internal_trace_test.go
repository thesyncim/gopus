package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestSILKChirpPacket15InternalTrace(t *testing.T) {
	const (
		packetIndex  = 15
		frameSize    = 1920
		signalFrames = 25
		channels     = 1
		bitrate      = 32000
	)

	totalSamples := signalFrames * frameSize * channels
	signal, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantChirpSweepV1, 48000, totalSamples, channels)
	if err != nil {
		t.Fatalf("generate chirp sweep: %v", err)
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(ModeCBR)

	trace := &silk.EncoderTrace{
		NLSF:     &silk.NLSFTrace{},
		GainLoop: &silk.GainLoopTrace{},
		FramePre: &silk.FrameStateTrace{},
		Frame:    &silk.FrameStateTrace{},
	}
	type capture struct {
		frameInPacket int
		pre           silk.FrameStateTrace
		nlsf          silk.NLSFTrace
		gain          silk.GainLoopTrace
	}
	var captures []capture
	currentPacket := -1
	trace.AfterFrame = func(frameInPacket int, tr *silk.EncoderTrace) {
		if currentPacket != packetIndex {
			return
		}
		captures = append(captures, capture{
			frameInPacket: frameInPacket,
			pre:           cloneFrameStateTrace(tr.FramePre),
			nlsf:          cloneNLSFTrace(tr.NLSF),
			gain:          cloneGainLoopTrace(tr.GainLoop),
		})
	}
	enc.SetSilkTrace(trace)

	samplesPerFrame := frameSize * channels
	var packet15 []byte
	for i := 0; i < signalFrames; i++ {
		currentPacket = i
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64OpusDemoF32ForTrace(signal[start:end])
		pkt, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", i, err)
		}
		if i == packetIndex {
			packet15 = append(packet15[:0], pkt...)
		}
	}

	if len(captures) != 2 {
		t.Fatalf("captured %d internal frames, want 2", len(captures))
	}

	t.Logf("packet %d len=%d head=%s", packetIndex, len(packet15), hexFirstNForTrace(packet15, 12))
	for _, c := range captures {
		t.Logf("frame %d pre: framesPerPacket=%d framesEncoded=%d prevLag=%d prevSignal=%d lastGain=%d target=%d snrQ7=%d bitsExceeded=%d",
			c.frameInPacket,
			c.pre.NFramesPerPacket, c.pre.NFramesEncoded, c.pre.PrevLag, c.pre.PrevSignalType, c.pre.LastGainIndex,
			c.pre.TargetRateBps, c.pre.SNRDBQ7, c.pre.NBitsExceeded)
		t.Logf("frame %d nlsf: interp=%d stage1=%d muQ20=%d prev=%v raw=%v quant=%v residuals=%v candStage1=%v candRD=%v",
			c.frameInPacket,
			c.nlsf.InterpIdx, c.nlsf.Stage1Idx, c.nlsf.MuQ20,
			append([]int16(nil), c.nlsf.PrevNLSFQ15...),
			append([]int16(nil), c.nlsf.RawNLSFQ15...),
			append([]int16(nil), c.nlsf.QuantizedNLSFQ15...),
			append([]int(nil), c.nlsf.Residuals...),
			append([]int(nil), c.nlsf.CandidateStage1...),
			append([]int32(nil), c.nlsf.CandidateRDQ25...))
		t.Logf("frame %d gain: before=%v resNrg=%v after=%v gainsUnq=%v lastGainPrev=%d seed=%d->%d maxBits=%d useCBR=%v speechQ8=%d tiltQ15=%d predGainQ7=%d qBands=%v inputQ=%.6f codingQ=%.6f",
			c.frameInPacket,
			shortFloat32ForTrace(c.gain.GainsBefore[:], c.gain.NumSubframes),
			shortFloat32ForTrace(c.gain.ResNrgBefore[:], c.gain.NumSubframes),
			shortFloat32ForTrace(c.gain.GainsAfter[:], c.gain.NumSubframes),
			shortInt32ForTrace(c.gain.GainsUnqQ16[:], c.gain.NumSubframes),
			c.gain.LastGainIndexPrev, c.gain.SeedIn, c.gain.SeedOut, c.gain.MaxBits, c.gain.UseCBR,
			c.gain.SpeechActivityQ8, c.gain.InputTiltQ15, c.gain.PredGainQ7,
			c.gain.InputQualityBandsQ15, c.gain.InputQuality, c.gain.CodingQuality)
		for _, iter := range c.gain.Iterations {
			t.Logf("frame %d iter %d: gainMultQ8=%d gainsID=%d idx=%v gainsQ16=%v bits=%d seed=%d/%d/%d skipped=%v",
				c.frameInPacket, iter.Iter, iter.GainMultQ8, iter.GainsID,
				shortInt8ForTrace(iter.GainsIndices[:], c.gain.NumSubframes),
				shortInt32ForTrace(iter.GainsQ16[:], c.gain.NumSubframes),
				iter.Bits, iter.SeedIn, iter.SeedAfterNSQ, iter.SeedOut, iter.SkippedNSQ)
		}
	}
}

func TestSILKChirpPacket14BufferState(t *testing.T) {
	const (
		packetIndex  = 14
		frameSize    = 1920
		signalFrames = 25
		channels     = 1
		bitrate      = 32000
	)

	totalSamples := signalFrames * frameSize * channels
	signal, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantChirpSweepV1, 48000, totalSamples, channels)
	if err != nil {
		t.Fatalf("generate chirp sweep: %v", err)
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(ModeCBR)

	samplesPerFrame := frameSize * channels
	for i := 0; i <= packetIndex; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64OpusDemoF32ForTrace(signal[start:end])
		if _, err := enc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("encode packet %d: %v", i, err)
		}
	}

	if enc.silkEncoder == nil {
		t.Fatal("expected SILK encoder")
	}
	buf := enc.silkEncoder.InputBuffer()
	fsKHz := enc.silkEncoder.SampleRate() / 1000
	keep := fsKHz*20 + enc.silkEncoder.LaShape()
	if keep > len(buf) {
		keep = len(buf)
	}
	scale := float32(32768.0)
	head := make([]int, 0, 8)
	for i := 0; i < 8 && i < keep; i++ {
		head = append(head, int(buf[i]*scale))
	}
	mid := make([]int, 0, 8)
	for i := keep - 12; i < keep-4 && i >= 0 && i < keep; i++ {
		mid = append(mid, int(buf[i]*scale))
	}
	tail := make([]int, 0, 4)
	for i := keep - 4; i < keep && i >= 0; i++ {
		tail = append(tail, int(buf[i]*scale))
	}
	t.Logf("keep=%d head=%v mid=%v tail=%v", keep, head, mid, tail)
}

func cloneFrameStateTrace(src *silk.FrameStateTrace) silk.FrameStateTrace {
	if src == nil {
		return silk.FrameStateTrace{}
	}
	dst := *src
	dst.PitchBuf = append([]float32(nil), src.PitchBuf...)
	return dst
}

func cloneNLSFTrace(src *silk.NLSFTrace) silk.NLSFTrace {
	if src == nil {
		return silk.NLSFTrace{}
	}
	dst := *src
	dst.LTPRes = append([]float32(nil), src.LTPRes...)
	dst.RawNLSFQ15 = append([]int16(nil), src.RawNLSFQ15...)
	dst.PrevNLSFQ15 = append([]int16(nil), src.PrevNLSFQ15...)
	dst.Residuals = append([]int(nil), src.Residuals...)
	dst.QuantizedNLSFQ15 = append([]int16(nil), src.QuantizedNLSFQ15...)
	return dst
}

func cloneGainLoopTrace(src *silk.GainLoopTrace) silk.GainLoopTrace {
	if src == nil {
		return silk.GainLoopTrace{}
	}
	dst := *src
	dst.Iterations = append([]silk.GainLoopIter(nil), src.Iterations...)
	return dst
}

func float32ToFloat64OpusDemoF32ForTrace(in []float32) []float64 {
	const inv24 = 1.0 / 8388608.0
	out := make([]float64, len(in))
	for i, s := range in {
		q := floorForTrace(0.5 + float64(s)*8388608.0)
		out[i] = q * inv24
	}
	return out
}

func floorForTrace(v float64) float64 {
	iv := int64(v)
	if float64(iv) > v {
		iv--
	}
	return float64(iv)
}

func hexFirstNForTrace(pkt []byte, n int) string {
	if n > len(pkt) {
		n = len(pkt)
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ' ')
		}
		b := pkt[i]
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(out)
}

func shortFloat32ForTrace(in []float32, n int) []float32 {
	if n > len(in) {
		n = len(in)
	}
	out := make([]float32, n)
	copy(out, in[:n])
	return out
}

func shortInt32ForTrace[T ~int32](in []T, n int) []T {
	if n > len(in) {
		n = len(in)
	}
	out := make([]T, n)
	copy(out, in[:n])
	return out
}

func shortInt8ForTrace(in []int8, n int) []int8 {
	if n > len(in) {
		n = len(in)
	}
	out := make([]int8, n)
	copy(out, in[:n])
	return out
}
