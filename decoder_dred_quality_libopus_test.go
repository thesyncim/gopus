//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
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
				start := originalFrame * dredQualityFrameSize * dredQualityChannels
				end := start + dredQualityFrameSize*dredQualityChannels
				if start < 0 || end > len(reference) {
					t.Fatalf("loss reference frame=%d outside reference", originalFrame)
				}
				lossReference = append(lossReference, reference[start:end]...)
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
