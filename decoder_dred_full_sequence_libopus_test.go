//go:build gopus_dred || gopus_osce

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The selected libopus decoder and gopus consume the same 220 encoded packets.
// The final undelivered packet has no recovery carrier and uses ordinary PLC.
// This gate checks all 220 time slots: received and concealed PCM, return
// lengths, and final ranges in playback order.
func TestDREDLongSequenceAllDecodedPCMMatchesLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredDecoderBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	goDecoderBlob := append(append([]byte(nil), decoderBlob...), dredDecoderBlob...)
	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	got := decodeDREDQualityPacketsWithTrailingPLC(t, packets, reference, goDecoderBlob, true, true)
	if len(packets) != 220 || len(got.frameIndices) != 220 || got.lossFrames != 120 {
		t.Fatalf("sequence shape packets=%d decoded=%d lost=%d want 220/220/120", len(packets), len(got.frameIndices), got.lossFrames)
	}

	binPath, err := getLibopusDREDQualitySequenceHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED quality sequence", err)
	}
	payload := libopustest.NewOraclePayloadVersion(libopusDREDQualitySequenceInputMagic, 2,
		dredQualitySampleRate, dredQualityChannels, dredQualityFrameSize,
		uint32(len(packets)), 1, uint32(len(decoderBlob)), uint32(len(dredDecoderBlob)))
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
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(),
		"DRED complete decode sequence", libopusDREDQualitySequenceOutputMagic, 2)
	if err != nil {
		t.Fatalf("run libopus DRED complete sequence: %v", err)
	}
	lossFrames, dredFrames, fallbackFrames := int(reader.I32()), int(reader.I32()), int(reader.I32())
	channels, sampleRate, frameSize, lossSamples := int(reader.I32()), int(reader.I32()), int(reader.I32()), int(reader.I32())
	if err := reader.Err(); err != nil {
		t.Fatalf("read libopus sequence header: %v", err)
	}
	if lossFrames != got.lossFrames || dredFrames != got.dredFrames || fallbackFrames != got.fallbackFrames ||
		channels != dredQualityChannels || sampleRate != dredQualitySampleRate || frameSize != dredQualityFrameSize ||
		lossSamples != 120*dredQualityFrameSize*dredQualityChannels {
		t.Fatalf("libopus sequence header=(loss=%d DRED=%d fallback=%d channels=%d rate=%d frame=%d samples=%d) Go=(%d,%d,%d)",
			lossFrames, dredFrames, fallbackFrames, channels, sampleRate, frameSize, lossSamples,
			got.lossFrames, got.dredFrames, got.fallbackFrames)
	}
	_ = reader.Bytes(lossSamples * 4) // v1 loss splice, followed by v2 frame records.
	if count := reader.Count(220); count != 220 {
		t.Fatalf("libopus frame record count=%d: %v", count, reader.Err())
	}
	offset := 0
	received, lost := 0, 0
	for i := 0; i < 220; i++ {
		frame := int(reader.U32())
		kind := reader.U32()
		samples := int(reader.I32())
		finalRange := reader.U32()
		if err := reader.Err(); err != nil {
			t.Fatalf("read libopus record %d: %v", i, err)
		}
		if frame != i || frame != got.frameIndices[i] || kind != got.frameKinds[i] ||
			samples != got.frameSamples[i] || samples != dredQualityFrameSize*dredQualityChannels ||
			finalRange != got.frameRanges[i] {
			t.Fatalf("record %d C=(frame=%d kind=%d samples=%d range=%08x) Go=(frame=%d kind=%d samples=%d range=%08x)",
				i, frame, kind, samples, finalRange, got.frameIndices[i], got.frameKinds[i], got.frameSamples[i], got.frameRanges[i])
		}
		if delivered := dredQualityPacketDelivered(i); delivered != (kind == 0) {
			t.Fatalf("frame=%d delivery=%v kind=%d", i, delivered, kind)
		}
		if kind == 0 {
			received++
		} else {
			lost++
		}
		if i == 219 && kind != 2 {
			t.Fatalf("trailing frame kind=%d want ordinary PLC", kind)
		}
		for sample := 0; sample < samples; sample++ {
			want := math.Float32bits(reader.Float32())
			if err := reader.Err(); err != nil {
				t.Fatalf("read frame=%d sample=%d: %v", frame, sample, err)
			}
			gotBits := math.Float32bits(got.decoded[offset+sample])
			if gotBits != want {
				t.Fatalf("record=%d frame=%d kind=%d sample=%d Go=%08x C=%08x range=%08x",
					i, frame, kind, sample, gotBits, want, finalRange)
			}
		}
		offset += samples
	}
	if offset != len(got.decoded) {
		t.Fatalf("compared samples=%d Go=%d", offset, len(got.decoded))
	}
	if received != 100 || lost != 120 {
		t.Fatalf("decoded kinds received=%d lost=%d want 100/120", received, lost)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("libopus sequence trailing output: %v", err)
	}
}
