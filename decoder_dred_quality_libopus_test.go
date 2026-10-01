//go:build gopus_dred || gopus_osce

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
)

const (
	libopusDREDQualitySequenceInputMagic  = "GDQI"
	libopusDREDQualitySequenceOutputMagic = "GDQO"
)

var libopusDREDQualitySequenceHelper libopustest.HelperCache

func TestExplicitDREDQualityTracksLibopusAtSixtyPercentLoss(t *testing.T) {
	requireDREDAudioQualityGate(t)
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredDecoderBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	goDecoderBlob := append(append([]byte(nil), decoderBlob...), dredDecoderBlob...)

	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	goPLC := decodeDREDQualityPackets(t, packets, reference, goDecoderBlob, false)
	goDRED := decodeDREDQualityPackets(t, packets, reference, goDecoderBlob, true)
	libPLC := decodeLibopusDREDQualityPackets(t, packets, reference, decoderBlob, dredDecoderBlob, false)
	libDRED := decodeLibopusDREDQualityPackets(t, packets, reference, decoderBlob, dredDecoderBlob, true)
	zeroOffsetReference := dredQualityLossReferenceAtOffset(t, reference, len(packets), 0)
	t.Logf("uncompensated zero-offset envelope diagnostic: Go PLC=%.5f Go DRED=%.5f C PLC=%.5f C DRED=%.5f",
		dredQualityEnvelope(zeroOffsetReference, goPLC.lossDecoded, dredQualitySampleRate),
		dredQualityEnvelope(zeroOffsetReference, goDRED.lossDecoded, dredQualitySampleRate),
		dredQualityEnvelope(zeroOffsetReference, libPLC.lossDecoded, dredQualitySampleRate),
		dredQualityEnvelope(zeroOffsetReference, libDRED.lossDecoded, dredQualitySampleRate))

	if goDRED.lossFrames != libDRED.lossFrames {
		t.Fatalf("loss frame count mismatch: go=%d libopus=%d", goDRED.lossFrames, libDRED.lossFrames)
	}
	if goDRED.dredFrames != libDRED.dredFrames {
		t.Fatalf("DRED frame count mismatch: go=%d libopus=%d", goDRED.dredFrames, libDRED.dredFrames)
	}
	if libDRED.dredFrames == 0 {
		t.Fatal("libopus DRED did not recover any lost frames")
	}

	goPLCMetrics := measureDREDQuality(t, goPLC.lossReference, goPLC.lossDecoded)
	goDREDMetrics := measureDREDQuality(t, goDRED.lossReference, goDRED.lossDecoded)
	libPLCMetrics := measureDREDQuality(t, libPLC.lossReference, libPLC.lossDecoded)
	libDREDMetrics := measureDREDQuality(t, libDRED.lossReference, libDRED.lossDecoded)
	goVsLib := measureDREDQuality(t, libDRED.lossDecoded, goDRED.lossDecoded)

	t.Logf("Go PLC loss quality:      snr=%.3f dB corr=%.5f env=%.5f opusQ=%s",
		goPLCMetrics.SNRDB, goPLCMetrics.Correlation, goPLCMetrics.Envelope, formatOptionalQuality(goPLCMetrics))
	t.Logf("Go DRED loss quality:     snr=%.3f dB corr=%.5f env=%.5f opusQ=%s recovered=%d fallback=%d",
		goDREDMetrics.SNRDB, goDREDMetrics.Correlation, goDREDMetrics.Envelope, formatOptionalQuality(goDREDMetrics),
		goDRED.dredFrames, goDRED.fallbackFrames)
	t.Logf("libopus PLC loss quality: snr=%.3f dB corr=%.5f env=%.5f opusQ=%s",
		libPLCMetrics.SNRDB, libPLCMetrics.Correlation, libPLCMetrics.Envelope, formatOptionalQuality(libPLCMetrics))
	t.Logf("libopus DRED loss quality: snr=%.3f dB corr=%.5f env=%.5f opusQ=%s recovered=%d fallback=%d",
		libDREDMetrics.SNRDB, libDREDMetrics.Correlation, libDREDMetrics.Envelope, formatOptionalQuality(libDREDMetrics),
		libDRED.dredFrames, libDRED.fallbackFrames)
	t.Logf("Go-vs-libopus DRED PCM:   snr=%.3f dB corr=%.5f env=%.5f opusQ=%s",
		goVsLib.SNRDB, goVsLib.Correlation, goVsLib.Envelope, formatOptionalQuality(goVsLib))
	t.Logf("Go-libopus DRED delta:    snr=%+.3f dB corr=%+.5f env=%+.5f opusQ=%s",
		goDREDMetrics.SNRDB-libDREDMetrics.SNRDB,
		goDREDMetrics.Correlation-libDREDMetrics.Correlation,
		goDREDMetrics.Envelope-libDREDMetrics.Envelope,
		formatOptionalQualityDelta(goDREDMetrics, libDREDMetrics))

	// Structural sanity (kept): libopus's own DRED must measurably improve the
	// concealed-frame envelope over its PLC fallback, otherwise the loss pattern
	// is not exercising DRED at all.
	if libDREDMetrics.Envelope < libPLCMetrics.Envelope+0.010 {
		t.Fatalf("libopus DRED envelope quality did not improve enough: dred=%.5f plc=%.5f", libDREDMetrics.Envelope, libPLCMetrics.Envelope)
	}

	// Migrated Go-vs-libopus DRED PCM parity gate. The reference is libopus's own
	// DRED-concealed PCM over the lost frames and the candidate is gopus's; we use
	// the canonical comparator (CompareDecodedFloat32) so the trusted bar and its
	// libopus-anchored rationale live in internal/qualitycompare rather than in
	// hand-picked per-test numbers (which is what the removed SNR>=20 / corr>=0.995
	// / env>=0.990 / opusQ-within-20 block was).
	//
	// The frame-count oracles above already pin the concealment structure to be
	// bit-identical (same lost/DRED/fallback frame counts), so this gate scores
	// only the decoded-audio agreement of the two DRED concealments.
	//
	// IMPORTANT: the bytes compared here are the LOSS-FRAME-ONLY splice
	// (run.lossDecoded), i.e. non-contiguous concealed frames concatenated. The
	// libopus oracle helper (tools/csrc/libopus_decoder_dred_quality_sequence.c)
	// emits only this splice, never the full continuous stream, so a contiguous
	// comparison is not available. opus_compare's perceptual Q is meaningless on
	// such a splice -- as proof, even libopus-DRED-vs-clean-source scores Q~=-730
	// on the same splice (see the diagnostic logs above). We therefore gate on the
	// canonical comparator's waveform corr/RMS (the metric the task prescribes when
	// opus_compare Q is not applicable) and log Q only as a diagnostic.
	maxDelay := 4 * dredQualityFrameSize
	if maxDelay < 960 {
		maxDelay = 960
	}
	cmp, err := qualitycompare.CompareDecodedFloat32(goDRED.lossDecoded, libDRED.lossDecoded, dredQualitySampleRate, dredQualityChannels, maxDelay)
	if err != nil {
		t.Fatalf("CompareDecodedFloat32(go-vs-libopus DRED): %v", err)
	}
	// Bar basis: anchored to the canonical near-exact bar that SILK/CELT/Hybrid
	// decode meet vs libopus, but with MinQ disabled because opus_compare Q is not
	// valid on the spliced loss-only stream (above). A bit-exact-routed concealment
	// whose only divergence is a transcendental/libm rounding tail would land at
	// corr>=0.997; the waveform corr is the honest parity metric here.
	dredPCMBar := qualitycompare.QualityBar{
		MinQ:    0,
		MinCorr: qualitycompare.QualityBarNearExact.MinCorr,
		RMSLo:   qualitycompare.QualityBarNearExact.RMSLo,
		RMSHi:   qualitycompare.QualityBarNearExact.RMSHi,
		Desc:    "near-exact vs libopus (corr/RMS; Q invalid on loss-only splice)",
	}
	qualitycompare.AssertQuality(t, cmp, dredPCMBar, "Go-vs-libopus DRED concealed PCM")
}

func TestDREDLowDelayReferenceOffsetAgainstLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	lookahead, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
		SampleRate:  dredQualitySampleRate,
		Channels:    dredQualityChannels,
		Application: 2051, // OPUS_APPLICATION_RESTRICTED_LOWDELAY
		FrameSize:   dredQualityFrameSize,
		Ops: []libopustest.CTLOp{{
			Op:      libopustest.CTLOpGet,
			Request: 4027, // OPUS_GET_LOOKAHEAD_REQUEST
		}},
	})
	if err != nil {
		t.Fatalf("query C OPUS_GET_LOOKAHEAD: %v", err)
	}
	if len(lookahead) != 1 || lookahead[0].Ret != 0 || !lookahead[0].HaveValue ||
		lookahead[0].Value != dredQualitySampleRate/400 {
		t.Fatalf("C OPUS_GET_LOOKAHEAD result=%+v want %d", lookahead, dredQualitySampleRate/400)
	}
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	decoded := decodeLibopusAllDeliveredQualityPackets(t, packets)
	if len(reference) != len(decoded) {
		t.Fatalf("clean sequence samples: reference=%d C=%d", len(reference), len(decoded))
	}

	const search = 240
	bestOffset, bestCorr := dredQualityBestReferenceOffset(reference, decoded, search)
	corrAtZero := dredQualityReferenceOffsetCorrelation(reference, decoded, 0, search)
	corrAtLookahead := dredQualityReferenceOffsetCorrelation(reference, decoded, int(lookahead[0].Value), search)
	t.Logf("restricted-low-delay C decode timing: lookahead=%d best-source-offset=%d corr=%.6f zero-offset-corr=%.6f lookahead-offset-corr=%.6f",
		lookahead[0].Value, bestOffset, bestCorr, corrAtZero, corrAtLookahead)
	if bestOffset != int(lookahead[0].Value) {
		t.Fatalf("C no-loss decode aligns at source offset %d, want OPUS_GET_LOOKAHEAD=%d", bestOffset, lookahead[0].Value)
	}
	if corrAtLookahead < corrAtZero+0.05 {
		t.Fatalf("lookahead alignment improvement too small: zero=%.6f lookahead=%.6f", corrAtZero, corrAtLookahead)
	}
}

func decodeLibopusAllDeliveredQualityPackets(t *testing.T, packets [][]byte) []float32 {
	t.Helper()
	binPath, err := getLibopusDREDQualitySequenceHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED quality sequence", err)
	}
	payload := libopustest.NewOraclePayloadVersion(libopusDREDQualitySequenceInputMagic, 2,
		uint32(dredQualitySampleRate), uint32(dredQualityChannels), uint32(dredQualityFrameSize),
		uint32(len(packets)), 0, 0, 0)
	for _, packet := range packets {
		payload.U32s(1, uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(),
		"libopus clean DRED timing sequence", libopusDREDQualitySequenceOutputMagic, 2)
	if err != nil {
		t.Fatalf("run libopus clean DRED timing sequence: %v", err)
	}
	lossFrames, dredFrames, fallbackFrames := reader.I32(), reader.I32(), reader.I32()
	channels, sampleRate, frameSize, lossSamples := reader.I32(), reader.I32(), reader.I32(), reader.I32()
	if err := reader.Err(); err != nil {
		t.Fatalf("read clean sequence header: %v", err)
	}
	if lossFrames != 0 || dredFrames != 0 || fallbackFrames != 0 || channels != dredQualityChannels ||
		sampleRate != dredQualitySampleRate || frameSize != dredQualityFrameSize || lossSamples != 0 {
		t.Fatalf("unexpected clean sequence header: loss=%d DRED=%d fallback=%d shape=%d/%d/%d samples=%d",
			lossFrames, dredFrames, fallbackFrames, channels, sampleRate, frameSize, lossSamples)
	}
	if reader.Count(len(packets)) != len(packets) {
		t.Fatalf("clean sequence record count: %v", reader.Err())
	}
	decoded := make([]float32, 0, len(packets)*dredQualityFrameSize*dredQualityChannels)
	for frame := range packets {
		gotFrame, kind, samples := reader.U32(), reader.U32(), reader.I32()
		_ = reader.U32() // final range
		if err := reader.Err(); err != nil {
			t.Fatalf("read clean frame %d header: %v", frame, err)
		}
		if gotFrame != uint32(frame) || kind != 0 || samples != dredQualityFrameSize*dredQualityChannels {
			t.Fatalf("clean frame %d record=(frame=%d kind=%d samples=%d)", frame, gotFrame, kind, samples)
		}
		for i := 0; i < int(samples); i++ {
			decoded = append(decoded, reader.Float32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("clean sequence output: %v", err)
	}
	return decoded
}

func dredQualityBestReferenceOffset(reference, decoded []float32, maxOffset int) (int, float64) {
	bestOffset, bestCorr := -1, math.Inf(-1)
	for offset := 0; offset <= maxOffset; offset++ {
		corr := dredQualityReferenceOffsetCorrelation(reference, decoded, offset, maxOffset)
		if corr > bestCorr {
			bestOffset, bestCorr = offset, corr
		}
	}
	return bestOffset, bestCorr
}

func dredQualityReferenceOffsetCorrelation(reference, decoded []float32, offset, maxOffset int) float64 {
	start := 20*dredQualityFrameSize + maxOffset
	if start < offset {
		start = offset
	}
	end := len(decoded)
	if end > len(reference)+offset {
		end = len(reference) + offset
	}
	return dredQualityCorrelation(reference[start-offset:end-offset], decoded[start:end])
}

func getLibopusDREDQualitySequenceHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDQualitySequenceHelper, "libopus_decoder_dred_quality_sequence.c", "gopus_libopus_decoder_dred_quality_sequence", true)
}

func decodeLibopusDREDQualityPackets(t *testing.T, packets [][]byte, reference []float32, decoderBlob, dredDecoderBlob []byte, useDRED bool) dredQualityRun {
	t.Helper()

	binPath, err := getLibopusDREDQualitySequenceHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED quality sequence", err)
	}

	useDREDFlag := uint32(0)
	if useDRED {
		useDREDFlag = 1
	}
	payload := libopustest.NewOraclePayload(libopusDREDQualitySequenceInputMagic,
		dredQualitySampleRate,
		dredQualityChannels,
		dredQualityFrameSize,
		uint32(len(packets)),
		useDREDFlag,
		uint32(len(decoderBlob)),
		uint32(len(dredDecoderBlob)),
	)
	payload.Raw(decoderBlob)
	payload.Raw(dredDecoderBlob)
	for frame, packet := range packets {
		delivered := uint32(0)
		if dredQualityPacketDelivered(frame) {
			delivered = 1
		}
		payload.U32s(delivered, uint32(len(packet)))
		payload.Raw(packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "DRED quality sequence", libopusDREDQualitySequenceOutputMagic)
	if err != nil {
		t.Fatalf("run libopus DRED quality sequence helper: %v", err)
	}

	run := dredQualityRun{
		lossFrames:     int(reader.I32()),
		dredFrames:     int(reader.I32()),
		fallbackFrames: int(reader.I32()),
	}
	channels := int(reader.I32())
	sampleRate := int(reader.I32())
	frameSize := int(reader.I32())
	sampleCount := int(reader.I32())
	if err := reader.Err(); err != nil {
		t.Fatalf("read libopus quality helper header: %v", err)
	}
	if channels != dredQualityChannels || sampleRate != dredQualitySampleRate || frameSize != dredQualityFrameSize {
		t.Fatalf("libopus helper shape=(channels=%d sampleRate=%d frameSize=%d) want (%d,%d,%d)",
			channels, sampleRate, frameSize, dredQualityChannels, dredQualitySampleRate, dredQualityFrameSize)
	}
	if sampleCount != run.lossFrames*dredQualityFrameSize*dredQualityChannels {
		t.Fatalf("libopus helper sample count=%d want %d", sampleCount, run.lossFrames*dredQualityFrameSize*dredQualityChannels)
	}
	run.lossDecoded = make([]float32, sampleCount)
	for i := range run.lossDecoded {
		run.lossDecoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("read libopus quality helper PCM: %v", err)
	}
	run.lossReference = dredQualityLossReference(t, reference, len(packets))
	if len(run.lossReference) != len(run.lossDecoded) {
		t.Fatalf("libopus loss reference samples=%d decoded=%d", len(run.lossReference), len(run.lossDecoded))
	}
	return run
}

func dredQualityLossReference(t *testing.T, reference []float32, frames int) []float32 {
	return dredQualityLossReferenceAtOffset(t, reference, frames, dredQualityLookaheadSamples)
}

func dredQualityLossReferenceAtOffset(t *testing.T, reference []float32, frames, offset int) []float32 {
	t.Helper()
	var lossReference []float32
	expected := 0
	haveExpected := false
	for frame := 0; frame < frames; frame++ {
		if !dredQualityPacketDelivered(frame) {
			continue
		}
		if haveExpected {
			missing := frame - expected
			for lostAgo := missing; lostAgo >= 1; lostAgo-- {
				originalFrame := frame - lostAgo
				frameReference, ok := dredQualityFrameReferenceWithOffset(reference, originalFrame, offset)
				if !ok {
					t.Fatalf("loss reference frame=%d outside reference", originalFrame)
				}
				lossReference = append(lossReference, frameReference...)
			}
		}
		expected = frame + 1
		haveExpected = true
	}
	return lossReference
}

func formatOptionalQualityDelta(a, b dredQualityMetrics) string {
	if !a.OpusQOK || !b.OpusQOK {
		return "unavailable"
	}
	return fmt.Sprintf("%+.3f", a.OpusQ-b.OpusQ)
}
