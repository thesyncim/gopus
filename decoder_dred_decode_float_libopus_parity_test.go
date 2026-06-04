//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	silkpkg "github.com/thesyncim/gopus/internal/silk"
)

// assertDecodedPCMQuality is the canonical end-to-end audio gate for this file's
// DRED/PLC decode + concealment cases. It scores the decoder's output PCM against
// the libopus reference PCM with the trusted opus_compare-based comparator
// (qualitycompare), replacing the historical sub-perceptual sample-wise PCM
// tolerances on the OUTPUT audio.
//
// Two-tier discipline: this governs end-to-end decoded/concealed audio ONLY.
// Internal DRED/FARGAN/CELT/SILK state snapshots remain bit-exact-tier oracles
// (assertDecoderDRED*StateApproxEqual*) and are NOT routed through this gate.
//
// opus_compare's Q metric requires 48 kHz and >=480 samples/channel. It is the
// trusted metric, but it is a *windowed* psychoacoustic model: on a single
// isolated CELT frame (480-960 samples / 10-20 ms) its Q becomes statistically
// unstable (the same physical sub-perceptual drift yields Q=100 on one frame and
// a wild negative Q on the next), even when the waveforms are essentially
// identical (corr>0.9998). These DRED tests each decode ONE concealed frame, so
// for them only segments meaningfully longer than a single max frame get the Q
// floor; shorter ones (and all sub-48k decoder-rate cases) gate on the
// delay-searched correlation/RMS envelope of QualityBarNearExact, which is the
// near-exact bar SILK/CELT/Hybrid already meet vs libopus. The measured Q is
// still computed and logged whenever it is computable (48 kHz, >=480 samples) so
// the analysis-frontend drift (e.g. the SWB history[2274] ~6.2e-3 frame_corr
// residual) is visible: a near-exact Q proves it is not a quality divergence.
func assertDecodedPCMQuality(t *testing.T, candidate, reference []float32, sampleRate, channels int, label string) {
	t.Helper()
	n := len(candidate)
	if len(reference) < n {
		n = len(reference)
	}
	candidate = candidate[:n]
	reference = reference[:n]
	if channels < 1 {
		channels = 1
	}
	maxDelay := 960
	samplesPerCh := n / channels
	// opus_compare returns a real Q only at 48 kHz with >=480 samples/channel.
	qComputable := sampleRate == 48000 && samplesPerCh >= 480
	// Only trust the Q floor when the segment is longer than a single max CELT
	// frame (one 20 ms frame is below opus_compare's reliable window).
	qTrustworthy := qComputable && samplesPerCh >= 1920
	// CompareDecodedFloat32 rejects non-48k sample rates outright, but its
	// correlation/RMS diagnostics are rate-independent; pass 48000 there purely
	// to obtain those diagnostics (and a logged-but-not-gated Q where computable)
	// for the sub-48k decoder-rate and single-frame cases.
	compareRate := sampleRate
	if sampleRate != 48000 {
		compareRate = 48000
	}
	cmp, err := qualitycompare.CompareDecodedFloat32(candidate, reference, compareRate, channels, maxDelay)
	if err != nil {
		t.Fatalf("%s: compare decoded quality: %v", label, err)
	}
	bar := qualitycompare.QualityBarNearExact
	if !qTrustworthy {
		// Q is not computable or not statistically reliable on this segment:
		// disable the Q floor and gate on the near-exact corr/RMS envelope.
		bar.MinQ = math.Inf(-1)
	}
	qualitycompare.AssertQuality(t, cmp, bar, label)
}

const (
	libopusDecoderDREDDecodeFloatInputMagic  = "GDDI"
	libopusDecoderDREDDecodeFloatOutputMagic = "GDDO"
	libopusCELTFramePLCNeural                = 4
	libopusCELTFrameDRED                     = 5
)

type libopusDecoderDREDDecodeFloatInfo struct {
	parseRet  int
	dredEnd   int
	warmupRet int
	ret       int
	nextRet   int
	channels  int
	state     lpcnetplc.StateSnapshot
	fargan    lpcnetplc.FARGANSnapshot
	celt48k   libopusDecoderDREDCELTSnapshot
	silk      libopusDecoderDREDSILKSnapshot
	pcm       []float32
	nextPCM   []float32
}

type libopusDecoderDREDCELTSnapshot struct {
	LastFrameType     int
	PLCFill           int
	PLCDuration       int
	SkipPLC           int
	PLCPreemphasisMem float32
	PreemphMem        [2]float32
	PLCPCM            [4 * lpcnetplc.FrameSize]float32
	WarmupPreemphMem  [2]float32
	WarmupPLCPreemph  float32
	WarmupPLCUpdate   [4 * lpcnetplc.FrameSize]float32
}

var libopusDecoderDREDDecodeFloatHelper libopustest.HelperCache

func getLibopusDecoderDREDDecodeFloatHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDecoderDREDDecodeFloatHelper, "libopus_decoder_dred_decode_float_info.c", "gopus_libopus_decoder_dred_decode_float", true)
}

func probeLibopusDecoderDREDDecodeFloat(seedPacket, packet []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packet, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, 0)
}

func probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packet []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, gain int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeAndNextFloatWithGain(seedPacket, packet, nil, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, gain)
}

func probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packet, nextPacket []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeAndNextFloatWithGain(seedPacket, packet, nextPacket, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, 0)
}

func probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket []byte, packetInfo libopusDREDPacket, decoderSampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	return probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, maxDRED, oracleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples)
}

func probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket []byte, packetInfo libopusDREDPacket, nextPacket []byte, decoderSampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	return probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, maxDRED, oracleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples)
}

func probeLibopusDecoderDREDDecodeAndNextFloatWithGain(seedPacket, packet, nextPacket []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, gain int) (libopusDecoderDREDDecodeFloatInfo, error) {
	binPath, err := getLibopusDecoderDREDDecodeFloatHelperPath()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	decoderModelBlob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}

	payload := libopustest.NewOraclePayloadVersion(libopusDecoderDREDDecodeFloatInputMagic, 7,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(warmupDREDOffsetSamples),
		uint32(dredOffsetSamples),
		uint32(frameSizeSamples),
	)
	payload.I32(int32(gain))
	payload.U32s(
		uint32(len(seedPacket)),
		uint32(len(packet)),
		uint32(len(nextPacket)),
		uint32(len(decoderModelBlob)),
		uint32(len(dredModelBlob)),
	)
	for _, chunk := range [][]byte{
		seedPacket,
		packet,
		nextPacket,
		decoderModelBlob,
		dredModelBlob,
	} {
		payload.Raw(chunk)
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "decoder dred decode", libopusDecoderDREDDecodeFloatOutputMagic, 5)
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	info := libopusDecoderDREDDecodeFloatInfo{
		parseRet:  int(reader.I32()),
		dredEnd:   int(reader.I32()),
		warmupRet: int(reader.I32()),
		ret:       int(reader.I32()),
		nextRet:   int(reader.I32()),
		channels:  int(reader.I32()),
	}
	info.state.Blend = int(reader.I32())
	info.state.LossCount = int(reader.I32())
	info.state.AnalysisGap = int(reader.I32())
	info.state.AnalysisPos = int(reader.I32())
	info.state.PredictPos = int(reader.I32())
	info.state.FECReadPos = int(reader.I32())
	info.state.FECFillPos = int(reader.I32())
	info.state.FECSkip = int(reader.I32())
	info.fargan.ContInitialized = reader.I32() != 0
	info.fargan.LastPeriod = int(reader.I32())
	info.celt48k.LastFrameType = int(reader.I32())
	info.celt48k.PLCFill = int(reader.I32())
	info.celt48k.PLCDuration = int(reader.I32())
	info.celt48k.SkipPLC = int(reader.I32())
	info.celt48k.PLCPreemphasisMem = reader.Float32()
	info.silk.LagPrev = reader.I32()
	info.silk.LastGainIndex = int(reader.I32())
	info.silk.LossCount = int(reader.I32())
	info.silk.PrevSignalType = int(reader.I32())
	readBits := func(dst []float32) error {
		for i := range dst {
			dst[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return err
		}
		return nil
	}
	if info.ret > 0 && info.channels > 0 {
		info.pcm = make([]float32, info.ret*info.channels)
		if err := readBits(info.pcm); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	if info.nextRet > 0 && info.channels > 0 {
		info.nextPCM = make([]float32, info.nextRet*info.channels)
		if err := readBits(info.nextPCM); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]float32{
		info.state.Features[:],
		info.state.Cont[:],
		info.state.PCM[:],
		info.state.PLCNet.GRU1[:],
		info.state.PLCNet.GRU2[:],
		info.state.PLCBak[0].GRU1[:],
		info.state.PLCBak[0].GRU2[:],
		info.state.PLCBak[1].GRU1[:],
		info.state.PLCBak[1].GRU2[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	info.fargan.DeemphMem = reader.Float32()
	for _, dst := range [][]float32{
		info.fargan.PitchBuf[:],
		info.fargan.CondConv1State[:],
		info.fargan.FWC0Mem[:],
		info.fargan.GRU1State[:],
		info.fargan.GRU2State[:],
		info.fargan.GRU3State[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]float32{
		info.celt48k.PreemphMem[:],
		info.celt48k.PLCPCM[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]float32{
		info.silk.SMid[:],
		info.silk.OutBuf[:],
		info.silk.SLPCQ14[:],
		info.silk.ExcQ14[:],
		info.silk.ResamplerIIR[:],
		info.silk.ResamplerFIR[:],
		info.silk.ResamplerDelay[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	if reader.Remaining() >= 4*(2+1+len(info.celt48k.WarmupPLCUpdate)) {
		warmupPLCPreemph := []float32{0}
		for _, dst := range [][]float32{
			info.celt48k.WarmupPreemphMem[:],
			warmupPLCPreemph,
			info.celt48k.WarmupPLCUpdate[:],
		} {
			if err := readBits(dst); err != nil {
				return libopusDecoderDREDDecodeFloatInfo{}, err
			}
		}
		info.celt48k.WarmupPLCPreemph = warmupPLCPreemph[0]
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	return info, nil
}

func requireLibopusDREDDecodeParsed(t testing.TB, info libopusDecoderDREDDecodeFloatInfo, label string) {
	t.Helper()
	if info.parseRet <= 0 {
		t.Fatalf("%s libopus DRED parse ret=%d want >0 (dredEnd=%d)", label, info.parseRet, info.dredEnd)
	}
	if info.parseRet < info.dredEnd {
		t.Fatalf("%s libopus DRED parse ret=%d before dredEnd=%d", label, info.parseRet, info.dredEnd)
	}
}

func assertDecoderDREDPLCStateApproxEqual(t *testing.T, got, want lpcnetplc.StateSnapshot, label string) {
	t.Helper()
	assertDecoderDREDPLCStateApproxEqualWithin(t, got, want, label, 1e-4)
}

func assertDecoderDREDPLCStateApproxEqualWithin(t *testing.T, got, want lpcnetplc.StateSnapshot, label string, tol float64) {
	t.Helper()
	if got.Blend != want.Blend ||
		got.LossCount != want.LossCount ||
		got.AnalysisGap != want.AnalysisGap ||
		got.AnalysisPos != want.AnalysisPos ||
		got.PredictPos != want.PredictPos ||
		got.FECReadPos != want.FECReadPos ||
		got.FECFillPos != want.FECFillPos ||
		got.FECSkip != want.FECSkip {
		t.Fatalf("%s header=%+v want %+v", label, got, want)
	}
	assertFloat32ApproxEqual(t, got.Features[:lpcnetplc.NumFeatures], want.Features[:lpcnetplc.NumFeatures], label+" features", tol)
	assertFloat32ApproxEqual(t, got.Cont[:], want.Cont[:], label+" continuity", tol)
	assertFloat32ApproxEqual(t, got.PCM[:], want.PCM[:], label+" pcm history", tol)
	assertFloat32ApproxEqual(t, got.PLCNet.GRU1[:], want.PLCNet.GRU1[:], label+" plc net gru1", tol)
	assertFloat32ApproxEqual(t, got.PLCNet.GRU2[:], want.PLCNet.GRU2[:], label+" plc net gru2", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[0].GRU1[:], want.PLCBak[0].GRU1[:], label+" plc bak0 gru1", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[0].GRU2[:], want.PLCBak[0].GRU2[:], label+" plc bak0 gru2", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[1].GRU1[:], want.PLCBak[1].GRU1[:], label+" plc bak1 gru1", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[1].GRU2[:], want.PLCBak[1].GRU2[:], label+" plc bak1 gru2", tol)
}

func assertDecoderDREDFARGANStateApproxEqual(t *testing.T, got, want lpcnetplc.FARGANSnapshot, label string) {
	t.Helper()
	assertDecoderDREDFARGANStateApproxEqualWithin(t, got, want, label, 1e-4)
}

func assertDecoderDREDFARGANStateApproxEqualWithin(t *testing.T, got, want lpcnetplc.FARGANSnapshot, label string, tol float64) {
	t.Helper()
	if got.ContInitialized != want.ContInitialized || got.LastPeriod != want.LastPeriod {
		t.Fatalf("%s header=%+v want %+v", label, got, want)
	}
	if math.Abs(float64(got.DeemphMem-want.DeemphMem)) > tol {
		t.Fatalf("%s deemph=%f want %f", label, got.DeemphMem, want.DeemphMem)
	}
	assertFloat32ApproxEqual(t, got.PitchBuf[:], want.PitchBuf[:], label+" pitch", tol)
	assertFloat32ApproxEqual(t, got.CondConv1State[:], want.CondConv1State[:], label+" cond", tol)
	assertFloat32ApproxEqual(t, got.FWC0Mem[:], want.FWC0Mem[:], label+" fwc0", tol)
	assertFloat32ApproxEqual(t, got.GRU1State[:], want.GRU1State[:], label+" gru1", tol)
	assertFloat32ApproxEqual(t, got.GRU2State[:], want.GRU2State[:], label+" gru2", tol)
	assertFloat32ApproxEqual(t, got.GRU3State[:], want.GRU3State[:], label+" gru3", tol)
}

func assertDecoderDREDCELT48kBridgeApproxEqual(t *testing.T, dec *Decoder, want libopusDecoderDREDCELTSnapshot, label string) {
	t.Helper()
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want, label, 1e-4)
}

func assertDecoderDREDCELT48kBridgeApproxEqualWithin(t *testing.T, dec *Decoder, want libopusDecoderDREDCELTSnapshot, label string, tol float64) {
	t.Helper()
	var plcState celt.PLCStateSnapshot
	var preemphMem [2]float32
	var plcFill int
	var plcPreemphasisMem float32
	var lastNeural bool
	var plcPCM [4 * lpcnetplc.FrameSize]float32
	if dec.celtDecoder != nil {
		plcState = dec.celtDecoder.SnapshotPLCState()
		preemphMem = dec.celtDecoder.SnapshotPreemphasisState()
	}
	if bridge := dec.dred48kBridgeState(); bridge != nil {
		plcFill = bridge.dredPLCFill
		plcPreemphasisMem = bridge.dredPLCPreemphMem
		lastNeural = bridge.dredLastNeural
		copyDREDPLCPCMInt16ToFloat32(&plcPCM, &bridge.dredPLCPCM)
	}
	if plcState.LastFrameType != want.LastFrameType || plcState.PLCDuration != want.PLCDuration || plcState.SkipPLC != (want.SkipPLC != 0) {
		t.Fatalf("%s celt plc state=%+v want {LastFrameType:%d PLCDuration:%d SkipPLC:%t}", label, plcState, want.LastFrameType, want.PLCDuration, want.SkipPLC != 0)
	}
	assertFloat32ApproxEqual(t, preemphMem[:], want.PreemphMem[:], label+" celt preemph_memD", tol)
	if plcFill != want.PLCFill {
		t.Fatalf("%s fill=%d want %d (lastFrameType=%d plcDuration=%d skipPLC=%d preemph=%f)", label, plcFill, want.PLCFill, want.LastFrameType, want.PLCDuration, want.SkipPLC, want.PLCPreemphasisMem)
	}
	if math.Abs(float64(plcPreemphasisMem-want.PLCPreemphasisMem)) > tol {
		t.Fatalf("%s preemph=%f want %f", label, plcPreemphasisMem, want.PLCPreemphasisMem)
	}
	wantNeural := want.LastFrameType == libopusCELTFramePLCNeural || want.LastFrameType == libopusCELTFrameDRED
	if lastNeural != wantNeural {
		t.Fatalf("%s lastNeural=%v want %v (lastFrameType=%d)", label, lastNeural, wantNeural, want.LastFrameType)
	}
	assertFloat32ApproxEqual(t, plcPCM[:], want.PLCPCM[:], label+" plc pcm", tol)
}

func assertDecoderDREDSILKStateApproxEqualWithin(t *testing.T, dec *Decoder, want libopusDecoderDREDSILKSnapshot, bandwidth silkpkg.Bandwidth, label string, tol float64) {
	t.Helper()
	if dec == nil || dec.silkDecoder == nil {
		t.Fatalf("%s missing SILK decoder", label)
	}
	got := dec.silkDecoder.SnapshotDecoderState(bandwidth, 0)
	if got.LagPrev != want.LagPrev ||
		got.LastGainIndex != want.LastGainIndex ||
		got.LossCount != want.LossCount ||
		got.PrevSignalType != want.PrevSignalType {
		t.Fatalf("%s header={LagPrev:%d LastGainIndex:%d LossCount:%d PrevSignalType:%d} want {LagPrev:%d LastGainIndex:%d LossCount:%d PrevSignalType:%d}",
			label,
			got.LagPrev, got.LastGainIndex, got.LossCount, got.PrevSignalType,
			want.LagPrev, want.LastGainIndex, want.LossCount, want.PrevSignalType)
	}
	assertFloat32ApproxEqual(t, got.SMid[:], want.SMid[:], label+" smid", tol)
	assertFloat32ApproxEqual(t, got.OutBuf[:], want.OutBuf[:], label+" outbuf", tol)
	assertFloat32ApproxEqual(t, got.SLPCQ14[:], want.SLPCQ14[:], label+" slpc_q14", tol)
	assertFloat32ApproxEqual(t, got.ExcQ14[:], want.ExcQ14[:], label+" exc_q14", tol)
	assertFloat32ApproxEqual(t, got.ResamplerIIR[:], want.ResamplerIIR[:], label+" resampler iir", tol)
	assertFloat32ApproxEqual(t, got.ResamplerFIR[:], want.ResamplerFIR[:], label+" resampler fir", tol)
	assertFloat32ApproxEqual(t, got.ResamplerDelay[:], want.ResamplerDelay[:], label+" resampler delay", tol)
}

func copyDREDPLCPCMInt16ToFloat32(dst *[4 * lpcnetplc.FrameSize]float32, src *[4 * lpcnetplc.FrameSize]int16) {
	for i := range src {
		dst[i] = float32(src[i]) * (1.0 / 32768.0)
	}
}

func prepareExplicitDREDDecodeParityState(t *testing.T) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForFrameSize(t, 960)
}

func prepareExplicitDREDDecodeParityStateForFrameSize(t *testing.T, frameSize int) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityState16k(t *testing.T) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize: 480,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityState16kForFrameSize(t *testing.T, frameSize int) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t *testing.T, decoderSampleRate int, packetCfg libopusDREDPacketConfig) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	t.Helper()

	packetInfo, err := emitLibopusDREDPacketWithConfig(packetCfg)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	if channels < 1 || channels > 2 {
		t.Skipf("explicit DRED decode parity requires mono or stereo packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}
	if packetCfg.ForceMode != toc.Mode {
		t.Skipf("explicit DRED decode parity requires mode=%v packet, got mode=%v", packetCfg.ForceMode, toc.Mode)
	}
	if packetCfg.Bandwidth != toc.Bandwidth {
		t.Skipf("explicit DRED decode parity requires bandwidth=%v packet, got bandwidth=%v", packetCfg.Bandwidth, toc.Bandwidth)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	seedPacket := makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t, toc.Mode, toc.Bandwidth, toc.FrameSize)

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	seedPCM := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(seedPacket, seedPCM)
	if err != nil {
		t.Fatalf("Decode(seed packet) error: %v", err)
	}
	if n <= 0 {
		t.Skip("carrier packet returned no audio")
	}

	standalone := NewDREDDecoder()
	if err := standalone.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("standalone SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	maxDRED, parseRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	if _, _, err := standalone.Parse(dred, packetInfo.packet, maxDRED, parseRate, true); err != nil {
		t.Fatalf("standalone Parse error: %v", err)
	}
	if err := standalone.Process(dred, dred); err != nil {
		t.Fatalf("standalone Process error: %v", err)
	}
	if !dred.Processed() {
		t.Fatal("standalone DRED did not reach processed state")
	}
	return dec, dred, packetInfo, seedPacket, n
}

func prepareCachedDREDDecodeParityStateForPacket(t *testing.T, packetInfo libopusDREDPacket) (*Decoder, int) {
	t.Helper()
	return prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 1)
}

func prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket) (*Decoder, int) {
	t.Helper()
	return prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, 1)
}

func prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket, wantChannels int) (*Decoder, int) {
	t.Helper()

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}

	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	if wantChannels > 0 && channels != wantChannels {
		t.Skipf("cached DRED decode parity requires %d-channel packet, got sampleRate=%d channels=%d", wantChannels, packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("Decode(DRED packet) error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode(DRED packet) returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("Decode(DRED packet) did not retain processed DRED state")
	}
	return dec, n
}

// parseCarrierDREDForExplicitDecode parses+processes a standalone *DRED from the
// carrier packet so cached-DRED parity tests can drive the explicit DRED-decode
// path (decodeExplicitDREDFloat), which is the libopus-conformant
// opus_decoder_dred_decode equivalent the SourceCarrierDRED oracle exercises
// (tools/csrc/libopus_decoder_dred_sequence_info.c case 3 calls
// opus_decoder_dred_decode_float). A public Decode(nil) must NOT auto-apply
// cached DRED (opus_decoder.c:736 gates the FEC feed on dred!=NULL); mirroring
// cc04ecf0's SILK reconciliation, recovery is verified through the explicit
// entry point instead.
func parseCarrierDREDForExplicitDecode(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket) *DRED {
	t.Helper()
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	standalone := NewDREDDecoder()
	if err := standalone.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("standalone SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	maxDRED, parseRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	if _, _, err := standalone.Parse(dred, packetInfo.packet, maxDRED, parseRate, true); err != nil {
		t.Fatalf("standalone Parse error: %v", err)
	}
	if err := standalone.Process(dred, dred); err != nil {
		t.Fatalf("standalone Process error: %v", err)
	}
	if !dred.Processed() {
		t.Fatal("standalone DRED did not reach processed state")
	}
	return dred
}

// decodeCachedCarrierDREDViaExplicit drives the explicit DRED-decode path with a
// standalone *DRED parsed from the carrier packet, recovering one lost frame at
// the given decoder-rate dred offset. This replaces the removed auto-on-loss
// cached-DRED application (a libopus feature opus_decode lacks); the explicit
// path matches the SourceCarrierDRED oracle (opus_decoder_dred_decode_float).
func decodeCachedCarrierDREDViaExplicit(t *testing.T, dec *Decoder, dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) int {
	t.Helper()
	got, err := dec.decodeExplicitDREDFloat(dred, dredOffsetSamples, pcm, frameSizeSamples)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	return got
}

func assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t *testing.T, label string, packetInfo libopusDREDPacket, pcmTol, plcTol, farganTol, celtTol float64) {
	t.Helper()

	dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cached first-loss")
	if want.step0.ret != n {
		t.Fatalf("%s libopus cached decoder first-loss ret=%d want %d", label, want.step0.ret, n)
	}

	// Explicit DRED-decode path (SourceCarrierDRED oracle). A public Decode(nil)
	// would run plain PLC and consume no cached DRED (opus_decoder.c:736).
	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("%s explicit DRED decode=%d want %d", label, got, n)
	}

	assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), label+" first-loss live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, label+" first-loss live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, label+" first-loss live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, label+" first-loss live-sequence celt", celtTol)
}

func assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t *testing.T, label string, packetInfo libopusDREDPacket, pcmTol, plcTol, farganTol, celtTol float64) {
	t.Helper()

	dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cached second-loss")
	if want.step0.ret != n {
		t.Fatalf("%s libopus cached decoder first warmup ret=%d want %d", label, want.step0.ret, n)
	}
	if want.step1.ret != n {
		t.Fatalf("%s libopus cached decoder second-loss ret=%d want %d", label, want.step1.ret, n)
	}

	// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736).
	pcm0 := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
	if got != n {
		t.Fatalf("%s explicit DRED decode(first)=%d want %d", label, got, n)
	}
	assertDecodedPCMQuality(t, pcm0[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), label+" warmup live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, label+" warmup live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, label+" warmup live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, label+" warmup live-sequence celt", celtTol)

	pcm1 := make([]float32, n*dec.Channels())
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != n {
		t.Fatalf("%s explicit DRED decode(second)=%d want %d", label, got, n)
	}
	assertDecodedPCMQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), label+" second-loss live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, label+" second-loss live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, label+" second-loss live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, label+" second-loss live-sequence celt", celtTol)
}

func decoderDREDLiveSequenceTolerances(frameSize int) (pcmTol, plcTol, farganTol, celtTol float64) {
	pcmTol, plcTol, farganTol, celtTol = 1e-4, 1e-4, 1e-4, 1e-4
	if frameSize >= 480 {
		pcmTol, plcTol, farganTol, celtTol = 1e-2, 1e-1, 2.5e-1, 3e-2
	}
	if frameSize >= 960 {
		// A 20 ms 48 kHz loss synthesizes three 16 kHz FARGAN frames.
		// Keep the sensitive recurrent state envelope, but pin PLC history tighter.
		return 5e-3, 6e-3, 9e-2, 5e-3
	}
	return pcmTol, plcTol, farganTol, celtTol
}

func assertInterleavedStereoDuplicated(t *testing.T, pcm []float32, samples int, label string) {
	t.Helper()
	if len(pcm) < samples*2 {
		t.Fatalf("%s PCM length=%d too short for %d stereo samples", label, len(pcm), samples)
	}
	for i := 0; i < samples; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("%s PCM not L=R duplicated at sample %d: |L-R|=%g", label, i, d)
		}
	}
}

func assertInterleavedStereoApproxDuplicated(t *testing.T, pcm []float32, samples int, label string, tol float64) {
	t.Helper()
	if len(pcm) < samples*2 {
		t.Fatalf("%s PCM length=%d too short for %d stereo samples", label, len(pcm), samples)
	}
	var maxDrift float64
	for i := 0; i < samples; i++ {
		d := math.Abs(float64(pcm[2*i] - pcm[2*i+1]))
		if d > maxDrift {
			maxDrift = d
		}
		if d > tol {
			t.Fatalf("%s PCM not L=R duplicated at sample %d: |L-R|=%g max=%g tol=%g", label, i, d, maxDrift, tol)
		}
	}
}

type cachedStereoDREDLiveFlow int

const (
	cachedStereoDREDFirstLoss cachedStereoDREDLiveFlow = iota
	cachedStereoDREDSecondLoss
	cachedStereoDREDFirstLossThenNext
	cachedStereoDREDSecondLossThenNext
)

func (f cachedStereoDREDLiveFlow) name() string {
	switch f {
	case cachedStereoDREDFirstLoss:
		return "first_loss"
	case cachedStereoDREDSecondLoss:
		return "second_loss"
	case cachedStereoDREDFirstLossThenNext:
		return "first_loss_then_next"
	case cachedStereoDREDSecondLossThenNext:
		return "second_loss_then_next"
	default:
		return "unknown"
	}
}

func assertDecoderCachedStereoDREDLiveSequenceMatchesLibopus(t *testing.T, label string, packetCfg libopusDREDPacketConfig, nextPacket []byte, flow cachedStereoDREDLiveFlow) {
	t.Helper()
	packetCfg.Channels = 2
	packetCfg.ForceChannels = 2
	packetInfo, err := emitLibopusDREDPacketWithConfig(packetCfg)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if !toc.Stereo || toc.Mode != packetCfg.ForceMode || toc.Bandwidth != packetCfg.Bandwidth || toc.FrameSize != packetCfg.FrameSize {
		t.Fatalf("%s forced stereo %v/%v/%d packet mismatch: stereo=%t mode=%v bandwidth=%v frame=%d", label, packetCfg.ForceMode, packetCfg.Bandwidth, packetCfg.FrameSize, toc.Stereo, toc.Mode, toc.Bandwidth, toc.FrameSize)
	}

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != packetCfg.FrameSize {
		t.Skipf("%s cached stereo live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", label, packetCfg.FrameSize, packetInfo.sampleRate, n)
	}

	step1Source := 0
	decodeNext := false
	switch flow {
	case cachedStereoDREDSecondLoss:
		step1Source = libopusDecoderDREDSequenceSourceCarrierDRED
	case cachedStereoDREDFirstLossThenNext:
		decodeNext = true
	case cachedStereoDREDSecondLossThenNext:
		step1Source = libopusDecoderDREDSequenceSourceCarrierDRED
		decodeNext = true
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, step1Source, 2*n, decodeNext)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cached stereo first-loss")
	if want.channels != 2 {
		t.Fatalf("%s libopus cached stereo DRED channels=%d want 2", label, want.channels)
	}
	if want.step0.ret != n {
		t.Fatalf("%s libopus cached stereo first-loss ret=%d want %d", label, want.step0.ret, n)
	}
	if step1Source != 0 && want.step1.ret != n {
		t.Fatalf("%s libopus cached stereo second-loss ret=%d want %d", label, want.step1.ret, n)
	}
	if decodeNext && want.next.ret <= 0 {
		t.Fatalf("%s libopus cached stereo follow-up ret=%d want >0", label, want.next.ret)
	}

	const stereoStateTol = 3e-3
	const stereoCELTTol = 3e-3
	const duplicateTol = 1e-2

	pcm0 := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoApproxDuplicated(t, pcm0, got, label+" first loss", duplicateTol)
	assertInterleavedStereoApproxDuplicated(t, want.step0.pcm, got, label+" libopus first loss", duplicateTol)

	comparePCM := pcm0[:got*dec.Channels()]
	compareSamples := got
	compareState := want.step0
	compareLabel := label + " first-loss"

	if step1Source != 0 {
		pcm1 := make([]float32, n*dec.Channels())
		got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
		if got != n {
			t.Fatalf("explicit DRED decode=%d want %d", got, n)
		}
		assertInterleavedStereoApproxDuplicated(t, pcm1, got, label+" second loss", duplicateTol)
		assertInterleavedStereoApproxDuplicated(t, want.step1.pcm, got, label+" libopus second loss", duplicateTol)
		comparePCM = pcm1[:got*dec.Channels()]
		compareSamples = got
		compareState = want.step1
		compareLabel = label + " second-loss"
	}

	if decodeNext {
		nextPCM := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		gotNext, err := dec.Decode(nextPacket, nextPCM)
		if err != nil {
			t.Fatalf("%s Decode(next packet) error: %v", label, err)
		}
		if gotNext != want.next.ret {
			t.Fatalf("%s Decode(next packet)=%d want %d", label, gotNext, want.next.ret)
		}
		comparePCM = nextPCM[:gotNext*dec.Channels()]
		compareSamples = gotNext
		compareState = want.next
		compareLabel = label + " next-packet"
	}

	assertDecodedPCMQuality(t, comparePCM, compareState.pcm[:compareSamples*dec.Channels()], packetInfo.sampleRate, dec.Channels(), compareLabel+" live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), compareState.state, compareLabel+" live-sequence plc", stereoStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), compareState.fargan, compareLabel+" live-sequence fargan", stereoStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, compareState.celt48k, compareLabel+" live-sequence celt", stereoCELTTol)
}

type hybridDREDAPIRateCase struct {
	name       string
	sampleRate int
	bandwidth  Bandwidth
	frameSize  int
}

func hybridDREDAPIRateCases() []hybridDREDAPIRateCase {
	return []hybridDREDAPIRateCase{
		{name: "8k_swb_10ms", sampleRate: 8000, bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "8k_fb_20ms", sampleRate: 8000, bandwidth: BandwidthFullband, frameSize: 960},
		{name: "12k_swb_10ms", sampleRate: 12000, bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "12k_fb_20ms", sampleRate: 12000, bandwidth: BandwidthFullband, frameSize: 960},
		{name: "24k_swb_10ms", sampleRate: 24000, bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "24k_fb_20ms", sampleRate: 24000, bandwidth: BandwidthFullband, frameSize: 960},
	}
}

func cachedHybridLiveSequenceTolerances(_ Bandwidth, frameSize int) (pcmTol, plcTol, farganTol, celtTol float64) {
	pcmTol, plcTol, farganTol, celtTol = decoderDREDLiveSequenceTolerances(frameSize)
	return pcmTol, plcTol, farganTol, celtTol
}
