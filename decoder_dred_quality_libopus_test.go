//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package gopus

import (
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDREDQualitySequenceInputMagic  = "GDQI"
	libopusDREDQualitySequenceOutputMagic = "GDQO"
)

var (
	libopusDREDQualitySequenceHelperOnce sync.Once
	libopusDREDQualitySequenceHelperPath string
	libopusDREDQualitySequenceHelperErr  error
)

func TestExplicitDREDQualityTracksLibopusAtSixtyPercentLoss(t *testing.T) {
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

	if libDREDMetrics.Envelope < libPLCMetrics.Envelope+0.010 {
		t.Fatalf("libopus DRED envelope quality did not improve enough: dred=%.5f plc=%.5f", libDREDMetrics.Envelope, libPLCMetrics.Envelope)
	}
	if goDREDMetrics.Envelope+0.015 < libDREDMetrics.Envelope {
		t.Fatalf("Go DRED envelope quality too far below libopus: go=%.5f libopus=%.5f", goDREDMetrics.Envelope, libDREDMetrics.Envelope)
	}
	if goVsLib.SNRDB < 20.0 {
		t.Fatalf("Go DRED PCM SNR diverges from libopus: %.3f dB", goVsLib.SNRDB)
	}
	if goVsLib.Correlation < 0.995 {
		t.Fatalf("Go DRED PCM correlation diverges from libopus: %.5f", goVsLib.Correlation)
	}
	if goVsLib.Envelope < 0.990 {
		t.Fatalf("Go DRED PCM envelope diverges from libopus: %.5f", goVsLib.Envelope)
	}
	if goDREDMetrics.OpusQOK && libDREDMetrics.OpusQOK && goDREDMetrics.OpusQ+20.0 < libDREDMetrics.OpusQ {
		t.Fatalf("Go DRED opus_compare quality too far below libopus: go=%.3f libopus=%.3f", goDREDMetrics.OpusQ, libDREDMetrics.OpusQ)
	}
}

func getLibopusDREDQualitySequenceHelperPath() (string, error) {
	libopusDREDQualitySequenceHelperOnce.Do(func() {
		libopusDREDQualitySequenceHelperPath, libopusDREDQualitySequenceHelperErr = buildLibopusDREDHelper("libopus_decoder_dred_quality_sequence.c", "gopus_libopus_decoder_dred_quality_sequence", true)
	})
	if libopusDREDQualitySequenceHelperErr != nil {
		return "", libopusDREDQualitySequenceHelperErr
	}
	return libopusDREDQualitySequenceHelperPath, nil
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
