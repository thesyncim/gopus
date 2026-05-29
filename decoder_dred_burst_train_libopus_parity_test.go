//go:build gopus_dred || gopus_extra_controls

package gopus

// Long burst train DRED recovery parity.
//
// Verifies that gopus explicit-DRED recovery tracks libopus
// opus_decoder_dred_decode_float across N consecutive losses (a "burst train").
// The libopus oracle is the quality-sequence helper (GDQI/GDQO) which runs the
// same loop libopus uses in application code:
//
//	for lostAgo = missing; lostAgo >= 1; lostAgo-- {
//	    opus_decoder_dred_decode_float(dec, dred, lostAgo*frameSize, pcm, frameSize)
//	}
//	opus_decode_float(dec, nextPacket, ..., pcm, frameSize, 0)
//
// Reference: opus_decoder.c (libopus 1.6.1) opus_decoder_dred_decode_float,
// tools/csrc/libopus_decoder_dred_quality_sequence.c.
//
// Approach:
//   - Encode a short CELT sequence with DRED, drop N consecutive frames in the
//     middle (the burst), then deliver the next good packet.
//   - Feed the same sequence to both gopus (explicit DRED path via
//     dec.DecodeDRED) and the libopus quality-sequence oracle.
//   - Assert that the recovered loss-frame PCM matches libopus at the
//     decoderDREDConcealQualityBar (waveform corr/RMS gate; same bar as the
//     existing 1- and 2-loss tests).

import (
	"fmt"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
)

// burstTrainConfig controls the burst-train parity test parameters.
type burstTrainConfig struct {
	burstLen  int // number of consecutive lost frames
	frameSize int // samples per frame at 48 kHz (480 or 960)
}

// buildBurstTrainPackets encodes (warmup + burstLen + 1) frames of voiced CELT
// with DRED enabled and returns (packets, reference PCM).
func buildBurstTrainPackets(t *testing.T, cfg burstTrainConfig, warmup int) (packets [][]byte, reference []float32) {
	t.Helper()

	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    1,
		Application: ApplicationLowDelay,
	})
	if err != nil {
		t.Fatalf("burst-train NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(cfg.frameSize); err != nil {
		t.Fatalf("burst-train SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("burst-train SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(48000); err != nil {
		t.Fatalf("burst-train SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatalf("burst-train SetComplexity: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("burst-train SetSignal: %v", err)
	}
	if err := enc.SetPacketLoss(60); err != nil {
		t.Fatalf("burst-train SetPacketLoss: %v", err)
	}
	if err := enc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("burst-train encoder SetDNNBlob: %v", err)
	}
	if err := enc.SetDREDDuration(80); err != nil {
		t.Fatalf("burst-train SetDREDDuration: %v", err)
	}
	enc.enc.SetMode(encpkg.ModeCELT)

	total := warmup + cfg.burstLen + 1
	pcm := make([]float32, cfg.frameSize)
	packet := make([]byte, maxPacketBytesPerStream)
	for i := 0; i < total; i++ {
		fillDREDQualitySpeechFrame(pcm, i)
		reference = append(reference, pcm...)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("burst-train Encode frame %d: %v", i, err)
		}
		packets = append(packets, append([]byte(nil), packet[:n]...))
	}
	return packets, reference
}

// runBurstTrainParityTest runs the end-to-end burst-train DRED parity check.
func runBurstTrainParityTest(t *testing.T, label string, cfg burstTrainConfig) {
	t.Helper()
	libopustest.RequireOracle(t)

	const warmup = 10
	packets, reference := buildBurstTrainPackets(t, cfg, warmup)
	totalFrames := len(packets)
	if totalFrames < warmup+cfg.burstLen+1 {
		t.Fatalf("%s not enough packets: %d", label, totalFrames)
	}

	// delivered[i]=true: frame is on the wire; burst frames are NOT delivered
	delivered := make([]bool, totalFrames)
	for i := 0; i < warmup; i++ {
		delivered[i] = true
	}
	delivered[warmup+cfg.burstLen] = true // recovery carrier

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, label+" dred model", err)
	}
	combinedBlob := append(append([]byte(nil), decoderBlob...), dredModelBlob...)

	// Build oracle sequence: nil slice element = lost (oracle sees packet_len=0)
	oracleSeq := make([][]byte, totalFrames)
	for i, p := range packets {
		if delivered[i] {
			oracleSeq[i] = p
		}
	}

	// libopus reference via quality-sequence oracle
	libResult := probeBurstTrainLibopusOracle(t, label, oracleSeq, decoderBlob, dredModelBlob, cfg.frameSize, true)

	// gopus decode (explicit DRED path)
	gopusResult := decodeBurstTrainGopus(t, label, oracleSeq, reference, combinedBlob, cfg.frameSize)

	if gopusResult.lossFrames != libResult.lossFrames {
		t.Fatalf("%s loss frame count: gopus=%d libopus=%d", label, gopusResult.lossFrames, libResult.lossFrames)
	}
	if gopusResult.lossFrames != cfg.burstLen {
		t.Fatalf("%s expected %d lost frames, got %d", label, cfg.burstLen, gopusResult.lossFrames)
	}
	if gopusResult.dredFrames != libResult.dredFrames {
		t.Fatalf("%s DRED frame count: gopus=%d libopus=%d", label, gopusResult.dredFrames, libResult.dredFrames)
	}
	if len(gopusResult.lossDecoded) != len(libResult.lossDecoded) {
		t.Fatalf("%s decoded sample count: gopus=%d libopus=%d",
			label, len(gopusResult.lossDecoded), len(libResult.lossDecoded))
	}
	if len(gopusResult.lossDecoded) == 0 {
		t.Skipf("%s no DRED-recovered PCM to compare (%d burst frames all fell back to PLC)", label, cfg.burstLen)
	}

	// PCM quality gate: gopus vs libopus recovered audio
	maxDelay := concealMaxDelay(cfg.frameSize * cfg.burstLen)
	got48 := upsample16kTo48k(gopusResult.lossDecoded, 1)
	want48 := upsample16kTo48k(libResult.lossDecoded, 1)
	cmp, cmpErr := qualitycompare.CompareDecodedFloat32(got48, want48, 48000, 1, maxDelay)
	if cmpErr != nil {
		t.Fatalf("%s compare: %v", label, cmpErr)
	}
	t.Logf("%s burst=%d dred_frames=%d corr=%.5f rms=%.5f",
		label, cfg.burstLen, gopusResult.dredFrames, cmp.Corr, cmp.RMSRatio)
	qualitycompare.AssertQuality(t, cmp, decoderDREDConcealQualityBar,
		fmt.Sprintf("%s burst-train DRED recovered PCM vs libopus", label))
}

// probeBurstTrainLibopusOracle queries the quality-sequence C oracle for the
// burst-train sequence.  Uses the oracle's native GDQI/GDQO protocol (same as
// decodeLibopusDREDQualityPackets in decoder_dred_quality_libopus_test.go but
// renamed to avoid collision, with configurable frameSize).
func probeBurstTrainLibopusOracle(t *testing.T, label string, packets [][]byte, decoderBlob, dredBlob []byte, frameSize int, useDRED bool) dredQualityRun {
	t.Helper()

	binPath, err := getLibopusDREDQualitySequenceHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, label+" quality sequence", err)
	}

	useDREDFlag := uint32(0)
	if useDRED {
		useDREDFlag = 1
	}
	payload := libopustest.NewOraclePayload(libopusDREDQualitySequenceInputMagic,
		uint32(48000),
		uint32(1),
		uint32(frameSize),
		uint32(len(packets)),
		useDREDFlag,
		uint32(len(decoderBlob)),
		uint32(len(dredBlob)),
	)
	payload.Raw(decoderBlob)
	payload.Raw(dredBlob)
	for _, pkt := range packets {
		if pkt != nil {
			payload.U32s(1, uint32(len(pkt)))
			payload.Raw(pkt)
		} else {
			payload.U32s(0, 0)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), label+" quality", libopusDREDQualitySequenceOutputMagic)
	if err != nil {
		t.Fatalf("%s quality oracle: %v", label, err)
	}

	run := dredQualityRun{
		lossFrames:     int(reader.I32()),
		dredFrames:     int(reader.I32()),
		fallbackFrames: int(reader.I32()),
	}
	channels := int(reader.I32())
	sampleRate := int(reader.I32())
	oracleFrameSize := int(reader.I32())
	sampleCount := int(reader.I32())
	if err := reader.Err(); err != nil {
		t.Fatalf("%s quality oracle header: %v", label, err)
	}
	if channels != 1 || sampleRate != 48000 || oracleFrameSize != frameSize {
		t.Fatalf("%s quality oracle shape=(%d,%d,%d) want (1,48000,%d)",
			label, channels, sampleRate, oracleFrameSize, frameSize)
	}
	if sampleCount != run.lossFrames*frameSize*channels {
		t.Fatalf("%s quality oracle sample count=%d want %d",
			label, sampleCount, run.lossFrames*frameSize*channels)
	}
	run.lossDecoded = make([]float32, sampleCount)
	for i := range run.lossDecoded {
		run.lossDecoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("%s quality oracle PCM: %v", label, err)
	}
	return run
}

// decodeBurstTrainGopus decodes a sequence with gopus using dec.DecodeDRED for
// lost frames when DRED is available, matching the quality-sequence oracle loop.
// It correctly handles arbitrary frameSize (not just dredQualityFrameSize=960).
func decodeBurstTrainGopus(t *testing.T, label string, packets [][]byte, reference []float32, combinedBlob []byte, frameSize int) dredQualityRun {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("%s burst gopus NewDecoder: %v", label, err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(combinedBlob); err != nil {
		t.Fatalf("%s burst gopus SetDNNBlob: %v", label, err)
	}

	probe := NewDREDDecoder()
	if err := probe.SetDNNBlob(combinedBlob); err != nil {
		t.Fatalf("%s burst gopus DREDDecoder SetDNNBlob: %v", label, err)
	}
	dredState := NewDRED()

	var run dredQualityRun
	pcm := make([]float32, dec.maxPacketSamples)
	expected := 0
	haveExpected := false

	for frame, packet := range packets {
		if packet == nil {
			continue
		}
		if haveExpected {
			missing := frame - expected
			if missing > 0 {
				available := 0
				dredReady := false
				var parseErr error
				available, _, parseErr = probe.Parse(dredState, packet, missing*frameSize, 48000, false)
				dredReady = parseErr == nil && available > 0 && dredState.Processed()

				for lostAgo := missing; lostAgo >= 1; lostAgo-- {
					origFrame := frame - lostAgo
					kindDRED := false
					var n int
					var decErr error
					if dredReady && available >= lostAgo*frameSize {
						n, decErr = dec.DecodeDRED(dredState, lostAgo*frameSize, pcm, frameSize)
						if decErr == nil {
							kindDRED = true
						}
					}
					if !kindDRED {
						n, decErr = dec.Decode(nil, pcm)
					}
					if decErr != nil {
						t.Fatalf("%s burst frame=%d: %v", label, origFrame, decErr)
					}
					// Accumulate loss-frame PCM (do not use appendDecodedFrame which
					// hardcodes dredQualityFrameSize=960 and would mis-index at 480 frames)
					run.lossFrames++
					if kindDRED {
						run.dredFrames++
					} else {
						run.fallbackFrames++
					}
					run.lossDecoded = append(run.lossDecoded, pcm[:n]...)
					start := origFrame * frameSize
					end := start + frameSize
					if start >= 0 && end <= len(reference) {
						run.lossReference = append(run.lossReference, reference[start:end]...)
					}
				}
			}
		}
		n, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("%s burst Decode frame %d: %v", label, frame, err)
		}
		run.decoded = append(run.decoded, pcm[:n]...)
		expected = frame + 1
		haveExpected = true
	}
	return run
}

// TestDREDBurstTrainRecovery4FramesCELT48k verifies gopus DRED recovery over
// 4 consecutive losses tracks libopus cursor/window across the whole burst.
// Reference: opus_decoder_dred_decode_float loop, opus_decoder.c libopus 1.6.1.
func TestDREDBurstTrainRecovery4FramesCELT48k(t *testing.T) {
	runBurstTrainParityTest(t, "4-frame-burst-CELT-48k", burstTrainConfig{
		burstLen:  4,
		frameSize: 960,
	})
}

// TestDREDBurstTrainRecovery6FramesCELT48k verifies DRED recovery over a
// 6-frame (120 ms) burst — frames beyond DRED payload coverage fall back to
// plain PLC and the frame-count parity is still asserted.
func TestDREDBurstTrainRecovery6FramesCELT48k(t *testing.T) {
	runBurstTrainParityTest(t, "6-frame-burst-CELT-48k", burstTrainConfig{
		burstLen:  6,
		frameSize: 960,
	})
}

// TestDREDBurstTrainRecovery3Frames10msCELT48k verifies DRED burst recovery
// with 10 ms (480-sample) frames — tests the frame-size cursor arithmetic.
func TestDREDBurstTrainRecovery3Frames10msCELT48k(t *testing.T) {
	runBurstTrainParityTest(t, "3-frame-10ms-burst-CELT-48k", burstTrainConfig{
		burstLen:  3,
		frameSize: 480,
	})
}
