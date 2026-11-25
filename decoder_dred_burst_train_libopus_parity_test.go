//go:build gopus_dred || gopus_osce

package gopus

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	encpkg "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/rangecoding"
	silkpkg "github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

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
		t.Fatalf("%s no DRED-recovered PCM to compare (%d burst frames all fell back to PLC)", label, cfg.burstLen)
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

func TestDecoderCELTNeuralPLCAPIRatesMatchesLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	for _, channels := range []int{1, 2} {
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband,
			Channels: channels, ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "CELT DRED carrier", err)
		}
		if ParseTOC(packetInfo.packet[0]).Stereo != (channels == 2) {
			t.Fatalf("carrier TOC stereo=%t want %t", ParseTOC(packetInfo.packet[0]).Stereo, channels == 2)
		}
		var nextPacket []byte
		if channels == 1 {
			nextPacket = makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 960)
		} else {
			nextPacket = makeValidStereoCELTPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthFullband)
		}
		for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, durationMS := range []int{20, 40, 60} {
				t.Run(fmt.Sprintf("%dch_%dHz_%dms", channels, rate, durationMS), func(t *testing.T) {
					dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					setDecoderComplexityForLibopusDREDParityTest(t, dec)
					if err := dec.SetDNNBlob(decoderBlob); err != nil {
						t.Fatalf("SetDNNBlob: %v", err)
					}
					setDREDDecoderBlobFromBytesForTest(t, dec, dredBlob)
					carrierSize := rate / 50
					lostSize := rate * durationMS / 1000
					pcm := make([]float32, lostSize*channels)
					carrierPCM := make([]float32, carrierSize*channels)
					recoveryPCM := make([]float32, carrierSize*channels)
					n, err := dec.Decode(packetInfo.packet, carrierPCM)
					if err != nil || n != carrierSize {
						t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", n, err, carrierSize)
					}
					maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, rate)
					want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket,
						maxDRED, oracleRate, lostSize, libopusDecoderDREDSequenceSourceLost, lostSize,
						libopusDecoderDREDSequenceSourceNone, 0, true)
					if err != nil {
						t.Fatalf("live C decoder: %v", err)
					}
					if want.carrierRet != n || want.step0.ret != lostSize || want.next.ret != carrierSize {
						t.Fatalf("C returns carrier/loss/recovery=%d/%d/%d want %d/%d/%d",
							want.carrierRet, want.step0.ret, want.next.ret, n, lostSize, carrierSize)
					}
					gotN, err := dec.Decode(nil, pcm)
					if err != nil || gotN != want.step0.ret {
						t.Fatalf("Decode(nil)=(%d,%v) C=%d", gotN, err, want.step0.ret)
					}
					assertFloat32BitsEqual(t, pcm[:gotN*channels], want.step0.pcm, "lost PCM")
					gotNext, err := dec.Decode(nextPacket, recoveryPCM)
					if err != nil || gotNext != want.next.ret {
						t.Fatalf("Decode(recovery)=(%d,%v) C=%d", gotNext, err, want.next.ret)
					}
					assertFloat32BitsEqual(t, recoveryPCM[:gotNext*channels], want.next.pcm, "recovery PCM")

					// Warm the retained decoder and exercise active carrier→neural
					// loss→recovery cycles with caller-owned buffers. Each duration
					// drives the same 20 ms CELT chunk cadence tested above.
					cycleOK := true
					cycle := func() {
						if n, err := dec.Decode(packetInfo.packet, carrierPCM); err != nil || n != carrierSize {
							cycleOK = false
						}
						if n, err := dec.Decode(nil, pcm); err != nil || n != lostSize {
							cycleOK = false
						}
						if n, err := dec.Decode(nextPacket, recoveryPCM); err != nil || n != carrierSize {
							cycleOK = false
						}
					}
					cycle()
					cycle()
					if allocs := testing.AllocsPerRun(20, cycle); allocs != 0 {
						t.Fatalf("warm carrier/loss/recovery allocations=%v want 0", allocs)
					}
					if !cycleOK {
						t.Fatal("warm carrier/loss/recovery return changed")
					}
					nonzero := false
					for i, sample := range pcm {
						if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
							t.Fatalf("loss sample[%d]=%v is nonfinite", i, sample)
						}
						if sample != 0 {
							nonzero = true
						}
					}
					if !nonzero {
						t.Fatal("warm neural loss output is silent")
					}
				})
			}
		}
	}
}

var decoderDREDConcealQualityBar = qualitycompare.QualityBar{
	MinQ:    math.Inf(-1), // logged only; opus_compare Q is unreliable on short pure-tone frames (see above).
	MinCorr: 0.9995,
	RMSLo:   0.98,
	RMSHi:   1.02,
	Desc:    "near-exact concealed audio vs libopus (16 kHz decode; corr/RMS gate, 48 kHz Q logged)",
}

// concealMaxDelay bounds the comparator delay search for a concealed frame of n
// per-channel samples (in the 48 kHz upsampled domain).
func concealMaxDelay(n int) int {
	d := n
	if d < 480 {
		d = 480
	}
	return d
}

// upsample16kTo48k linearly interpolates interleaved 16 kHz PCM to 48 kHz (3x).
// Both the gopus candidate and the libopus reference pass through this identical
// path before opus_compare, so the comparison measures gopus-vs-libopus
// divergence at 16 kHz reported on opus_compare's trusted 48 kHz scale.
func upsample16kTo48k(in []float32, channels int) []float32 {
	if channels <= 0 || len(in) == 0 {
		return nil
	}
	frames := len(in) / channels
	if frames == 0 {
		return nil
	}
	out := make([]float32, frames*3*channels)
	for ch := 0; ch < channels; ch++ {
		for i := 0; i < frames; i++ {
			cur := in[i*channels+ch]
			next := cur
			if i+1 < frames {
				next = in[(i+1)*channels+ch]
			}
			base := (i*3)*channels + ch
			out[base] = cur
			out[base+channels] = cur + (next-cur)*(1.0/3.0)
			out[base+2*channels] = cur + (next-cur)*(2.0/3.0)
		}
	}
	return out
}

// assertConcealedAudioMatchesLibopus checks exact PCM bits and applies the
// independent opus_compare quality gate for a concealed frame. It upsamples
// 16 kHz output to 48 kHz for the quality score.
func assertConcealedAudioMatchesLibopus(t *testing.T, got, want []float32, channels int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	assertFloat32BitsEqual(t, got, want, label+" PCM bits")
	gotUp := upsample16kTo48k(got, channels)
	wantUp := upsample16kTo48k(want, channels)
	cmp, err := qualitycompare.CompareDecodedFloat32(gotUp, wantUp, 48000, channels, concealMaxDelay(len(wantUp)/channels))
	if err != nil {
		t.Fatalf("%s compare: %v", label, err)
	}
	qualitycompare.AssertQuality(t, cmp, decoderDREDConcealQualityBar, label)
}

func prepareDecoderForNeuralConcealmentParity(t *testing.T) (*Decoder, []float32, libopusDREDPacket, int) {
	t.Helper()
	return prepareDecoderForNeuralConcealmentParityForFrameSize(t, 480)
}

func prepareDecoderForNeuralConcealmentParityForFrameSize(t *testing.T, frameSize int) (*Decoder, []float32, libopusDREDPacket, int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacketWithFrameSize(frameSize)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	if channels != 1 {
		t.Fatalf("conceal parity test requires mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(16000, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(requireLibopusDecoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	setDREDDecoderBlobFromBytesForTest(t, dec, modelBlob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("Decode did not retain processed DRED state")
	}
	return dec, pcm, packetInfo, n
}

func setDREDDecoderBlobFromBytesForTest(t *testing.T, dec *Decoder, modelBlob []byte) {
	t.Helper()

	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
}

func TestDecoderFirstLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 0, 0, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "first-loss next-packet")
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil)=%d want %d", gotN, n)
	}
	// End-to-end audio gate: the quality comparator checks the 16 kHz decode
	// rate and logs the 48 kHz Q score.
	assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step0.pcm[:n], dec.Channels(), "first-loss live-sequence pcm")

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
	}

	assertConcealedAudioMatchesLibopus(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.Channels(), "first-loss next packet live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "first-loss next packet live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "first-loss next packet live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "first-loss next packet live-sequence celt")
}

func TestDecoderSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 1, 2*n, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "second-loss next-packet")
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
	}
	if want.step1.ret != n {
		t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, first)=%d want %d", gotN, n)
	}
	assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step0.pcm[:n], dec.Channels(), "second-loss warmup live-sequence pcm")

	gotN, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
	}
	assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step1.pcm[:n], dec.Channels(), "second-loss live-sequence pcm")

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
	}

	assertConcealedAudioMatchesLibopus(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.Channels(), "second-loss next packet live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "second-loss next packet live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "second-loss next packet live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "second-loss next packet live-sequence celt")
}

func TestDecoderFirstLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("first-loss next-packet carrier_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil)=%d want %d", gotN, n)
			}

			assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step0.pcm[:n], dec.Channels(), fmt.Sprintf("first-loss frame-size %d live-sequence pcm", frameSize))

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
			}

			assertConcealedAudioMatchesLibopus(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.Channels(), fmt.Sprintf("first-loss frame-size %d next packet live-sequence pcm", frameSize))
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "first-loss frame-size next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "first-loss frame-size next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "first-loss frame-size next packet live-sequence celt")
		})
	}
}

func TestDecoderSecondLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("second-loss next-packet carrier_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, first)=%d want %d", gotN, n)
			}

			assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step0.pcm[:n], dec.Channels(), fmt.Sprintf("second-loss frame-size %d warmup live-sequence pcm", frameSize))

			gotN, err = dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
			}
			assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step1.pcm[:n], dec.Channels(), fmt.Sprintf("second-loss frame-size %d live-sequence pcm", frameSize))

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
			}

			assertConcealedAudioMatchesLibopus(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.Channels(), fmt.Sprintf("second-loss frame-size %d next packet live-sequence pcm", frameSize))
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "second-loss frame-size next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "second-loss frame-size next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "second-loss frame-size next packet live-sequence celt")
		})
	}
}

func assertCrossModeHandoverDecodeRouting(t *testing.T, label string, seedMode Mode, seedBW Bandwidth, seedFrameSize int, carrierFrameSize int, decoderRate int, lossCount int) {
	t.Helper()
	libopustest.RequireOracle(t)

	// CELT DRED carrier packet
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: carrierFrameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, label+" CELT carrier packet", err)
	}

	// Prior-mode seed packet
	seedPacket := makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t, seedMode, seedBW, seedFrameSize)
	toc := ParseTOC(seedPacket[0])
	if toc.Mode != seedMode {
		t.Fatalf("%s seed packet mode=%v want %v", label, toc.Mode, seedMode)
	}

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, label+" dred model", err)
	}

	channels := 1

	// Prepare a warmed-up decoder
	dec, err := NewDecoder(DefaultDecoderConfig(decoderRate, channels))
	if err != nil {
		t.Fatalf("%s NewDecoder: %v", label, err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("%s SetDNNBlob: %v", label, err)
	}
	setDREDDecoderBlobFromBytesForTest(t, dec, dredModelBlob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	if _, err := dec.Decode(seedPacket, pcm); err != nil {
		t.Fatalf("%s Decode(seed) error: %v", label, err)
	}
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("%s Decode(carrier) error: %v", label, err)
	}
	if n <= 0 {
		t.Fatalf("%s Decode(carrier) returned no audio", label)
	}

	// DRED sidecar must be populated
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode(carrier) did not retain DRED payload", label)
	}
	if requireDecoderDREDState(t, dec).dredDecoded.NbLatents <= 0 {
		t.Fatalf("%s Decode(carrier) did not retain processed DRED latents", label)
	}

	// libopus oracle: seed then recover lossCount steps from carrier DRED
	step0Source := libopusDecoderDREDSequenceSourceCarrierDRED
	step1Source := libopusDecoderDREDSequenceSourceNone
	step1Offset := 0
	if lossCount >= 2 {
		step1Source = libopusDecoderDREDSequenceSourceCarrierDRED
		step1Offset = 2 * n
	}
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderRate)
	want, err := probeLibopusDecoderDREDSequence(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		step0Source, n,
		step1Source, step1Offset,
		false,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cross-mode")
	if want.step0.ret != n {
		t.Fatalf("%s libopus first-loss ret=%d want %d", label, want.step0.ret, n)
	}

	// Structural checks: oracle ret must match frame count
	dred := parseCarrierDREDForExplicitDecode(t, decoderRate, packetInfo)

	// First loss
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != want.step0.ret {
		t.Fatalf("%s explicit DRED decode(first)=%d want %d", label, got, want.step0.ret)
	}
	// Structural PLC state: Blend and LossCount must track oracle exactly
	gotState := requireDecoderDREDState(t, dec).dredPLC.Snapshot()
	if gotState.Blend != want.step0.state.Blend {
		t.Fatalf("%s blend after first loss=%d want %d", label, gotState.Blend, want.step0.state.Blend)
	}
	if gotState.LossCount != want.step0.state.LossCount {
		t.Fatalf("%s loss_count after first loss=%d want %d", label, gotState.LossCount, want.step0.state.LossCount)
	}

	// PCM quality: log as cross-mode residual evidence
	assertCrossModeHandoverPCMExact(t, pcm[:n], want.step0.pcm[:n], label+" first-loss PCM")

	if lossCount < 2 {
		return
	}
	if want.step1.ret != n {
		t.Fatalf("%s libopus second-loss ret=%d want %d", label, want.step1.ret, n)
	}

	// Second loss
	pcm1 := make([]float32, len(pcm))
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != want.step1.ret {
		t.Fatalf("%s explicit DRED decode(second)=%d want %d", label, got, want.step1.ret)
	}
	gotState2 := requireDecoderDREDState(t, dec).dredPLC.Snapshot()
	if gotState2.Blend != want.step1.state.Blend {
		t.Fatalf("%s blend after second loss=%d want %d", label, gotState2.Blend, want.step1.state.Blend)
	}
	if gotState2.LossCount != want.step1.state.LossCount {
		t.Fatalf("%s loss_count after second loss=%d want %d", label, gotState2.LossCount, want.step1.state.LossCount)
	}
	assertCrossModeHandoverPCMExact(t, pcm1[:n], want.step1.pcm[:n], label+" second-loss PCM")
}

// assertCrossModeHandoverPCMExact compares gopus and libopus cross-mode
// concealed audio exactly and logs the quality diagnostic.
func assertCrossModeHandoverPCMExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("%s PCM len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s PCM[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	// Use 48 kHz upsampled comparison matching assertConcealedAudioMatchesLibopus
	gotUp := upsample16kTo48k(got, 1)
	wantUp := upsample16kTo48k(want, 1)
	n := len(wantUp)
	cmp, err := qualitycompare.CompareDecodedFloat32(gotUp, wantUp, 48000, 1, n)
	if err != nil {
		t.Fatalf("%s CompareDecodedFloat32: %v", label, err)
	}
	t.Logf("%s: Q=%.2f delay=%d corr=%.6f rms=%.4f (cross-mode DRED recovery is bit-exact vs libopus oracle)", label, cmp.Q, cmp.BestDelay, cmp.Corr, cmp.RMSRatio)
}

// TestDREDCrossModeHandoverSILKtoCELTFirstLoss verifies DRED decode routing
// and structural state at the first loss after a SILK→CELT mode handover.
// Reference: opus_decoder.c (libopus 1.6.1) mode-switch + opus_decoder_dred_decode_float.
func TestDREDCrossModeHandoverSILKtoCELTFirstLoss(t *testing.T) {
	assertCrossModeHandoverDecodeRouting(t,
		"SILK→CELT-first-loss",
		ModeSILK, BandwidthWideband, 960, 960, 16000, 1,
	)
}

// TestDREDCrossModeHandoverSILKtoCELTSecondLoss extends the SILK→CELT test to
// two consecutive DRED recoveries.
func TestDREDCrossModeHandoverSILKtoCELTSecondLoss(t *testing.T) {
	assertCrossModeHandoverDecodeRouting(t,
		"SILK→CELT-second-loss",
		ModeSILK, BandwidthWideband, 960, 960, 16000, 2,
	)
}

// TestDREDCrossModeHandoverHybridtoCELTFirstLoss verifies DRED routing after
// a Hybrid→CELT handover.
func TestDREDCrossModeHandoverHybridtoCELTFirstLoss(t *testing.T) {
	assertCrossModeHandoverDecodeRouting(t,
		"Hybrid→CELT-first-loss",
		ModeHybrid, BandwidthFullband, 960, 960, 16000, 1,
	)
}

// TestDREDCrossModeHandoverHybridtoCELTSecondLoss verifies two consecutive
// DRED recovery steps after a Hybrid→CELT handover.
func TestDREDCrossModeHandoverHybridtoCELTSecondLoss(t *testing.T) {
	assertCrossModeHandoverDecodeRouting(t,
		"Hybrid→CELT-second-loss",
		ModeHybrid, BandwidthFullband, 960, 960, 16000, 2,
	)
}

// TestDREDCrossModeHandoverSILKtoCELT10msFirstLoss verifies SILK→CELT
// handover with 10 ms (480-sample) carrier frames.
func TestDREDCrossModeHandoverSILKtoCELT10msFirstLoss(t *testing.T) {
	assertCrossModeHandoverDecodeRouting(t,
		"SILK→CELT-10ms-first-loss",
		ModeSILK, BandwidthWideband, 480, 480, 16000, 1,
	)
}

// TestDREDCrossModeHandoverSILKtoCELT48kFirstLoss verifies SILK→CELT at
// multiple decoder rates.
func TestDREDCrossModeHandoverSILKtoCELT48kFirstLoss(t *testing.T) {
	for _, decoderRate := range []int{48000, 24000} {
		decoderRate := decoderRate
		t.Run(fmt.Sprintf("decoder_%d", decoderRate), func(t *testing.T) {
			assertCrossModeHandoverDecodeRouting(t,
				fmt.Sprintf("SILK→CELT-first-loss-%dHz", decoderRate),
				ModeSILK, BandwidthWideband, 960, 960, decoderRate, 1,
			)
		})
	}
}

func TestDecoderCachedDREDDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracle(t, "cached CELT", packetInfo)
		})
	}
}

func TestDecoderCachedCELTDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband {
		t.Fatalf("cached CELT DRED API-rate packet TOC=%+v, want CELT FB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 24000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached CELT warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			dred := parseCarrierDREDForExplicitDecode(t, sampleRate, packetInfo)
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached CELT API-rate first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT API-rate first-loss ret=%d want %d", want.step0.ret, n)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle): a public
			// Decode(nil) runs plain PLC and consumes no cached DRED
			// (opus_decoder.c:736); recovery is verified through the explicit
			// entry point per cc04ecf0's reconciliation.
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached CELT API-rate live-sequence first-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached CELT API-rate live-sequence first-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached CELT API-rate live-sequence first-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached CELT API-rate live-sequence first-loss celt")
		})
	}
}

func TestDecoderCachedStereoCELTDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT DRED API-rate packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband || !toc.Stereo {
		t.Fatalf("cached stereo CELT DRED API-rate packet TOC=%+v, want stereo CELT FB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, 2)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached stereo CELT warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			dred := parseCarrierDREDForExplicitDecode(t, sampleRate, packetInfo)
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo CELT decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo CELT API-rate first-loss")
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo CELT DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus cached stereo CELT API-rate first-loss ret=%d want %d", want.step0.ret, n)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736).
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached stereo CELT API-rate live-sequence first-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo CELT API-rate live-sequence first-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo CELT API-rate live-sequence first-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached stereo CELT API-rate live-sequence first-loss celt")
		})
	}
}

func TestDecoderCachedCELTDREDRequestedPLCDurationMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	for _, sampleRate := range []int{8000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, sampleRate, packetInfo)
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			// DRED recovery is requested through the explicit DRED-decode path
			// (SourceCarrierDRED oracle = opus_decoder_dred_decode_float,
			// opus_decoder.c:1625). The carrier DRED covers one frame, so the
			// requested recovery duration is the carrier frame. A public
			// Decode(nil) of a longer duration runs PLAIN PLC and consumes no
			// cached DRED (opus_decoder.c:736 gates the FEC feed on dred!=NULL);
			// that invariant is asserted separately below.
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED requested-duration sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached CELT requested-duration first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT requested-duration first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached CELT requested-duration live-sequence first-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached CELT requested-duration live-sequence first-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached CELT requested-duration live-sequence first-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached CELT requested-duration live-sequence first-loss celt")

			// Verify a public packet-loss Decode(nil) over a longer requested
			// duration applies no cached DRED (plain PLC), matching
			// opus_decode(NULL,...) with dred==NULL (opus_decoder.c:736).
			lossDec, lossN := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			lossPCM := make([]float32, 2*lossN*lossDec.Channels())
			gotLoss, err := lossDec.Decode(nil, lossPCM)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if gotLoss != 2*lossN {
				t.Fatalf("Decode(nil)=%d want %d", gotLoss, 2*lossN)
			}
			lossState := requireDecoderDREDState(t, lossDec)
			if lossState.dredRecovery != 0 {
				t.Fatalf("public lost dredRecovery=%d want 0", lossState.dredRecovery)
			}
			if fill := lossState.dredPLC.FECFillPos(); fill != 0 {
				t.Fatalf("public lost FECFillPos=%d want 0", fill)
			}
			if skip := lossState.dredPLC.FECSkip(); skip != 0 {
				t.Fatalf("public lost FECSkip=%d want 0", skip)
			}
		})
	}
}

func TestDecoderCachedStereoDREDDecodeMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Fatalf("cached stereo CELT live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo first-loss")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus cached stereo decoder first-loss ret=%d want %d", want.step0.ret, n)
	}

	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	for i := 0; i < got; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("cached stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.step0.pcm[2*i] - want.step0.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus cached stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo live-sequence first-loss pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo live-sequence first-loss plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo live-sequence first-loss fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached stereo live-sequence first-loss celt")
}

func TestDecoderCachedStereoDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Fatalf("cached stereo CELT second-loss parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo second-loss")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("libopus cached stereo DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
	}

	pcm0 := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm0, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	pcm1 := make([]float32, n*dec.Channels())
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm1, got, "cached stereo second loss")
	assertInterleavedStereoDuplicated(t, want.step1.pcm, got, "libopus cached stereo second loss")

	assertDecodedPCMExactAndQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo live-sequence second-loss pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached stereo live-sequence second-loss plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached stereo live-sequence second-loss fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "cached stereo live-sequence second-loss celt")
}

func TestDecoderCachedStereoDRED16kCELTMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize48k      = 960
		decoderSampleRate = 16000
	)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize48k,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo || toc.Mode != ModeCELT {
		t.Fatalf("cached stereo 16k DRED parity packet mismatch: stereo=%t mode=%v", toc.Stereo, toc.Mode)
	}

	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderSampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)

	for _, tc := range []struct {
		name        string
		step1Source int
	}{
		{name: "first_loss"},
		{name: "second_loss", step1Source: libopusDecoderDREDSequenceSourceCarrierDRED},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, 2)
			dred := parseCarrierDREDForExplicitDecode(t, decoderSampleRate, packetInfo)
			if n != wantFrame {
				t.Fatalf("cached stereo 16k warmup samples=%d want %d", n, wantFrame)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, tc.step1Source, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo 16k "+tc.name)
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo 16k DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus cached stereo 16k first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			comparePCM := pcm[:got*dec.Channels()]
			compareState := want.step0
			compareLabel := "cached stereo 16k first-loss"

			if tc.step1Source != 0 {
				pcm1 := make([]float32, n*dec.Channels())
				got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
				if got != n {
					t.Fatalf("explicit DRED decode=%d want %d", got, n)
				}
				if want.step1.ret != n {
					t.Fatalf("libopus cached stereo 16k second-loss ret=%d want %d", want.step1.ret, n)
				}
				comparePCM = pcm1[:got*dec.Channels()]
				compareState = want.step1
				compareLabel = "cached stereo 16k second-loss"
			}

			assertInterleavedStereoApproxDuplicated(t, comparePCM, n, compareLabel, 1e-2)
			assertInterleavedStereoApproxDuplicated(t, compareState.pcm, n, compareLabel+" libopus", 1e-2)
			assertDecodedPCMExactAndQuality(t, comparePCM, compareState.pcm[:n*dec.Channels()], decoderSampleRate, dec.Channels(), compareLabel+" live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), compareState.state, compareLabel+" live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), compareState.fargan, compareLabel+" live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, compareState.celt48k, compareLabel+" live-sequence celt")
		})
	}
}

func TestDecoderCachedStereoDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}
	nextPacket := makeValidCELTPacketForDREDTest(t)

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Fatalf("cached stereo CELT follow-up parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo first-loss next-packet")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus cached stereo first-loss ret=%d want %d", want.step0.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus cached stereo follow-up ret=%d want >0", want.next.ret)
	}

	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	nextPCM := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next stereo CELT packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next stereo CELT packet)=%d want %d", gotNext, want.next.ret)
	}

	assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext*dec.Channels()], want.next.pcm[:gotNext*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo next packet live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached stereo next packet live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached stereo next packet live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached stereo next packet live-sequence celt")
}

func TestDecoderCachedStereoDREDSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}
	nextPacket := makeValidCELTPacketForDREDTest(t)

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Fatalf("cached stereo CELT second-loss follow-up parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo second-loss next-packet")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("libopus cached stereo DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus cached stereo follow-up ret=%d want >0", want.next.ret)
	}

	pcm0 := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm0, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	pcm1 := make([]float32, n*dec.Channels())
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm1, got, "cached stereo second loss")
	assertInterleavedStereoDuplicated(t, want.step1.pcm, got, "libopus cached stereo second loss")

	nextPCM := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next stereo CELT packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next stereo CELT packet)=%d want %d", gotNext, want.next.ret)
	}

	assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext*dec.Channels()], want.next.pcm[:gotNext*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo second-loss next packet live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached stereo second-loss next packet live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached stereo second-loss next packet live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached stereo second-loss next packet live-sequence celt")
}

func TestDecoderCachedStereoDREDCELTMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "wb_2_5ms", bandwidth: BandwidthWideband, frameSize: 120},
		{name: "wb_5ms", bandwidth: BandwidthWideband, frameSize: 240},
		{name: "wb_10ms", bandwidth: BandwidthWideband, frameSize: 480},
		{name: "wb_20ms", bandwidth: BandwidthWideband, frameSize: 960},
		{name: "swb_2_5ms", bandwidth: BandwidthSuperwideband, frameSize: 120},
		{name: "swb_5ms", bandwidth: BandwidthSuperwideband, frameSize: 240},
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_2_5ms", bandwidth: BandwidthFullband, frameSize: 120},
		{name: "fb_5ms", bandwidth: BandwidthFullband, frameSize: 240},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}
	flows := []cachedStereoDREDLiveFlow{
		cachedStereoDREDFirstLoss,
		cachedStereoDREDSecondLoss,
		cachedStereoDREDFirstLossThenNext,
		cachedStereoDREDSecondLossThenNext,
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			nextPacket := makeValidStereoCELTPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)
			for _, flow := range flows {
				flow := flow
				t.Run(flow.name(), func(t *testing.T) {
					assertDecoderCachedStereoDREDLiveSequenceMatchesLibopus(t, "cached stereo CELT "+tc.name+" "+flow.name(), libopusDREDPacketConfig{
						FrameSize: tc.frameSize,
						ForceMode: ModeCELT,
						Bandwidth: tc.bandwidth,
					}, nextPacket, flow)
				})
			}
		})
	}
}

func TestDecoderCachedDREDDecodeCELTSuperwidebandMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracle(t, "cached CELT SWB", packetInfo)
		})
	}
}

func TestDecoderCachedDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Fatalf("cached CELT live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT first-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT decoder follow-up ret=%d want >0", want.next.ret)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736),
			// then decode the next real packet.
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached CELT next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedDREDThenNextPacketCELTSuperwidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Fatalf("cached CELT SWB live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT SWB first-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT SWB decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT SWB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT SWB next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT SWB next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached CELT SWB next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t, "cached CELT", packetInfo)
		})
	}
}

func TestDecoderCachedDREDSecondLossCELTSuperwidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t, "cached CELT SWB", packetInfo)
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Fatalf("cached CELT second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT second-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached CELT decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT decoder follow-up ret=%d want >0", want.next.ret)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736),
			// two recoveries then the next real packet.
			pcm0 := make([]float32, dec.maxPacketSamples)
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode(first)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT live-sequence warmup pcm")

			pcm1 := make([]float32, dec.maxPacketSamples)
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode(second)=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT second-loss next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT second-loss next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached CELT second-loss next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketCELTSuperwidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Fatalf("cached CELT SWB second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT SWB second-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT SWB decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached CELT SWB decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT SWB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB live-sequence warmup pcm")

			pcm1 := make([]float32, dec.maxPacketSamples)
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT SWB second-loss next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT SWB second-loss next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached CELT SWB second-loss next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedDREDDecodeCELTWidebandMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracle(t, "cached CELT WB", packetInfo)
		})
	}
}

func TestDecoderCachedDREDThenNextPacketCELTWidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Fatalf("cached CELT WB live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT WB first-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT WB decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT WB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "cached CELT WB live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached CELT WB next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT WB next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT WB next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached CELT WB next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedDREDSecondLossCELTWidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t, "cached CELT WB", packetInfo)
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketCELTWidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Fatalf("cached CELT WB second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT WB second-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT WB decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached CELT WB decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT WB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm0[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "cached CELT WB live-sequence warmup pcm")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "cached CELT WB live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached CELT WB second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT WB second-loss next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT WB second-loss next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached CELT WB second-loss next packet live-sequence celt")
		})
	}
}

func TestDecoderExplicitDREDDecode16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k frame-size libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k frame-size libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k follow-up frame-size pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k follow-up frame-size plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitDREDDecode16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT SWB frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k celt swb frame-size libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt swb frame-size libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt swb frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt swb frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT SWB follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k CELT SWB follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k celt swb follow-up frame-size pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt swb follow-up frame-size plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt swb follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt swb follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitDREDDecode16kCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT WB frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k celt wb frame-size libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt wb frame-size libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt wb frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt wb frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT WB follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k CELT WB follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k celt wb follow-up frame-size pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt wb follow-up frame-size plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt wb follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt wb follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "offset matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "offset matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "offset matrix fargan")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixCELTSuperwidebandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthSuperwideband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "celt swb offset matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "celt swb offset matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "celt swb offset matrix fargan")
		})
	}
}

func TestDecoderExplicitDREDDecodeFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt swb frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt wb frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "second loss frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "second loss frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB second-loss parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT SWB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt swb second loss frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb second loss frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit follow-up frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "follow-up frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "follow-up frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "follow-up frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "follow-up frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT SWB follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt swb follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb follow-up celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss follow-up frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next packet) after second loss=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "second-loss follow-up frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "second-loss follow-up frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "second-loss follow-up frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "second-loss follow-up frame size matrix celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB second-loss follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT SWB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT SWB second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT SWB packet) after second loss=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt swb second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB second-loss parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT WB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt wb second loss frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb second loss frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT WB follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt wb follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb follow-up celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB second-loss follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT WB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT WB second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT WB packet) after second loss=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt wb second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitDREDWarmup48kStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, _, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Fatalf("48 kHz warmup parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	if dec.celtDecoder == nil {
		t.Fatal("decoder missing CELT state after seed packet")
	}
	gotPreemph := dec.celtDecoder.SnapshotPreemphasisState()
	assertFloat32BitsEqual(t, gotPreemph[:], want.celt48k.WarmupPreemphMem[:], "warmup celt preemph_memD")
	var gotPLCUpdate [4 * lpcnetplc.FrameSize]float32
	_, gotPLCPreemph := dec.celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem(gotPLCUpdate[:])
	assertFloat32BitsEqual(t, []float32{gotPLCPreemph}, []float32{want.celt48k.WarmupPLCPreemph}, "warmup plc_preemphasis_mem")
	assertFloat32BitsEqual(t, gotPLCUpdate[:], want.celt48k.WarmupPLCUpdate[:], "warmup plc_update")
}

// The ordinary cached Decode(nil) path follows libopus FRAME_PLC_NEURAL,
// while the explicit DRED API follows FRAME_DRED. The bootstrap checks below
// exercise the explicit path; the decode parity tests compare it with the
// matching live libopus API.

func TestDecoderExplicitDREDFirstConcealFrameBootstraps48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < lpcnetplc.FrameSize {
		t.Fatalf("48 kHz bootstrap regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	if !requireDecoderDREDState(t, dec).dredPLC.GenerateConcealedFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
		t.Fatal("ConcealFrameFloatWithAnalysis returned false after 48 kHz bootstrap")
	}
}

func TestDecoderExplicitDREDThreeConcealFramesBootstraps48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Fatalf("48 kHz triple-frame regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	for i := 0; i < 3; i++ {
		if !requireDecoderDREDState(t, dec).dredPLC.GenerateConcealedFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
			t.Fatalf("GenerateConcealedFrameFloatWithAnalysis returned false at frame %d (plc=%+v fargan=%+v)", i, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
		}
	}
}

func TestDecoderExplicitDREDThreeConcealFramesManualStep48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Fatalf("48 kHz manual-step regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	if !requireDecoderDREDState(t, dec).dredPLC.PrimeFirstLossWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN) {
		t.Fatal("PrimeFirstLossWithAnalysis returned false")
	}
	var (
		frame    [lpcnetplc.FrameSize]float32
		features [lpcnetplc.NumFeatures]float32
	)
	for i := 0; i < 3; i++ {
		requireDecoderDREDState(t, dec).dredPLC.ConcealmentFeatureStep(&requireDecoderDREDState(t, dec).dredPredictor)
		if got := requireDecoderDREDState(t, dec).dredPLC.FillCurrentFeatures(features[:]); got != len(features) {
			t.Fatalf("FillCurrentFeatures()=%d want %d", got, len(features))
		}
		if got := requireDecoderDREDState(t, dec).dredFARGAN.Synthesize(frame[:], features[:]); got != lpcnetplc.FrameSize {
			t.Fatalf("Synthesize()=%d want %d at frame %d (fargan=%+v)", got, lpcnetplc.FrameSize, i, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
		}
		requireDecoderDREDState(t, dec).dredPLC.QueueFeatures(features[:])
		requireDecoderDREDState(t, dec).dredPLC.FinishConcealedFrameFloat(frame[:])
	}
}

func TestDecoderExplicitDREDThreeConcealFramesMixedHelpers48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Fatalf("48 kHz mixed-helper regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	if !requireDecoderDREDState(t, dec).dredPLC.ConcealFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
		t.Fatal("ConcealFrameFloatWithAnalysis(first) returned false")
	}
	for i := 1; i < 3; i++ {
		st := requireDecoderDREDState(t, dec)
		before := st.dredPLC.Snapshot()
		gotFEC := st.dredPLC.ConcealFrameFloat(&st.dredPredictor, &st.dredFARGAN, frame[:])
		wantFEC := before.FECReadPos != before.FECFillPos && before.FECSkip == 0
		if gotFEC != wantFEC {
			t.Fatalf("ConcealFrameFloat gotFEC=%v want %v at frame %d (fecRead=%d fecFill=%d fecSkip=%d)", gotFEC, wantFEC, i, before.FECReadPos, before.FECFillPos, before.FECSkip)
		}
		after := st.dredPLC.Snapshot()
		if after.AnalysisPos >= before.AnalysisPos {
			t.Fatalf("ConcealFrameFloat did not advance at frame %d (analysisPos=%d want <%d)", i, after.AnalysisPos, before.AnalysisPos)
		}
	}
}

func TestDecoderExplicitDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit libopus fargan")
}

// TestDecoderExplicitStereoDREDDecodeMatchesLibopus exercises the stereo DRED
// runtime against libopus. The neural signal is mono, while CELT stereo state
// and overlap crossfade still follow celt_decode_lost().
func TestDecoderExplicitStereoDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit DRED parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	// This forced-stereo carrier yields the libopus mono neural duplicate
	// shape, so both sides should stay bit-exact L=R for this fixture.
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo libopus fargan")
	assertDecodedPCMExactAndQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo libopus pcm")
}

// TestDecoderExplicitStereoDRED16kDecodeMatchesLibopus covers the same CELT
// stereo DRED path through the 16 kHz decoder API.
func TestDecoderExplicitStereoDRED16kDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// Force stereo at the libopus encoder control layer so this exercises a
	// real 16 kHz stereo carrier instead of the encoder's auto mono choice.
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("libopus dred emit helper produced mono TOC at 480-sample CELT FB despite forced channels (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit DRED 16k parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo 16k DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo 16k DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo 16k DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	// L=R interleaved invariant: both gopus and libopus must produce
	// L=R-duplicated stereo PCM for DRED concealment at 16 kHz, same as the
	// 48 kHz stereo case.
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("gopus stereo 16k DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo 16k DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo 16k libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo 16k libopus fargan")
	assertDecodedPCMExactAndQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo 16k libopus pcm")
}

func TestDecoderExplicitDREDDecode16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus 16k decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k libopus fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k libopus celt")
}

func TestDecoderPublicDecodeDRED16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name string
		cfg  libopusDREDPacketConfig
	}{
		{
			name: "celt_fb_mono",
			cfg: libopusDREDPacketConfig{
				FrameSize: 480,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			},
		},
		{
			name: "celt_fb_stereo",
			cfg: libopusDREDPacketConfig{
				FrameSize:     480,
				ForceMode:     ModeCELT,
				Bandwidth:     BandwidthFullband,
				Channels:      2,
				ForceChannels: 2,
			},
		},
		{
			name: "hybrid_swb_mono",
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus public 16k decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.Channels() {
				t.Fatalf("libopus public 16k decoder DRED channels=%d want %d", want.channels, dec.Channels())
			}

			pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}
			if gotDuration := dec.LastPacketDuration(); gotDuration != n {
				t.Fatalf("LastPacketDuration()=%d want API-rate frame %d", gotDuration, n)
			}

			gotPCM := pcm[:got*dec.Channels()]
			wantPCM := want.pcm[:got*dec.Channels()]
			if dec.Channels() == 2 {
				assertInterleavedStereoApproxDuplicated(t, gotPCM, got, "public 16k DecodeDRED", 1e-2)
				assertInterleavedStereoApproxDuplicated(t, wantPCM, got, "libopus public 16k DecodeDRED", 1e-2)
			}
			assertDecodedPCMExactAndQuality(t, gotPCM, wantPCM, dec.SampleRate(), dec.Channels(), "public 16k DecodeDRED pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public 16k DecodeDRED plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public 16k DecodeDRED fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "public 16k DecodeDRED celt")
		})
	}
}

func TestDecoderPublicDecodeDREDOverlongRequestMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	requested := overlongAPIRateRequestedFrameSize(dec.SampleRate())
	if requested <= dec.maxPacketSamples {
		t.Fatalf("requested=%d not over max packet samples %d", requested, dec.maxPacketSamples)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, dec.SampleRate(), -1, n, requested)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED overlong decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus public overlong DRED")
	if want.ret != requested {
		t.Fatalf("libopus public overlong DRED ret=%d want %d", want.ret, requested)
	}

	pcm := make([]float32, requested*dec.Channels())
	got, err := dec.DecodeDRED(dred, n, pcm, requested)
	if err != nil {
		t.Fatalf("DecodeDRED overlong error: %v", err)
	}
	if got != requested {
		t.Fatalf("DecodeDRED overlong=%d want %d", got, requested)
	}

	gotPCM := pcm[:got*dec.Channels()]
	wantPCM := want.pcm[:got*dec.Channels()]
	assertDecodedPCMExactAndQuality(t, gotPCM, wantPCM, dec.SampleRate(), dec.Channels(), "public overlong DecodeDRED pcm")
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusFirstLoss(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Fatalf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit first libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit second libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit second libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit second libopus fargan")
}

func TestDecoderExplicitDREDDecodeSecondLossGainTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	const gainA = 256
	const gainB = -512

	if err := dec.SetGain(gainA); err != nil {
		t.Fatalf("SetGain(%d) error: %v", gainA, err)
	}
	wantFirst, err := probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n, gainA)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, wantFirst, "libopus decoder DRED")
	if wantFirst.ret != n {
		t.Fatalf("libopus decoder DRED first ret=%d want %d", wantFirst.ret, n)
	}

	pcm0 := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(first)=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm0[:n], wantFirst.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit second-loss gain transition first libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), wantFirst.state, "explicit second-loss gain transition first libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), wantFirst.fargan, "explicit second-loss gain transition first libopus fargan")

	if err := dec.SetGain(gainB); err != nil {
		t.Fatalf("SetGain(%d) error: %v", gainB, err)
	}
	wantSecond, err := probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n, gainB)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, wantSecond, "libopus decoder DRED")
	if wantSecond.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", wantSecond.warmupRet, n)
	}
	if wantSecond.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", wantSecond.ret, n)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	got, err = dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm1[:n], wantSecond.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit second-loss gain transition second libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), wantSecond.state, "explicit second-loss gain transition second libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), wantSecond.fargan, "explicit second-loss gain transition second libopus fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, wantSecond.celt48k, "explicit second-loss gain transition second libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLoss16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus 16k decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus 16k decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k second libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second libopus fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k second libopus celt")
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusSecondLoss(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Fatalf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED second")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second decode ret=%d want %d", want.ret, n)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit second libopus celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Fatalf("48 kHz explicit follow-up parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit next packet pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit next packet plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit next packet fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit next packet celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus 16k decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k next packet pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k next packet plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k next packet fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k next packet celt")
}

func TestDecoderExplicitSecondLossThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus 16k decoder warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus 16k decoder second ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus 16k decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k second-loss next packet pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second-loss next packet plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second-loss next packet fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k second-loss next packet celt")
}

func TestDecoderExplicitSecondLossThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Fatalf("48 kHz explicit second-loss follow-up parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus decoder second-loss follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) after second loss error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet) after second loss=%d want %d", gotNext, want.nextRet)
	}

	assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit second-loss next packet pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit second-loss next packet plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit second-loss next packet fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit second-loss next packet celt")
}

func TestDecoderCachedStereoDREDHybridMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}
	flows := []cachedStereoDREDLiveFlow{
		cachedStereoDREDFirstLoss,
		cachedStereoDREDSecondLoss,
		cachedStereoDREDFirstLossThenNext,
		cachedStereoDREDSecondLossThenNext,
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			nextPacket := makeValidStereoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)
			for _, flow := range flows {
				flow := flow
				t.Run(flow.name(), func(t *testing.T) {
					assertDecoderCachedStereoDREDLiveSequenceMatchesLibopus(t, "cached stereo Hybrid "+tc.name+" "+flow.name(), libopusDREDPacketConfig{
						FrameSize: tc.frameSize,
						ForceMode: ModeHybrid,
						Bandwidth: tc.bandwidth,
					}, nextPacket, flow)
				})
			}
		})
	}
}

func TestDecoderCachedHybridDREDDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracle(t, "cached hybrid", packetInfo)
		})
	}
}

func TestDecoderCachedHybridDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached hybrid first-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "cached hybrid live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached hybrid next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached hybrid next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached hybrid next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached hybrid next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t, "cached hybrid", packetInfo)
		})
	}
}

func TestDecoderCachedHybridSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached hybrid second-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus cached hybrid decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached hybrid decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "cached hybrid live-sequence warmup pcm")

			pcm1 := make([]float32, dec.maxPacketSamples)
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "cached hybrid live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached hybrid second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached hybrid second-loss next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached hybrid second-loss next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "cached hybrid second-loss next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDRED16kDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid first-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid first-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "16k cached hybrid first-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "16k cached hybrid first-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "16k cached hybrid first-loss live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid first-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first-loss ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid first-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "API-rate cached hybrid first-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "API-rate cached hybrid first-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "API-rate cached hybrid first-loss live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid first-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first-loss ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus %d Hz cached hybrid decoder follow-up ret=%d want >0", tc.sampleRate, want.next.ret)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "API-rate cached hybrid next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "API-rate cached hybrid next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "API-rate cached hybrid next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid second-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first warmup ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder second-loss ret=%d want %d", tc.sampleRate, want.step1.ret, n)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid warmup live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "API-rate cached hybrid warmup live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "API-rate cached hybrid warmup live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "API-rate cached hybrid warmup live-sequence celt")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid second-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "API-rate cached hybrid second-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "API-rate cached hybrid second-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "API-rate cached hybrid second-loss live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid second-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first warmup ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder second-loss ret=%d want %d", tc.sampleRate, want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus %d Hz cached hybrid decoder follow-up ret=%d want >0", tc.sampleRate, want.next.ret)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid live-sequence warmup pcm")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "API-rate cached hybrid second-loss next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "API-rate cached hybrid second-loss next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "API-rate cached hybrid second-loss next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDRED16kThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid first-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus 16k cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "16k cached hybrid next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "16k cached hybrid next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "16k cached hybrid next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "16k cached hybrid next packet live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDRED16kSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid second-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder second-loss ret=%d want %d", want.step1.ret, n)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid warmup live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "16k cached hybrid warmup live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "16k cached hybrid warmup live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "16k cached hybrid warmup live-sequence celt")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid second-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "16k cached hybrid second-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "16k cached hybrid second-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "16k cached hybrid second-loss live-sequence celt")
		})
	}
}

func TestDecoderCachedHybridDRED16kSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid second-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus 16k cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid live-sequence warmup pcm")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "16k cached hybrid second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "16k cached hybrid second-loss next packet live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "16k cached hybrid second-loss next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.next.celt48k, "16k cached hybrid second-loss next packet live-sequence celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if packetInfo.sampleRate != 48000 || n != tc.frameSize {
				t.Fatalf("hybrid explicit parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", tc.frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecode16kMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz hybrid explicit frame=%d got %d want %d", tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeAPIRateMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz hybrid explicit frame=%d got %d want %d", tc.sampleRate, tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED decode ret=%d want %d", tc.sampleRate, want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "API-rate hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacketAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED decode ret=%d want %d", tc.sampleRate, want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus %d Hz hybrid decoder follow-up ret=%d want >0", tc.sampleRate, want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit next packet pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "API-rate hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossAPIRateMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED warmup ret=%d want %d", tc.sampleRate, want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED second ret=%d want %d", tc.sampleRate, want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit second loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "API-rate hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacketAPIRateMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED warmup ret=%d want %d", tc.sampleRate, want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED second ret=%d want %d", tc.sampleRate, want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus %d Hz hybrid decoder second-loss follow-up ret=%d want >0", tc.sampleRate, want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "API-rate hybrid explicit second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "hybrid explicit next packet pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid explicit next packet pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "hybrid explicit second loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLoss16kMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid explicit second loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacketMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "hybrid explicit second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "hybrid explicit second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacket16kMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid explicit second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid explicit second-loss follow-up celt")
		})
	}
}

// TestDecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus exercises the
// stereo DRED runtime path at 16 kHz against a Hybrid SWB carrier packet
// (10 ms / 480 samples) instead of CELT FB. Libopus leaves tiny L/R drift on
// this forced Hybrid seam, so the duplicate-shape check is numerical.
func TestDecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// Force stereo at the libopus encoder control layer so this exercises a
	// real 16 kHz stereo carrier instead of the encoder's auto mono choice.
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthSuperwideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("libopus dred emit helper produced mono TOC at 480-sample Hybrid SWB despite forced channels (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthSuperwideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit Hybrid DRED 16k parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo Hybrid 16k DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo Hybrid 16k DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo Hybrid 16k DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	const stereoHybridDuplicateTol = 3e-3
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d > stereoHybridDuplicateTol {
			t.Fatalf("gopus stereo Hybrid 16k DRED PCM not duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d > stereoHybridDuplicateTol {
			t.Fatalf("libopus stereo Hybrid 16k DRED PCM not duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo Hybrid 16k libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo Hybrid 16k libopus fargan")
	assertDecodedPCMExactAndQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo Hybrid 16k libopus pcm")
}

func TestDecoderExplicit16kHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid explicit libopus celt")
		})
	}
}

func TestDecoderPublicDecodeDREDHybridAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name       string
		sampleRate int
		cfg        libopusDREDPacketConfig
	}{
		{
			name:       "8k_hybrid_swb_mono",
			sampleRate: 8000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
		{
			name:       "12k_hybrid_swb_mono",
			sampleRate: 12000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
		{
			name:       "24k_hybrid_swb_mono",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
		{
			name:       "24k_hybrid_swb_stereo",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize:     960,
				ForceMode:     ModeHybrid,
				Bandwidth:     BandwidthSuperwideband,
				Channels:      2,
				ForceChannels: 2,
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public hybrid API-rate DRED")
			if want.ret != n {
				t.Fatalf("libopus public hybrid API-rate DRED ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.Channels() {
				t.Fatalf("libopus public hybrid API-rate DRED channels=%d want %d", want.channels, dec.Channels())
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}

			gotPCM := pcm[:got*dec.Channels()]
			wantPCM := want.pcm[:got*dec.Channels()]
			assertDecodedPCMExactAndQuality(t, gotPCM, wantPCM, dec.SampleRate(), dec.Channels(), "public hybrid API-rate DecodeDRED pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public hybrid API-rate DecodeDRED plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public hybrid API-rate DecodeDRED fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "public hybrid API-rate DecodeDRED celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixHybridSuperwidebandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthSuperwideband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder hybrid SWB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "hybrid swb offset matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "hybrid swb offset matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "hybrid swb offset matrix fargan")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixHybridFullbandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid FB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder hybrid FB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "hybrid fb offset matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "hybrid fb offset matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "hybrid fb offset matrix fargan")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrix16kHybridMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
	}{
		{name: "swb", bandwidth: BandwidthSuperwideband},
		{name: "fb", bandwidth: BandwidthFullband},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz hybrid offset frame=%d want %d", n, wantFrame)
			}
			boundary := -dred.Parsed().Header.OffsetSamples(16000)

			offsets := []struct {
				name       string
				dredOffset int
			}{
				{name: "before_first_feature_boundary", dredOffset: boundary - 1},
				{name: "at_first_feature_boundary", dredOffset: boundary},
				{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
				{name: "at_second_feature_boundary", dredOffset: boundary + n},
				{name: "late_offset", dredOffset: boundary + 2*n},
			}

			for _, offset := range offsets {
				offset := offset
				t.Run(offset.name, func(t *testing.T) {
					localDec, localDRED, localPacketInfo, seedPacket, localN := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
						FrameSize: 960,
						ForceMode: ModeHybrid,
						Bandwidth: tc.bandwidth,
					})
					if localPacketInfo.sampleRate != packetInfo.sampleRate || localN != n {
						t.Fatalf("local 16 kHz hybrid packet changed: sampleRate=%d frame=%d want sampleRate=%d frame=%d", localPacketInfo.sampleRate, localN, packetInfo.sampleRate, n)
					}
					want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, localPacketInfo, 16000, -1, offset.dredOffset, n)
					if err != nil {
						libopustest.HelperUnavailable(t, "decoder DRED decode", err)
					}
					requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
					if want.ret != n {
						t.Fatalf("libopus decoder 16k hybrid DRED decode ret=%d want %d", want.ret, n)
					}

					pcm := make([]float32, localDec.maxPacketSamples)
					got, err := localDec.decodeExplicitDREDFloat(localDRED, offset.dredOffset, pcm, n)
					if err != nil {
						t.Fatalf("decodeExplicitDREDFloat error: %v", err)
					}
					if got != n {
						t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
					}

					assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "16k hybrid offset matrix pcm")
					assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "16k hybrid offset matrix plc")
					assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid offset matrix fargan")
				})
			}
		})
	}
}

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

func assertDecodedPCMExactAndQuality(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM lengths got=%d want=%d", label, len(got), len(want))
	}
	assertFloat32BitsEqual(t, got, want, label+" PCM bits")
	assertDecodedPCMQuality(t, got, want, sampleRate, channels, label)
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

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "decoder dred decode", libopusDecoderDREDDecodeFloatOutputMagic, 6)
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
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]int32{info.silk.SLPCQ14[:], info.silk.ExcQ14[:], info.silk.ResamplerIIR[:]} {
		for i := range dst {
			dst[i] = reader.I32()
		}
	}
	for _, dst := range [][]float32{
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

func assertDecoderDREDPLCStateBitsMatch(t *testing.T, got, want lpcnetplc.StateSnapshot, label string) {
	t.Helper()
	if got.Blend != want.Blend ||
		got.LossCount != want.LossCount ||
		got.AnalysisGap != want.AnalysisGap ||
		got.AnalysisPos != want.AnalysisPos ||
		got.PredictPos != want.PredictPos ||
		got.FECReadPos != want.FECReadPos ||
		got.FECFillPos != want.FECFillPos ||
		got.FECSkip != want.FECSkip {
		t.Fatalf("%s header={blend:%d loss:%d gap:%d analysis:%d predict:%d fecRead:%d fecFill:%d fecSkip:%d} want {blend:%d loss:%d gap:%d analysis:%d predict:%d fecRead:%d fecFill:%d fecSkip:%d}",
			label,
			got.Blend, got.LossCount, got.AnalysisGap, got.AnalysisPos, got.PredictPos, got.FECReadPos, got.FECFillPos, got.FECSkip,
			want.Blend, want.LossCount, want.AnalysisGap, want.AnalysisPos, want.PredictPos, want.FECReadPos, want.FECFillPos, want.FECSkip,
		)
	}
	assertFloat32BitsEqual(t, got.Features[:lpcnetplc.NumFeatures], want.Features[:lpcnetplc.NumFeatures], label+" features")
	assertFloat32BitsEqual(t, got.Cont[:], want.Cont[:], label+" continuity")
	assertFloat32BitsEqual(t, got.PCM[:], want.PCM[:], label+" pcm history")
	assertFloat32BitsEqual(t, got.PLCNet.GRU1[:], want.PLCNet.GRU1[:], label+" plc net gru1")
	assertFloat32BitsEqual(t, got.PLCNet.GRU2[:], want.PLCNet.GRU2[:], label+" plc net gru2")
	assertFloat32BitsEqual(t, got.PLCBak[0].GRU1[:], want.PLCBak[0].GRU1[:], label+" plc bak0 gru1")
	assertFloat32BitsEqual(t, got.PLCBak[0].GRU2[:], want.PLCBak[0].GRU2[:], label+" plc bak0 gru2")
	assertFloat32BitsEqual(t, got.PLCBak[1].GRU1[:], want.PLCBak[1].GRU1[:], label+" plc bak1 gru1")
	assertFloat32BitsEqual(t, got.PLCBak[1].GRU2[:], want.PLCBak[1].GRU2[:], label+" plc bak1 gru2")
}

func assertDecoderDREDFARGANStateBitsMatch(t *testing.T, got, want lpcnetplc.FARGANSnapshot, label string) {
	t.Helper()
	if got.ContInitialized != want.ContInitialized || got.LastPeriod != want.LastPeriod {
		t.Fatalf("%s header=%+v want %+v", label, got, want)
	}
	if math.Float32bits(got.DeemphMem) != math.Float32bits(want.DeemphMem) {
		t.Fatalf("%s deemph=%f want %f", label, got.DeemphMem, want.DeemphMem)
	}
	assertFloat32BitsEqual(t, got.PitchBuf[:], want.PitchBuf[:], label+" pitch")
	assertFloat32BitsEqual(t, got.CondConv1State[:], want.CondConv1State[:], label+" cond")
	assertFloat32BitsEqual(t, got.FWC0Mem[:], want.FWC0Mem[:], label+" fwc0")
	assertFloat32BitsEqual(t, got.GRU1State[:], want.GRU1State[:], label+" gru1")
	assertFloat32BitsEqual(t, got.GRU2State[:], want.GRU2State[:], label+" gru2")
	assertFloat32BitsEqual(t, got.GRU3State[:], want.GRU3State[:], label+" gru3")
}

func assertDecoderDREDCELT48kBridgeBitsMatch(t *testing.T, dec *Decoder, want libopusDecoderDREDCELTSnapshot, label string) {
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
	assertFloat32BitsEqual(t, preemphMem[:], want.PreemphMem[:], label+" celt preemph_memD")
	if plcFill != want.PLCFill {
		t.Fatalf("%s fill=%d want %d (lastFrameType=%d plcDuration=%d skipPLC=%d preemph=%f)", label, plcFill, want.PLCFill, want.LastFrameType, want.PLCDuration, want.SkipPLC, want.PLCPreemphasisMem)
	}
	if math.Float32bits(plcPreemphasisMem) != math.Float32bits(want.PLCPreemphasisMem) {
		t.Fatalf("%s preemph=%f want %f", label, plcPreemphasisMem, want.PLCPreemphasisMem)
	}
	wantNeural := want.LastFrameType == libopusCELTFramePLCNeural || want.LastFrameType == libopusCELTFrameDRED
	if lastNeural != wantNeural {
		t.Fatalf("%s lastNeural=%v want %v (lastFrameType=%d)", label, lastNeural, wantNeural, want.LastFrameType)
	}
	assertFloat32BitsEqual(t, plcPCM[:], want.PLCPCM[:], label+" plc pcm")
}

func assertDecoderDREDSILKStateBitsMatch(t *testing.T, dec *Decoder, want libopusDecoderDREDSILKSnapshot, bandwidth silkpkg.Bandwidth, label string) {
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
	assertFloat32BitsEqual(t, got.SMid[:], want.SMid[:], label+" smid")
	assertFloat32BitsEqual(t, got.OutBuf[:], want.OutBuf[:], label+" outbuf")
	if got.SLPCQ14 != want.SLPCQ14 {
		t.Fatalf("%s slpc_q14 integer bits differ: Go=%v C=%v", label, got.SLPCQ14, want.SLPCQ14)
	}
	if got.ExcQ14 != want.ExcQ14 {
		t.Fatalf("%s exc_q14 integer bits differ", label)
	}
	if got.ResamplerIIR != want.ResamplerIIR {
		t.Fatalf("%s resampler IIR integer bits differ: Go=%v C=%v", label, got.ResamplerIIR, want.ResamplerIIR)
	}
	assertFloat32BitsEqual(t, got.ResamplerFIR[:], want.ResamplerFIR[:], label+" resampler fir")
	assertFloat32BitsEqual(t, got.ResamplerDelay[:], want.ResamplerDelay[:], label+" resampler delay")
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
		t.Fatalf("explicit DRED decode parity requires mono or stereo packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}
	if packetCfg.ForceMode != toc.Mode {
		t.Fatalf("explicit DRED decode parity requires mode=%v packet, got mode=%v", packetCfg.ForceMode, toc.Mode)
	}
	if packetCfg.Bandwidth != toc.Bandwidth {
		t.Fatalf("explicit DRED decode parity requires bandwidth=%v packet, got bandwidth=%v", packetCfg.Bandwidth, toc.Bandwidth)
	}
	decoderBlob := dredHistoryDecoderModelBlob(t)
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
		t.Fatal("carrier packet returned no audio")
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

	decoderBlob := dredHistoryDecoderModelBlob(t)
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
		t.Fatalf("cached DRED decode parity requires %d-channel packet, got sampleRate=%d channels=%d", wantChannels, packetInfo.sampleRate, channels)
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
// the given decoder-rate DRED offset. The result is compared with the
// SourceCarrierDRED oracle (opus_decoder_dred_decode_float).
func decodeCachedCarrierDREDViaExplicit(t *testing.T, dec *Decoder, dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) int {
	t.Helper()
	got, err := dec.decodeExplicitDREDFloat(dred, dredOffsetSamples, pcm, frameSizeSamples)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	return got
}

func assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracle(t *testing.T, label string, packetInfo libopusDREDPacket) {
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

	assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), label+" first-loss live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, label+" first-loss live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, label+" first-loss live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, label+" first-loss live-sequence celt")
}

func assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t *testing.T, label string, packetInfo libopusDREDPacket) {
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
	assertDecodedPCMExactAndQuality(t, pcm0[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), label+" warmup live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, label+" warmup live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, label+" warmup live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, label+" warmup live-sequence celt")

	pcm1 := make([]float32, n*dec.Channels())
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != n {
		t.Fatalf("%s explicit DRED decode(second)=%d want %d", label, got, n)
	}
	assertDecodedPCMExactAndQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), label+" second-loss live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, label+" second-loss live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, label+" second-loss live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, label+" second-loss live-sequence celt")
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
		t.Fatalf("%s cached stereo live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", label, packetCfg.FrameSize, packetInfo.sampleRate, n)
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

	assertDecodedPCMExactAndQuality(t, comparePCM, compareState.pcm[:compareSamples*dec.Channels()], packetInfo.sampleRate, dec.Channels(), compareLabel+" live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), compareState.state, compareLabel+" live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), compareState.fargan, compareLabel+" live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, compareState.celt48k, compareLabel+" live-sequence celt")
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

func TestDecoderSILKDecodeNilWithCachedDREDLossesMatchLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name              string
		decoderSampleRate int
	}{
		{name: "decoder_8000", decoderSampleRate: 8000},
		{name: "decoder_12000", decoderSampleRate: 12000},
		{name: "decoder_16000", decoderSampleRate: 16000},
		{name: "decoder_24000", decoderSampleRate: 24000},
		{name: "decoder_48000", decoderSampleRate: 48000},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 960
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			toc := ParseTOC(packetInfo.packet[0])
			if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
				t.Fatalf("cached SILK DRED test packet TOC=%+v, want SILK WB", toc)
			}

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.decoderSampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.decoderSampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, wantFrame, tc.decoderSampleRate)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.decoderSampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached SILK first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached SILK decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus cached SILK decoder channels=%d want 1", want.channels)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], tc.decoderSampleRate, dec.Channels(), "cached SILK live-sequence first-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK live-sequence first-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK live-sequence first-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached SILK live-sequence first-loss celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK live-sequence first-loss silk")

			decSecond, nSecond := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.decoderSampleRate, packetInfo)
			if nSecond != wantFrame {
				t.Fatalf("cached SILK second-loss warmup samples=%d want %d at %d Hz", nSecond, wantFrame, tc.decoderSampleRate)
			}
			wantSecond, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, nSecond, libopusDecoderDREDSequenceSourceLost, nSecond, libopusDecoderDREDSequenceSourceLost, 2*nSecond, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, wantSecond, "cached SILK second-loss")
			if wantSecond.step1.ret != nSecond {
				t.Fatalf("libopus cached SILK decoder second-loss ret=%d want %d", wantSecond.step1.ret, nSecond)
			}

			pcm0 := make([]float32, nSecond*decSecond.Channels())
			got0, err := decSecond.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, warmup) error: %v", err)
			}
			if got0 != nSecond {
				t.Fatalf("Decode(nil, warmup)=%d want %d", got0, nSecond)
			}
			pcm1 := make([]float32, nSecond*decSecond.Channels())
			got1, err := decSecond.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got1 != nSecond {
				t.Fatalf("Decode(nil, second)=%d want %d", got1, nSecond)
			}
			assertDecodedPCMExactAndQuality(t, pcm1[:got1], wantSecond.step1.pcm[:got1], tc.decoderSampleRate, decSecond.Channels(), "cached SILK live-sequence second-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, decSecond).dredPLC.Snapshot(), wantSecond.step1.state, "cached SILK live-sequence second-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, decSecond).dredFARGAN.Snapshot(), wantSecond.step1.fargan, "cached SILK live-sequence second-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, decSecond, wantSecond.step1.celt48k, "cached SILK live-sequence second-loss celt")
			assertDecoderDREDSILKStateBitsMatch(t, decSecond, wantSecond.step1.silk, silkpkg.BandwidthWideband, "cached SILK live-sequence second-loss silk")
		})
	}
}

func TestDecoderSILKDecodeNilWithCachedDREDMatchesLiveLostOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
		t.Fatalf("SILK DRED loss test packet TOC=%+v, want SILK WB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK lost sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "SILK public lost decode")
			if want.step0.ret != n {
				t.Fatalf("libopus SILK public lost ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus SILK public lost channels=%d want 1", want.channels)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			state := requireDecoderDREDState(t, dec)
			if state.dredRecovery != 0 {
				t.Fatalf("SILK public lost dredRecovery=%d want 0", state.dredRecovery)
			}
			if fill := state.dredPLC.FECFillPos(); fill != 0 {
				t.Fatalf("SILK public lost FECFillPos=%d want 0", fill)
			}
			if skip := state.dredPLC.FECSkip(); skip != 0 {
				t.Fatalf("SILK public lost FECSkip=%d want 0", skip)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "SILK public lost pcm")
			assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, "SILK public lost plc")
			assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, "SILK public lost fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "SILK public lost celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "SILK public lost silk")
		})
	}
}

func TestDecoderSILKDecodeNilWithCachedDREDRequestedPLCDurationMatchesLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "dred packet", err)
		}
		if toc := ParseTOC(packetInfo.packet[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("cached SILK DRED requested PLC packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
				for _, requested := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(fmt.Sprintf("request_%d", requested), func(t *testing.T) {
						dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, channels)
						packetFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
						if err != nil {
							t.Fatalf("packetSamplesAtRate: %v", err)
						}
						if n != packetFrame {
							t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, packetFrame, sampleRate)
						}

						want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, requested, libopusDecoderDREDSequenceSourceLost, requested, libopusDecoderDREDSequenceSourceNone, 0, false)
						if err != nil {
							libopustest.HelperUnavailable(t, "decoder DRED requested PLC sequence", err)
						}
						requireLibopusDREDSequenceParsed(t, want, "cached SILK requested PLC")
						if want.channels != channels {
							t.Fatalf("libopus cached SILK requested PLC channels=%d want %d", want.channels, channels)
						}
						if want.step0.ret != requested {
							t.Fatalf("libopus cached SILK requested PLC ret=%d want %d", want.step0.ret, requested)
						}

						pcm := make([]float32, requested*dec.Channels())
						got, err := dec.Decode(nil, pcm)
						if err != nil {
							t.Fatalf("Decode(nil) error: %v", err)
						}
						if got != requested {
							t.Fatalf("Decode(nil)=%d want %d", got, requested)
						}

						assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached SILK requested PLC live-sequence pcm")
						assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK requested PLC live-sequence plc")
						assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK requested PLC live-sequence fargan")
						assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached SILK requested PLC live-sequence celt")
						assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK requested PLC live-sequence silk")
					})
				}
			})
		}
	}
}

func TestDecoderSILKDecodeWithFECNoLBRRWithCachedDREDRequestedDurationMatchesLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "dred packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("cached SILK DRED FEC-fallback requested packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}
		firstFrameData, err := extractFirstFramePayload(packetInfo.packet, toc)
		if err != nil {
			t.Fatalf("extractFirstFramePayload: %v", err)
		}
		if packetHasLBRR(firstFrameData, toc) {
			t.Fatal("cached SILK DRED FEC-fallback fixture unexpectedly carries LBRR")
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
				for _, requested := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(fmt.Sprintf("request_%d", requested), func(t *testing.T) {
						dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, channels)
						packetFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
						if err != nil {
							t.Fatalf("packetSamplesAtRate: %v", err)
						}
						if n != packetFrame {
							t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, packetFrame, sampleRate)
						}

						want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, requested, libopusDecoderDREDSequenceSourceLost, requested, libopusDecoderDREDSequenceSourceNone, 0, false)
						if err != nil {
							libopustest.HelperUnavailable(t, "decoder DRED requested FEC-fallback sequence", err)
						}
						requireLibopusDREDSequenceParsed(t, want, "cached SILK requested FEC-fallback")
						if want.channels != channels {
							t.Fatalf("libopus cached SILK requested FEC-fallback channels=%d want %d", want.channels, channels)
						}
						if want.step0.ret != requested {
							t.Fatalf("libopus cached SILK requested FEC-fallback ret=%d want %d", want.step0.ret, requested)
						}

						pcm := make([]float32, requested*dec.Channels())
						got, err := dec.DecodeWithFEC(packetInfo.packet, pcm, true)
						if err != nil {
							t.Fatalf("DecodeWithFEC(no LBRR) error: %v", err)
						}
						if got != requested {
							t.Fatalf("DecodeWithFEC(no LBRR)=%d want %d", got, requested)
						}

						assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached SILK requested FEC-fallback live-sequence pcm")
						assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK requested FEC-fallback live-sequence plc")
						assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK requested FEC-fallback live-sequence fargan")
						assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached SILK requested FEC-fallback live-sequence celt")
						assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK requested FEC-fallback live-sequence silk")
					})
				}
			})
		}
	}
}

func TestDecoderSILKDecodeWithFECNoLBRRWithCachedDREDLossesMatchLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			const frameSize = 960
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			toc := ParseTOC(packetInfo.packet[0])
			if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
				t.Fatalf("cached SILK DRED FEC-fallback packet TOC=%+v, want SILK WB", toc)
			}
			firstFrameData, err := extractFirstFramePayload(packetInfo.packet, toc)
			if err != nil {
				t.Fatalf("extractFirstFramePayload: %v", err)
			}
			if packetHasLBRR(firstFrameData, toc) {
				t.Fatal("cached SILK DRED FEC-fallback fixture unexpectedly carries LBRR")
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceLost, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached SILK FEC-fallback first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached SILK FEC-fallback first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached SILK FEC-fallback second-loss ret=%d want %d", want.step1.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.DecodeWithFEC(packetInfo.packet, pcm, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC(no LBRR) error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeWithFEC(no LBRR)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.step0.pcm[:got], sampleRate, dec.Channels(), "cached SILK FEC-fallback live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK FEC-fallback live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK FEC-fallback live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached SILK FEC-fallback live-sequence celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK FEC-fallback live-sequence silk")

			pcmSecond := make([]float32, n*dec.Channels())
			gotSecond, err := dec.Decode(nil, pcmSecond)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if gotSecond != n {
				t.Fatalf("Decode(nil, second)=%d want %d", gotSecond, n)
			}
			assertDecodedPCMExactAndQuality(t, pcmSecond[:gotSecond], want.step1.pcm[:gotSecond], sampleRate, dec.Channels(), "cached SILK FEC-fallback second-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached SILK FEC-fallback second-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached SILK FEC-fallback second-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "cached SILK FEC-fallback second-loss live-sequence celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "cached SILK FEC-fallback second-loss live-sequence silk")
		})
	}
}

func TestDecoderCachedStereoSILKDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeSILK,
		Bandwidth:     BandwidthWideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo SILK DRED packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || !toc.Stereo {
		t.Fatalf("cached stereo SILK DRED packet TOC=%+v, want stereo SILK WB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, 2)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached stereo SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			// A public packet-loss Decode(nil) does not consume cached DRED:
			// libopus gates lpcnet_plc_fec_add on dred!=NULL (opus_decoder.c:736),
			// and opus_decode(NULL) passes dred==NULL, so SILK loss runs plain PLC.
			// This matches the mono SILK siblings and commit cc04ecf0; this stereo
			// case was overlooked there and still used the stale CarrierDRED source.
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceLost, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo SILK decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo SILK second-loss")
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo SILK DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n || want.step1.ret != n {
				t.Fatalf("libopus cached stereo SILK DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}

			assertInterleavedStereoApproxDuplicated(t, pcm0[:got*dec.Channels()], got, "cached stereo SILK first loss", 1e-2)
			assertInterleavedStereoApproxDuplicated(t, want.step0.pcm, got, "libopus cached stereo SILK first loss", 1e-2)
			assertDecodedPCMExactAndQuality(t, pcm0[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached stereo SILK first-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo SILK first-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo SILK first-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "cached stereo SILK first-loss live-sequence celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached stereo SILK first-loss live-sequence silk")

			pcm1 := make([]float32, n*dec.Channels())
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertInterleavedStereoApproxDuplicated(t, pcm1[:got*dec.Channels()], got, "cached stereo SILK second loss", 1e-2)
			assertInterleavedStereoApproxDuplicated(t, want.step1.pcm, got, "libopus cached stereo SILK second loss", 1e-2)
			assertDecodedPCMExactAndQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached stereo SILK second-loss live-sequence pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached stereo SILK second-loss live-sequence plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached stereo SILK second-loss live-sequence fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "cached stereo SILK second-loss live-sequence celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "cached stereo SILK second-loss live-sequence silk")
		})
	}
}

// TestDecoderExplicitSILKDREDDecodeMatchesLibopus exercises the SILK-only
// explicit DRED decode path against libopus. libopus routes SILK-only DRED
// through silk_Decode(lost_flag=1) with FEC features queued in lpcnet, where
// the SILK DeepPLC hook produces 16 kHz neural concealment and SILK upsamples
// to the API rate. The gopus equivalent (decodeExplicitSILKDREDFloat) installs
// the same DeepPLC hook around a standard PLC chunk decode after priming the
// LPCNet/FARGAN entry history from the prior SILK native lowband.
func TestDecoderExplicitSILKDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 48000, libopusDREDPacketConfig{
			FrameSize: 960,
			ForceMode: ModeSILK,
			Bandwidth: BandwidthWideband,
		})

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder SILK DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit silk libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit silk libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit silk libopus fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit silk libopus celt")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit silk libopus silk")
}

// TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus mirrors the 48 kHz SILK
// explicit DRED parity test at a 16 kHz decoder rate. SILK runs internally at
// 16 kHz so the 16 kHz API path skips the SILK->API upsampler entirely; the
// DeepPLC neural lowband is emitted at 16 kHz directly to the caller buffer.
// libopus's opus_decoder_dred_decode_float supports this path at any internal
// SR including 16 kHz.
func TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 16000, libopusDREDPacketConfig{
			FrameSize: 960,
			ForceMode: ModeSILK,
			Bandwidth: BandwidthWideband,
		})

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder 16k SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder 16k SILK DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k silk libopus pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k silk libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k silk libopus fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k silk libopus celt")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit 16k silk libopus silk")
}

func TestDecoderPublicSILKDREDAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name       string
		sampleRate int
		cfg        libopusDREDPacketConfig
	}{
		{
			name:       "8k_mono",
			sampleRate: 8000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			},
		},
		{
			name:       "12k_mono",
			sampleRate: 12000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			},
		},
		{
			name:       "24k_mono",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			},
		},
		{
			name:       "24k_stereo",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize:     960,
				ForceMode:     ModeSILK,
				Bandwidth:     BandwidthWideband,
				Channels:      2,
				ForceChannels: 2,
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK API-rate DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public SILK API-rate DRED")
			if want.ret != n {
				t.Fatalf("libopus public SILK API-rate DRED ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.Channels() {
				t.Fatalf("libopus public SILK API-rate DRED channels=%d want %d", want.channels, dec.Channels())
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "public SILK API-rate DecodeDRED pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public SILK API-rate DecodeDRED plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public SILK API-rate DecodeDRED fargan")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.silk, silkpkg.BandwidthWideband, "public SILK API-rate DecodeDRED silk")
		})
	}
}

// TestDecoderExplicitSILKDREDDecodeStereoMatchesLibopus mirrors
// TestDecoderExplicitSILKDREDDecodeMatchesLibopus for the stereo SILK DRED
// runtime path. libopus routes DRED through one lpcnet state on SILK channel 0
// and exposes duplicated L=R PCM on the API side for this fixture.
func TestDecoderExplicitSILKDREDDecodeStereoMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeSILK,
		Bandwidth:     BandwidthWideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo SILK DRED packet produced mono TOC at 960-sample WB (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 48000, libopusDREDPacketConfig{
			FrameSize:     960,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      2,
			ForceChannels: 2,
		})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit SILK DRED parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo SILK DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("gopus stereo SILK DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo SILK DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo SILK libopus plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo SILK libopus fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit stereo SILK libopus celt")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit stereo SILK libopus silk")
	assertDecodedPCMExactAndQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo SILK libopus pcm")
}

func TestDecodeDREDInt24TracksDecodeDREDFloat(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{960, 480} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			decFloat, dredFloat, _, _, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			decInt24, dredInt24, _, _, _ := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)

			channels := decFloat.Channels()
			needed := n * channels

			pcmFloat := make([]float32, needed)
			gotFloat, err := decFloat.DecodeDRED(dredFloat, n, pcmFloat, n)
			if err != nil {
				t.Fatalf("DecodeDRED float error: %v", err)
			}
			if gotFloat != n {
				t.Fatalf("DecodeDRED float returned %d want %d", gotFloat, n)
			}

			pcmInt24 := make([]int32, needed)
			gotInt24, err := decInt24.DecodeDREDInt24(dredInt24, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if gotInt24 != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", gotInt24, n)
			}

			wantInt24 := make([]int32, needed)
			for i := 0; i < gotFloat*channels; i++ {
				wantInt24[i] = float32ToInt24(pcmFloat[i])
			}
			for i := 0; i < gotFloat*channels; i++ {
				if pcmInt24[i] != wantInt24[i] {
					t.Fatalf("pcmInt24[%d]=%d want float32ToInt24(%g)=%d",
						i, pcmInt24[i], pcmFloat[i], wantInt24[i])
				}
			}
		})
	}
}

// TestDecodeDREDInt24OracleQuality verifies that Decoder.DecodeDREDInt24
// output — converted back to float32 via int24/8388608 — meets the same
// near-exact quality bar against the libopus opus_decoder_dred_decode_float
// oracle as the float DRED decode tests.
//
// This mirrors the oracle-based CELT DRED float tests. The oracle PCM comes
// from the SourceCarrierDRED sequence (opus_decoder_dred_decode_float called
// in the same decoder context as after decoding the carrier packet), and
// assertDecodedPCMQuality verifies the decoded audio is near-exact.
//
// The int24 quantisation ≤1 LSB rounding is sub-perceptual: it cannot affect
// the quality score at the near-exact bar.
func TestDecodeDREDInt24OracleQuality(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{960, 480} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)

			// Oracle: opus_decoder_dred_decode_float in carrier-DRED context.
			want, err := probeLibopusDecoderDREDSequence(
				nil, packetInfo.packet, nil,
				packetInfo.maxDREDSamples, packetInfo.sampleRate,
				n, libopusDecoderDREDSequenceSourceCarrierDRED,
				n, libopusDecoderDREDSequenceSourceNone, 0, false,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "DRED int24 oracle quality")
			if want.step0.ret != n {
				t.Fatalf("oracle DRED sequence step0.ret=%d want %d", want.step0.ret, n)
			}

			channels := dec.Channels()
			pcmInt24 := make([]int32, n*channels)
			got, err := dec.DecodeDREDInt24(dred, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", got, n)
			}

			// Convert int24 output back to float32 for quality comparison.
			gotFloat := int32Int24ToFloat32(pcmInt24[:got*channels])
			label := fmt.Sprintf("DRED int24 oracle quality frame_size=%d", frameSize)
			assertDecodedPCMQuality(t, gotFloat, want.step0.pcm[:got*channels], packetInfo.sampleRate, channels, label)
		})
	}
}

// TestDecodeDREDInt24MatchesLibopusInt24Reference compares the public Go
// int24 output with opus_decoder_dred_decode24() from the selected C build.
func TestDecodeDREDInt24MatchesLibopusInt24Reference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{960, 480} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)

			// The helper runs the carrier decode and DRED decode through opus_decode24
			// and opus_decoder_dred_decode24 on the same persistent decoder state.
			want, err := probeLibopusDecoderDREDSequenceInt24(
				nil, packetInfo.packet, nil,
				packetInfo.maxDREDSamples, packetInfo.sampleRate,
				n, libopusDecoderDREDSequenceSourceCarrierDRED,
				n, libopusDecoderDREDSequenceSourceNone, 0, false,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "DRED int24 reference")
			if want.step0.ret != n {
				t.Fatalf("oracle DRED sequence step0.ret=%d want %d", want.step0.ret, n)
			}

			channels := dec.Channels()
			needed := n * channels
			pcmInt24 := make([]int32, needed)
			got, err := dec.DecodeDREDInt24(dred, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", got, n)
			}

			if len(want.step0.pcm24) != got*channels {
				t.Fatalf("libopus int24 sample count=%d want %d", len(want.step0.pcm24), got*channels)
			}

			label := fmt.Sprintf("DRED int24 reference frame_size=%d", frameSize)
			assertInt24ParitySelectedCExact(t, pcmInt24[:got*channels], want.step0.pcm24, packetInfo.sampleRate, channels, label)
		})
	}
}

// TestDecodeDREDInt24MatrixMatchesLibopusInt24Reference extends the int24 DRED
// recovery parity coverage from the single CELT-FB fixture exercised by
// TestDecodeDREDInt24MatchesLibopusInt24Reference to the full DRED mode/rate/
// channel matrix already gated for the float DRED decode path: SILK-WB, CELT-FB
// and Hybrid-SWB/FB, mono and stereo, at 48 kHz and 16 kHz decoder rates.
//
// For each mode, rate, and channel combination, the same-format C helper decodes
// the seed with opus_decode24(), parses the carrier DRED payload, and calls
// opus_decoder_dred_decode24() without decoding the carrier. This matches the
// explicit Go DRED path exercised here, which starts from the seeded decoder.
func TestDecodeDREDInt24MatrixMatchesLibopusInt24Reference(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name         string
		decoderRate  int
		cfg          libopusDREDPacketConfig
		wantChannels int
	}{
		{
			name:        "celt_fb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		},
		{
			name:        "celt_fb_mono_48k_10ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		},
		{
			name:         "celt_fb_stereo_48k_20ms",
			decoderRate:  48000,
			cfg:          libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, Channels: 2, ForceChannels: 2},
			wantChannels: 2,
		},
		{
			name:        "celt_fb_mono_16k_20ms",
			decoderRate: 16000,
			cfg:         libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		},
		{
			name:         "celt_fb_stereo_16k_20ms",
			decoderRate:  16000,
			cfg:          libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, Channels: 2, ForceChannels: 2},
			wantChannels: 2,
		},
		{
			name:        "silk_wb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband},
		},
		{
			name:        "silk_wb_mono_16k_20ms",
			decoderRate: 16000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband},
		},
		{
			name:         "silk_wb_stereo_48k_20ms",
			decoderRate:  48000,
			cfg:          libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2, ForceChannels: 2},
			wantChannels: 2,
		},
		{
			name:        "hybrid_swb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthSuperwideband},
		},
		{
			name:        "hybrid_fb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthFullband},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.decoderRate, tc.cfg)

			wantChannels := tc.wantChannels
			if wantChannels == 0 {
				wantChannels = 1
			}
			if dec.Channels() != wantChannels {
				t.Fatalf("int24 DRED matrix got decoder channels=%d, want %d", dec.Channels(), wantChannels)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.decoderRate)
			want, err := probeLibopusDecoderDREDSequenceInt24WithoutCarrierDecode(
				seedPacket, packetInfo.packet, nil,
				maxDRED, oracleRate, n,
				libopusDecoderDREDSequenceSourceCarrierDRED, n,
				libopusDecoderDREDSequenceSourceNone, 0, false,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED int24 decode", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "libopus int24 DRED matrix")
			if want.step0.ret != n {
				t.Fatalf("libopus int24 DRED matrix decode ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != wantChannels {
				t.Fatalf("libopus int24 DRED matrix channels=%d want %d", want.channels, wantChannels)
			}

			channels := dec.Channels()
			needed := n * channels
			pcmInt24 := make([]int32, needed)
			got, err := dec.DecodeDREDInt24(dred, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", got, n)
			}

			label := "DRED int24 matrix reference " + tc.name
			if len(want.step0.pcm24) != got*channels {
				t.Fatalf("%s sample count=%d want %d", label, len(want.step0.pcm24), got*channels)
			}
			for i, sample := range pcmInt24[:got*channels] {
				if sample != want.step0.pcm24[i] {
					t.Fatalf("%s sample[%d]=%d want selected C %d", label, i, sample, want.step0.pcm24[i])
				}
			}
			assertInt24ParitySelectedCExact(t, pcmInt24[:got*channels], want.step0.pcm24, tc.decoderRate, channels, label)
		})
	}
}

func TestDecodeDREDCarrierHistoryCodecStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband}
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, cfg)
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	before, err := probeLibopusDecoderDREDSequence(seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceNone, 0,
		libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED carrier pre-state", err)
	}
	requireLibopusDREDSequenceParsed(t, before, "libopus carrier pre-state")
	want, err := probeLibopusDecoderDREDSequenceWithSampleFormats(seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceCarrierDRED, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatInt24,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED carrier history state", err)
	}
	pcm := make([]float32, dec.maxPacketSamples*dec.Channels())
	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("Decode(carrier packet): %v", err)
	}
	if got := dec.FinalRange(); got != before.step0.finalRange {
		t.Fatalf("carrier pre-DRED final range=%08x want selected C %08x", got, before.step0.finalRange)
	}
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, before.step0.celt48k, "carrier pre-DRED CELT")
	assertDecoderDREDSILKStateBitsMatch(t, dec, before.step0.silk, silkpkg.BandwidthWideband, "carrier pre-DRED SILK")
	if !dec.dredNeuralConcealmentReady() {
		t.Fatal("DRED neural runtime did not initialize")
	}
	dec.primeExplicitSILKDREDEntryHistory()
	primed := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, primed.dredPLC.Snapshot(), before.step0.state, "carrier primed pre-queue PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, primed.dredFARGAN.Snapshot(), before.step0.fargan, "carrier primed pre-queue FARGAN")
	queued, err := probeLibopusDecoderDREDSequenceWithSampleFormats(seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceQueueCarrierDRED, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatInt24,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED queued carrier state", err)
	}
	dec.queueExplicitDREDRecovery(dred, n, n)
	queuedState := requireDecoderDREDState(t, dec)
	gotQueued := queuedState.dredPLC.Snapshot()
	assertDecoderDREDPLCStateBitsMatch(t, gotQueued, queued.step0.state, "carrier queued PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, queuedState.dredFARGAN.Snapshot(), queued.step0.fargan, "carrier queued FARGAN")
	if gotQueued.FECFillPos != queued.step0.state.FECFillPos || gotQueued.FECSkip != queued.step0.state.FECSkip {
		t.Fatalf("carrier queued feature counts Go(fill=%d skip=%d) C(fill=%d skip=%d)", gotQueued.FECFillPos, gotQueued.FECSkip, queued.step0.state.FECFillPos, queued.step0.state.FECSkip)
	}
	for i := 0; i < gotQueued.FECFillPos; i++ {
		assertFloat32BitsEqual(t, gotQueued.FEC[i][:], queued.step0.state.FEC[i][:], fmt.Sprintf("carrier queued feature[%d]", i))
	}
	pcm24 := make([]int32, n*dec.Channels())
	if _, err := dec.DecodeDREDInt24(dred, n, pcm24, n); err != nil {
		t.Fatalf("DecodeDREDInt24 after carrier: %v", err)
	}
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, "carrier-history DRED PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, "carrier-history DRED FARGAN")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "carrier history CELT")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "carrier history SILK")
	if got := dec.FinalRange(); got != want.step0.finalRange {
		t.Fatalf("carrier DRED final range=%08x want selected C %08x", got, want.step0.finalRange)
	}
	if len(want.step0.pcm24) != len(pcm24) {
		t.Fatalf("carrier DRED int24 sample count=%d want %d", len(pcm24), len(want.step0.pcm24))
	}
	for i := range pcm24 {
		if pcm24[i] != want.step0.pcm24[i] {
			t.Fatalf("carrier DRED int24 sample[%d]=%d want selected C %d", i, pcm24[i], want.step0.pcm24[i])
		}
	}
}

// TestDecodeDREDInt24MatrixTracksDecodeDREDFloat verifies that across the full
// DRED mode/rate/channel matrix DecodeDREDInt24 equals RES2INT24 applied to the
// gopus float DRED decode (DecodeDRED) sample-for-sample. Both run the identical
// decodeExplicitDREDFloat float compute on the same host, so this is a strict
// byte/sample-exact equality on every architecture — it is independent of the
// libopus oracle and of the documented arm64 1-ULP float drift. It locks the
// int24 quantisation wiring (float32ToInt24Slice == per-sample RES2INT24) for
// the whole matrix rather than only the CELT-FB fixture.
func TestDecodeDREDInt24MatrixTracksDecodeDREDFloat(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name        string
		decoderRate int
		cfg         libopusDREDPacketConfig
	}{
		{name: "celt_fb_mono_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband}},
		{name: "celt_fb_stereo_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, Channels: 2, ForceChannels: 2}},
		{name: "celt_fb_mono_16k_20ms", decoderRate: 16000, cfg: libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband}},
		{name: "silk_wb_mono_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband}},
		{name: "silk_wb_mono_16k_20ms", decoderRate: 16000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband}},
		{name: "silk_wb_stereo_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2, ForceChannels: 2}},
		{name: "hybrid_swb_mono_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthSuperwideband}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Two decoders seeded identically advance through the same DRED parse
			// and process steps; DecodeDRED produces the float reference,
			// DecodeDREDInt24 must equal float32ToInt24(floatPCM) sample-for-sample.
			decFloat, dredFloat, _, _, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.decoderRate, tc.cfg)
			decInt24, dredInt24, _, _, n2 := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.decoderRate, tc.cfg)
			if n != n2 {
				t.Fatalf("seed frame count mismatch: float=%d int24=%d", n, n2)
			}

			channels := decFloat.Channels()
			needed := n * channels

			pcmFloat := make([]float32, needed)
			gotFloat, err := decFloat.DecodeDRED(dredFloat, n, pcmFloat, n)
			if err != nil {
				t.Fatalf("DecodeDRED float error: %v", err)
			}
			if gotFloat != n {
				t.Fatalf("DecodeDRED float returned %d want %d", gotFloat, n)
			}

			pcmInt24 := make([]int32, needed)
			gotInt24, err := decInt24.DecodeDREDInt24(dredInt24, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if gotInt24 != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", gotInt24, n)
			}

			for i := 0; i < gotFloat*channels; i++ {
				want := float32ToInt24(pcmFloat[i])
				if pcmInt24[i] != want {
					t.Fatalf("%s: pcmInt24[%d]=%d want float32ToInt24(%g)=%d",
						tc.name, i, pcmInt24[i], pcmFloat[i], want)
				}
			}
		})
	}
}

// TestDecodeDREDInt24BufferTooSmall verifies that DecodeDREDInt24 returns
// ErrBufferTooSmall when the output buffer is too small.
func TestDecodeDREDInt24BufferTooSmall(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, _, _, n := prepareExplicitDREDDecodeParityState(t)
	channels := dec.Channels()

	if _, err := dec.DecodeDREDInt24(dred, n, make([]int32, n*channels-1), n); err != ErrBufferTooSmall {
		t.Fatalf("DecodeDREDInt24(short buf) error=%v want %v", err, ErrBufferTooSmall)
	}
}

// TestDecodeDREDInt24InvalidFrameSize verifies that DecodeDREDInt24 returns
// ErrInvalidArgument for a zero frame size.
func TestDecodeDREDInt24InvalidFrameSize(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, _, _, n := prepareExplicitDREDDecodeParityState(t)
	channels := dec.Channels()

	if _, err := dec.DecodeDREDInt24(dred, n, make([]int32, n*channels), 0); err != ErrInvalidArgument {
		t.Fatalf("DecodeDREDInt24(frameSize=0) error=%v want %v", err, ErrInvalidArgument)
	}
}

func TestDecoderCoreDNNBlobDoesNotArmGoodPacketDREDWork(t *testing.T) {
	requireDREDRuntimeForTest(t)

	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("decoder did not retain neural model readiness")
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("core decoder SetDNNBlob armed standalone DRED payload scanning")
	}
	if dec.dredGoodPacketMarkerActive() {
		t.Fatal("core decoder SetDNNBlob armed good-packet DRED marker work")
	}

	packet := testCELTPacket()
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode(good packet) error: %v", err)
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("good packet armed standalone DRED payload scanning")
	}
	if dec.dredGoodPacketMarkerActive() {
		t.Fatal("good packet armed DRED marker work without payload/recovery state")
	}
	if state := dec.dredState(); state != nil {
		t.Fatalf("good packet with core DNN blob woke DRED sidecar: %+v", state)
	}
}

func TestNewDecoderLeavesDREDPayloadBufferDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if s := dec.dredState(); s != nil && len(s.dredData) != 0 {
		t.Fatalf("len(dredData)=%d want 0 before standalone DRED arm", len(s.dredData))
	}

	setValidDREDDecoderBlobForTest(t, dec)
	if got := len(requireDecoderDREDState(t, dec).dredData); got != 0 {
		t.Fatalf("len(dredData)=%d want 0 after standalone DRED arm before any cached payload", got)
	}

	dec.setDREDDecoderBlob(nil)
	if s := dec.dredState(); s != nil && len(s.dredData) != 0 {
		t.Fatalf("len(dredData)=%d want 0 after standalone DRED clear", len(s.dredData))
	}
}

func TestStandaloneDREDArmKeepsRecoveryNeuralAnd48kBridgeDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	setValidDREDDecoderBlobForTest(t, dec)

	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDPayloadState == nil {
		t.Fatal("standalone DRED arm did not retain payload state")
	}
	if state.decoderDREDRecoveryState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
	}
	if state.decoderDREDNeuralState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated neural state: %+v", state.decoderDREDNeuralState)
	}
	if state.decoderDRED48kBridgeState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated 48k bridge state: %+v", state.decoderDRED48kBridgeState)
	}
}

func TestMainDecoderDNNBlobKeepsRecoveryAndPayloadDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("main decoder DNN blob did not retain neural model readiness")
	}
	if dec.dredNeuralRuntimeLoaded() {
		t.Fatal("main decoder DNN blob eagerly loaded neural runtime")
	}
	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("main decoder DNN blob eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("main decoder DNN blob eagerly allocated neural recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("main decoder DNN blob eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("16 kHz decoder eagerly allocated 48k bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder48kDNNBlobKeepsRecoveryAndBridgeDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("48 kHz decoder DNN blob did not retain neural model readiness")
	}
	if dec.dredNeuralRuntimeLoaded() {
		t.Fatal("48 kHz decoder DNN blob eagerly loaded neural runtime")
	}
	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated neural recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder16kDNNBlobGoodDecodeKeepsRecoveryDormantUntilLoss(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("good decode eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("good decode eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("good decode eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("good decode eagerly allocated 48k bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder48kDNNBlobGoodDecodeKeepsRecoveryDormantUntilLoss(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder48kDNNBlobGoodSILKHybridDecodeKeepsRecoveryDormantUntilLoss(t *testing.T) {
	tests := []struct {
		name   string
		packet func(*testing.T) []byte
	}{
		{name: "silk", packet: makeValidMono48kSILKPacketForDREDTest},
		{name: "hybrid", packet: func(t *testing.T) []byte {
			return makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthFullband)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			if _, err := dec.Decode(tt.packet(t), pcm); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if state := dec.dredState(); state != nil {
				t.Fatalf("48 kHz %s good decode eagerly allocated DRED sidecar: %+v", tt.name, state)
			}
		})
	}
}

func TestClearingStandaloneDREDPreservesMainNeuralState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%23)-11) / 23
	}
	dec.ensureDREDRecoveryState()
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)
	before := requireDecoderDREDState(t, dec).dredPLC.Snapshot()

	setValidDREDDecoderBlobForTest(t, dec)
	dec.setDREDDecoderBlob(nil)

	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDPayloadState != nil {
		t.Fatalf("clearing standalone DRED left payload state behind: %+v", state.decoderDREDPayloadState)
	}
	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("clearing standalone DRED dropped main neural model readiness")
	}
	if state.decoderDREDRecoveryState == nil {
		t.Fatalf("clearing standalone DRED dropped retained recovery state: %+v", state)
	}
	after := state.dredPLC.Snapshot()
	if after.AnalysisPos != before.AnalysisPos || after.PredictPos != before.PredictPos || after.Blend != before.Blend {
		t.Fatalf("clearing standalone DRED reset neural PLC history: before=%+v after=%+v", before, after)
	}
}

func TestDecoderResetDropsActivatedDREDRuntimeBackToDormant(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !dec.dredNeuralConcealmentReady() {
		t.Fatal("decoder failed to materialize neural concealment runtime")
	}
	if dec.dredRecoveryState() == nil || dec.dredNeuralState() == nil {
		t.Fatalf("decoder runtime did not materialize fully: %+v", dec.dredState())
	}

	dec.Reset()
	if dec.dnnBlob == nil || !dec.dredNeuralModelsLoaded() {
		t.Fatal("Reset cleared retained decoder DNN control state")
	}
	if dec.dredRecoveryState() != nil {
		t.Fatalf("Reset left DRED recovery runtime live: %+v", dec.dredState())
	}
	if dec.dredNeuralState() != nil {
		t.Fatalf("Reset left DRED neural runtime live: %+v", dec.dredState())
	}
	if dec.dred48kBridgeState() != nil {
		t.Fatalf("Reset left DRED 48k bridge runtime live: %+v", dec.dredState())
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode(good packet) after Reset error: %v", err)
	}
	if dec.dredState() != nil {
		t.Fatalf("good decode after Reset eagerly reawakened DRED sidecar: %+v", dec.dredState())
	}
}

func TestDecoderSetDNNBlobPreservesActive48kBridge(t *testing.T) {
	requireDREDRuntimeForTest(t)

	dec := mustNewTestDecoder(t, 48000, 1)
	blob := makeValidDecoderTestDNNBlob()
	if err := dec.SetDNNBlob(blob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !dec.dredNeuralConcealmentReady() {
		t.Fatal("decoder failed to materialize neural concealment runtime")
	}
	bridge := dec.dred48kBridgeState()
	if bridge == nil {
		t.Fatalf("decoder did not materialize 48 kHz bridge: %+v", dec.dredState())
	}

	bridge.dredPLCPCM[0] = 4096
	bridge.dredPLCFill = 37
	bridge.dredPLCPreemphMem = 0.25
	bridge.dredLastNeural = true

	state := requireDecoderDREDState(t, dec)
	var farganPCM [lpcnetplc.FARGANContSamples]float32
	var farganFeatures [lpcnetplc.ContVectors * lpcnetplc.NumFeatures]float32
	for i := range farganPCM {
		farganPCM[i] = float32((i%31)-15) / 19
	}
	for i := range farganFeatures {
		farganFeatures[i] = float32((i%17)-8) / 11
	}
	if n := state.dredFARGAN.PrimeContinuity(farganPCM[:], farganFeatures[:]); n != lpcnetplc.FARGANContSamples {
		t.Fatalf("PrimeContinuity()=%d want %d", n, lpcnetplc.FARGANContSamples)
	}
	beforeFARGAN := state.dredFARGAN.Snapshot()

	// libopus reloads the model in-place for OPUS_SET_DNN_BLOB; it does not
	// reset active PLC/DRED bridge state on a successful reload.
	if err := dec.SetDNNBlob(blob); err != nil {
		t.Fatalf("SetDNNBlob(reload) error: %v", err)
	}
	got := dec.dred48kBridgeState()
	if got != bridge {
		t.Fatalf("SetDNNBlob(reload) replaced 48 kHz bridge: before=%p after=%p", bridge, got)
	}
	if got.dredPLCPCM[0] != 4096 ||
		got.dredPLCFill != 37 ||
		got.dredPLCPreemphMem != 0.25 ||
		!got.dredLastNeural {
		t.Fatalf("SetDNNBlob(reload) reset 48 kHz bridge: %+v", got)
	}
	if afterFARGAN := requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(); afterFARGAN != beforeFARGAN {
		t.Fatalf("SetDNNBlob(reload) reset FARGAN state: before=%+v after=%+v", beforeFARGAN, afterFARGAN)
	}
}

func TestDecoderPrimeDREDCELTEntryHistoryStaysDormantWithoutNeuralConcealment(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.primeDREDCELTEntryHistory(ModeCELT, false); got != 0 {
		t.Fatalf("primeDREDCELTEntryHistory()=%d want 0", got)
	}
	if dec.dredState() != nil {
		t.Fatalf("dred sidecar awakened without neural concealment readiness: %+v", dec.dredState())
	}
}

func TestDecoderLeavesDREDPayloadDormantWithoutDREDModel(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder cached dormant DRED sidecar=%+v want nil", dec.dredState())
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples without model=%d want 0", got)
	}
}

func TestDecoderLeavesDREDStateDormantWithoutAnySidecar(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar=%+v want nil", dec.dredState())
	}

	n, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode(nil) returned zero samples")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar after PLC=%+v want nil", dec.dredState())
	}
}

func TestPublicDecoderSetDNNBlobDoesNotArmDREDDecoderWhenBlobContainsModel(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderControlWithDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("public decoder SetDNNBlob armed standalone DRED payload scanning")
	}

	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(extended, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.dredCachedPayloadActive() {
		t.Fatal("public decoder SetDNNBlob cached DRED payload without standalone decoder state")
	}
	if state := dec.dredState(); state != nil && state.decoderDREDPayloadState != nil {
		t.Fatalf("public decoder SetDNNBlob woke DRED payload sidecar: %+v", state.decoderDREDPayloadState)
	}
}

func TestDecoderLeavesDREDPayloadDormantWhenIgnoringExtensions(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)
	dec.SetIgnoreExtensions(true)

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	if got := requireDecoderDREDState(t, dec).dredCache; got != (internaldred.Cache{}) {
		t.Fatalf("decoder cached ignored DRED cache=%+v want zero state", got)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples while ignoring=%d want 0", got)
	}
}

func TestDecoderCachedCELTDRED16kExplicitMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband {
		t.Fatalf("CELT 16k DRED packet TOC=%+v, want CELT FB", toc)
	}

	const decoderRate = 16000
	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, decoderRate, packetInfo)
	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}
	if n != wantFrame {
		t.Fatalf("CELT 16k warmup samples=%d want %d", n, wantFrame)
	}

	dred := parseCarrierDREDForExplicitDecode(t, decoderRate, packetInfo)
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderRate)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "CELT 16k first-loss")
	if want.step0.ret != n {
		t.Fatalf("libopus CELT 16k first-loss ret=%d want %d", want.step0.ret, n)
	}

	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], decoderRate, dec.Channels(), "CELT 16k explicit live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "CELT 16k explicit live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "CELT 16k explicit live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "CELT 16k explicit live-sequence celt")
}

// TestDecoderCachedStereoCELTDRED16kExplicitMatchesLiveSequenceOracle gates the
// stereo CELT explicit-decode path at the 16 kHz decoder API rate.  The
// TestDecoderCachedStereoCELTDREDAPIRateMatchesLiveSequenceOracle already covers
// 16 kHz stereo, but this test targets the same cell via the sequence oracle so
// it closes the mono/stereo 16 kHz CELT explicit-decode row.
func TestDecoderCachedStereoCELTDRED16kExplicitMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT 16k dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband || !toc.Stereo {
		t.Fatalf("stereo CELT 16k DRED packet TOC=%+v, want stereo CELT FB", toc)
	}

	const decoderRate = 16000
	const wantChannels = 2
	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderRate, packetInfo, wantChannels)
	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}
	if n != wantFrame {
		t.Fatalf("stereo CELT 16k warmup samples=%d want %d", n, wantFrame)
	}

	dred := parseCarrierDREDForExplicitDecode(t, decoderRate, packetInfo)
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderRate)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT 16k decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "stereo CELT 16k first-loss")
	if want.channels != wantChannels {
		t.Fatalf("libopus stereo CELT 16k DRED channels=%d want %d", want.channels, wantChannels)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus stereo CELT 16k first-loss ret=%d want %d", want.step0.ret, n)
	}

	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("stereo CELT 16k explicit DRED decode=%d want %d", got, n)
	}

	assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], decoderRate, dec.Channels(), "stereo CELT 16k explicit live-sequence pcm")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "stereo CELT 16k explicit live-sequence plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "stereo CELT 16k explicit live-sequence fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "stereo CELT 16k explicit live-sequence celt")
}

// hybrid16kExplicitDREDCases returns the 16 kHz decoder-rate hybrid test cases
// for the explicit decodeExplicitDREDFloat battery. These are the 16 kHz
// equivalents of hybridDREDAPIRateCases() and close the "16 kHz hybrid explicit
// decode" cell in the DRED parity matrix.
func hybrid16kExplicitDREDCases() []hybridDREDAPIRateCase {
	return []hybridDREDAPIRateCase{
		{name: "16k_swb_10ms", sampleRate: 16000, bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "16k_swb_20ms", sampleRate: 16000, bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "16k_fb_10ms", sampleRate: 16000, bandwidth: BandwidthFullband, frameSize: 480},
		{name: "16k_fb_20ms", sampleRate: 16000, bandwidth: BandwidthFullband, frameSize: 960},
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateFirstLossMatchesLibopus gates the
// first-loss explicit DRED decode at a 16 kHz decoder API rate for Hybrid
// SWB/FB carriers. Mirrors TestDecoderExplicitHybridDREDDecodeAPIRateMatrixMatchesLibopus
// (which covers 8/12/24 kHz) extended to 16 kHz.
//
// libopus reference: opus_decoder_dred_decode_float (opus_decoder.c) called with
// a 16 kHz decoder via probeLibopusDecoderDREDDecodeFloatForDecoder.
func TestDecoderExplicitHybridDRED16kAPIRateFirstLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16k hybrid explicit frame=%d got %d want %d", tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus decoder 16k hybrid %s DRED", tc.name))
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit first-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit first-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit first-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid API-rate explicit first-loss celt")
		})
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateThenNextPacketMatchesLibopus gates the
// then-next-packet path at 16 kHz for Hybrid explicit DRED.
func TestDecoderExplicitHybridDRED16kAPIRateThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus 16k hybrid %s next-packet DRED", tc.name))
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit next-packet pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit next-packet plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit next-packet fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid API-rate explicit next-packet celt")
		})
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateSecondLossMatchesLibopus gates the
// second-loss explicit DRED decode at 16 kHz for Hybrid carriers.
func TestDecoderExplicitHybridDRED16kAPIRateSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus 16k hybrid %s second-loss DRED", tc.name))
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit second-loss pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit second-loss plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit second-loss fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid API-rate explicit second-loss celt")
		})
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateSecondLossThenNextPacketMatchesLibopus
// gates the second-loss-then-next-packet path at 16 kHz for Hybrid explicit DRED.
func TestDecoderExplicitHybridDRED16kAPIRateSecondLossThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus 16k hybrid %s second-loss+next DRED", tc.name))
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit second-loss+next pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit second-loss+next plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit second-loss+next fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "16k hybrid API-rate explicit second-loss+next celt")
		})
	}
}

// silkExplicitDREDAPIRateCases returns the sub-48 kHz SILK decoder-rate cases
// for the explicit decodeExplicitDREDFloat battery.
func silkExplicitDREDAPIRateCases() []struct {
	name       string
	sampleRate int
} {
	return []struct {
		name       string
		sampleRate int
	}{
		{name: "8k", sampleRate: 8000},
		{name: "12k", sampleRate: 12000},
		{name: "24k", sampleRate: 24000},
	}
}

// TestDecoderExplicitSILKDREDAPIRateFirstLossMatchesLibopus gates the internal
// decodeExplicitDREDFloat path (not the public DecodeDRED wrapper) for SILK
// carriers at 8/12/24 kHz decoder API rates.
//
// TestDecoderPublicSILKDREDAPIRateMatchesLibopus covers the same rates via the
// public DecodeDRED API; these tests gate the internal path independently.
// libopus reference: opus_decoder_dred_decode_float (opus_decoder.c).
func TestDecoderExplicitSILKDREDAPIRateFirstLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range silkExplicitDREDAPIRateCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
				t, tc.sampleRate, libopusDREDPacketConfig{
					FrameSize: 960,
					ForceMode: ModeSILK,
					Bandwidth: BandwidthWideband,
				})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("SILK %s explicit frame=%d want %d", tc.name, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK API-rate DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus SILK %s explicit DRED", tc.name))
			if want.ret != n {
				t.Fatalf("libopus SILK %s explicit DRED ret=%d want %d", tc.name, want.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus SILK %s explicit DRED channels=%d want 1", tc.name, want.channels)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got*dec.Channels()], want.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), fmt.Sprintf("SILK %s explicit API-rate pcm", tc.name))
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, fmt.Sprintf("SILK %s explicit API-rate plc", tc.name))
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, fmt.Sprintf("SILK %s explicit API-rate fargan", tc.name))
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, fmt.Sprintf("SILK %s explicit API-rate celt", tc.name))
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.silk, silkpkg.BandwidthWideband, fmt.Sprintf("SILK %s explicit API-rate silk", tc.name))
		})
	}
}

// TestDecoderExplicitSILKDREDAPIRateSecondLossMatchesLibopus gates the
// second-loss explicit decodeExplicitDREDFloat path for SILK at 8/12/24/48 kHz
// decoder rates. The existing TestDecoderExplicitSILKDREDDecodeMatchesLibopus
// and TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus cover 48 kHz and
// 16 kHz respectively; this test extends the matrix to all API rates with two
// consecutive losses.
//
// libopus reference: opus_decoder_dred_decode_float called twice
// (warmup_dred_offset=n, dred_offset=2n).
func TestDecoderExplicitSILKDREDAPIRateSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// All five API rates: 8/12/16/24/48 kHz.
	tests := []struct {
		name       string
		sampleRate int
	}{
		{name: "8k", sampleRate: 8000},
		{name: "12k", sampleRate: 12000},
		{name: "16k", sampleRate: 16000},
		{name: "24k", sampleRate: 24000},
		{name: "48k", sampleRate: 48000},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
				t, tc.sampleRate, libopusDREDPacketConfig{
					FrameSize: 960,
					ForceMode: ModeSILK,
					Bandwidth: BandwidthWideband,
				})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("SILK %s second-loss frame=%d want %d", tc.name, n, wantFrame)
			}

			// First loss (warmup).
			pcm0 := make([]float32, n*dec.Channels())
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			// Oracle: warmup at offset n, decode at offset 2n.
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK API-rate second-loss DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus SILK %s second-loss explicit DRED", tc.name))
			if want.warmupRet != n {
				t.Fatalf("libopus SILK %s second-loss warmup ret=%d want %d", tc.name, want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus SILK %s second-loss ret=%d want %d", tc.name, want.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus SILK %s second-loss channels=%d want 1", tc.name, want.channels)
			}

			// Second loss.
			pcm1 := make([]float32, n*dec.Channels())
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got*dec.Channels()], want.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), fmt.Sprintf("SILK %s second-loss explicit pcm", tc.name))
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, fmt.Sprintf("SILK %s second-loss explicit plc", tc.name))
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, fmt.Sprintf("SILK %s second-loss explicit fargan", tc.name))
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, fmt.Sprintf("SILK %s second-loss explicit celt", tc.name))
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.silk, silkpkg.BandwidthWideband, fmt.Sprintf("SILK %s second-loss explicit silk", tc.name))
		})
	}
}

const (
	libopusDecoderDREDFECHistoryInputMagic  = "GDFI"
	libopusDecoderDREDFECHistoryOutputMagic = "GDFO"
)

var libopusDecoderDREDFECHistoryHelper libopustest.HelperCache

func getLibopusDecoderDREDFECHistoryHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(
		&libopusDecoderDREDFECHistoryHelper,
		"libopus_decoder_dred_fec_history_info.c",
		"gopus_libopus_decoder_dred_fec_history",
		true,
	)
}

func probeLibopusDecoderDREDFECHistory(seed, fec []byte, frameSize int, model []byte) (seedSamples, fecSamples int, pcm [lpcnetplc.PLCBufSize]float32, err error) {
	binPath, err := getLibopusDecoderDREDFECHistoryHelperPath()
	if err != nil {
		return 0, 0, pcm, err
	}
	payload := libopustest.NewOraclePayloadVersion(
		libopusDecoderDREDFECHistoryInputMagic,
		1,
		uint32(len(seed)),
		uint32(len(fec)),
		uint32(frameSize),
		uint32(len(model)),
	)
	payload.Raw(seed)
	payload.Raw(fec)
	payload.Raw(model)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "decoder DRED FEC history", libopusDecoderDREDFECHistoryOutputMagic)
	if err != nil {
		return 0, 0, pcm, err
	}
	seedSamples = int(reader.I32())
	fecSamples = int(reader.I32())
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return 0, 0, pcm, err
	}
	return seedSamples, fecSamples, pcm, nil
}

func decodeDREDFECForHistory(t *testing.T, model, seed, fec []byte, requestSamples int) (*Decoder, int, []float32) {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(model); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	if !dec.ensureDREDNeuralConcealmentRuntime() {
		t.Fatal("DRED PLC runtime did not load")
	}
	if err := dec.SetComplexity(0); err != nil {
		t.Fatalf("SetComplexity(0): %v", err)
	}

	primePCM := make([]float32, dec.maxPacketSamples)
	primeSamples, err := dec.Decode(seed, primePCM)
	if err != nil {
		t.Fatalf("Decode prime: %v", err)
	}
	fecPCM := make([]float32, requestSamples)
	fecSamples, err := dec.DecodeWithFEC(fec, fecPCM, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC: %v", err)
	}
	if fecSamples != requestSamples {
		t.Fatalf("DecodeWithFEC samples=%d want %d", fecSamples, requestSamples)
	}
	if primeSamples <= 0 {
		t.Fatalf("Decode prime samples=%d", primeSamples)
	}
	return dec, fecSamples, fecPCM[:fecSamples]
}

func assertDecoderDREDFECHistoryMatchesLibopus(t *testing.T, name string, seed, fec []byte, requestSamples int) {
	t.Helper()
	model := requireLibopusDecoderNeuralModelBlob(t)
	wantSeedSamples, wantFECSamples, wantHistory, err := probeLibopusDecoderDREDFECHistory(seed, fec, requestSamples, model)
	if err != nil {
		libopustest.HelperUnavailable(t, name+" decoder DRED FEC history", err)
	}
	wantSequence, err := libopustest.ProbeDecodeSequence(48000, 1, []libopustest.DecodeDiffCase{
		{Packet: seed, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 5760},
		{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(requestSamples), DecodeFEC: true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, name+" decode sequence", err)
	}
	if len(wantSequence) != 2 || int(wantSequence[0].Code) != wantSeedSamples || int(wantSequence[1].Code) != wantFECSamples {
		t.Fatalf("%s C sequence results=%v, history helper counts=(%d,%d)", name, wantSequence, wantSeedSamples, wantFECSamples)
	}

	dec, gotFECSamples, gotPCM := decodeDREDFECForHistory(t, model, seed, fec, requestSamples)
	if gotFECSamples != wantFECSamples {
		t.Fatalf("%s FEC samples=%d want %d", name, gotFECSamples, wantFECSamples)
	}
	if gotRange, wantRange := dec.FinalRange(), wantSequence[1].FinalRange; gotRange != wantRange {
		t.Fatalf("%s FEC range=%08x want %08x", name, gotRange, wantRange)
	}
	wantPCM := wantSequence[1].Float32()
	if len(gotPCM) != len(wantPCM) {
		t.Fatalf("%s FEC PCM length=%d want %d", name, len(gotPCM), len(wantPCM))
	}
	for i := range wantPCM {
		if gotBits, wantBits := math.Float32bits(gotPCM[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
			t.Fatalf("%s FEC PCM[%d]=%08x want %08x", name, i, gotBits, wantBits)
		}
	}

	state := dec.dredRecoveryState()
	if state == nil {
		t.Fatalf("%s has no DRED recovery state", name)
	}
	gotHistory := state.dredPLC.Snapshot().PCM
	for i := range wantHistory {
		if gotBits, wantBits := math.Float32bits(gotHistory[i]), math.Float32bits(wantHistory[i]); gotBits != wantBits {
			t.Fatalf("%s LPCNet history[%d]=%08x want C %08x", name, i, gotBits, wantBits)
		}
	}

	// Warm reusable packet and decoder scratch, then lock the history hook path
	// to the steady-state zero-allocation contract.
	output := make([]float32, requestSamples)
	decode := func() {
		if _, err := dec.DecodeWithFEC(fec, output, true); err != nil {
			panic(err)
		}
	}
	decode()
	decode()
	if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
		t.Fatalf("%s warmed DecodeWithFEC allocations=%g want 0", name, allocs)
	}
}

func TestDecoderDREDFECPLCUpdatesMatchLibopusHistory(t *testing.T) {
	libopustest.RequireOracle(t)
	seed := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	noLBRR := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	if packetHasInBandFEC(t, noLBRR) {
		t.Fatal("no-LBRR prefix fixture unexpectedly carries LBRR")
	}
	t.Run("plc_prefix_before_fec", func(t *testing.T) {
		assertDecoderDREDFECHistoryMatchesLibopus(t, "PLC prefix", seed, noLBRR, 1920)
	})

	var partial []byte
	for _, packet := range encodeFECBurstyStreamForTest(t, BandwidthWideband, 24000, 1, 1920, 40) {
		if isPartialMultiFrameLBRR(t, packet) {
			partial = packet
			break
		}
	}
	if len(partial) == 0 {
		t.Fatal("partial LBRR fixture not found")
	}
	t.Run("missing_lbrr_subframe", func(t *testing.T) {
		assertDecoderDREDFECHistoryMatchesLibopus(t, "missing LBRR subframe", seed, partial, 1920)
	})
}

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

func newDREDHistoryDecoder(t *testing.T, complexity int, loadModel bool) *Decoder {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetComplexity(complexity); err != nil {
		t.Fatalf("SetComplexity(%d): %v", complexity, err)
	}
	if loadModel {
		prepareDREDHistoryOSCEModel(t, dec)
		if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
			t.Fatalf("SetDNNBlob: %v", err)
		}
	}
	return dec
}

func probeDREDHistoryCarrier(t *testing.T, seedPacket []byte, packetInfo libopusDREDPacket, options libopusDecoderDREDSequenceOptions) libopusDecoderDREDSequenceInfo {
	t.Helper()
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	want, err := probeLibopusDecoderDREDSequenceWithOptions(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceCarrierDRED, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatInt24,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true, options,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED history sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "DRED history sequence")
	if want.carrierRet != n || want.step0.ret != n || want.channels != 1 {
		t.Fatalf("DRED history C returns carrier/recovery/channels=%d/%d/%d want %d/%d/1", want.carrierRet, want.step0.ret, want.channels, n, n)
	}
	return want
}

func assertDREDHistoryInt24MatchesC(t *testing.T, dec *Decoder, dred *DRED, packetInfo libopusDREDPacket, want libopusDecoderDREDSequenceInfo, label string) {
	t.Helper()
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	pcm := make([]int32, n)
	got, err := dec.DecodeDREDInt24(dred, n, pcm, n)
	if err != nil || got != want.step0.ret {
		t.Fatalf("%s DecodeDREDInt24=(%d,%v) C=%d", label, got, err, want.step0.ret)
	}
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, label+" PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, label+" FARGAN")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, label+" CELT")
	if got := dec.FinalRange(); got != want.step0.finalRange {
		t.Fatalf("%s final range=%08x C=%08x", label, got, want.step0.finalRange)
	}
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, label+" SILK")
	assertInt24ParitySelectedCExact(t, pcm[:got], want.step0.pcm24, 48000, 1, label+" PCM")
}

func TestDREDHistoryWrapResetAndLateModelLoadMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED carrier", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	seedRepeats := 8
	carrierSamples := ParseTOC(packetInfo.packet[0]).FrameSize
	wrapPos := (seedRepeats*320 + carrierSamples*16000/48000) % lpcnetplc.PLCBufSize
	for _, tc := range []struct {
		name               string
		resetAfterSeed     bool
		loadModelAfterSeed bool
		wantHistoryFill    int
		wantHistoryPos     int
	}{
		{name: "ring_wrap", wantHistoryFill: lpcnetplc.PLCBufSize, wantHistoryPos: wrapPos},
		{name: "reset", resetAfterSeed: true, wantHistoryFill: 320, wantHistoryPos: 320},
		{name: "model_loaded_after_seed", loadModelAfterSeed: true, wantHistoryFill: lpcnetplc.PLCBufSize, wantHistoryPos: wrapPos},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := libopusDecoderDREDSequenceOptions{
				seedRepeats:        seedRepeats,
				complexity:         10,
				resetAfterSeed:     tc.resetAfterSeed,
				loadModelAfterSeed: tc.loadModelAfterSeed,
			}
			if tc.loadModelAfterSeed {
				options.useWeightsFile = true
				options.decoderModelBlob = dredHistoryDecoderModelBlob(t)
			}
			want := probeDREDHistoryCarrier(t, seedPacket, packetInfo, options)
			dec := newDREDHistoryDecoder(t, 10, !tc.loadModelAfterSeed)
			seedPCM := make([]float32, dec.maxPacketSamples)
			for i := 0; i < seedRepeats; i++ {
				if got, err := dec.Decode(seedPacket, seedPCM); err != nil || got != 960 {
					t.Fatalf("Decode(seed %d)=(%d,%v) want (960,nil)", i, got, err)
				}
			}
			if tc.loadModelAfterSeed {
				if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
					t.Fatalf("SetDNNBlob after seed: %v", err)
				}
			}
			if tc.resetAfterSeed {
				dec.Reset()
				if dec.rawSILKHistoryFill != 0 || dec.rawSILKHistoryPos != 0 || dec.pcmHistorySynced {
					t.Fatalf("Reset left retained history fill=%d pos=%d synced=%v", dec.rawSILKHistoryFill, dec.rawSILKHistoryPos, dec.pcmHistorySynced)
				}
			}
			carrierPCM := make([]float32, dec.maxPacketSamples)
			if got, err := dec.Decode(packetInfo.packet, carrierPCM); err != nil || got != carrierSamples {
				t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", got, err, carrierSamples)
			}
			if tc.loadModelAfterSeed {
				maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
				postCarrier, err := probeLibopusDecoderDREDSequenceWithOptions(
					seedPacket, packetInfo.packet, nil,
					maxDRED, oracleRate, carrierSamples,
					libopusDecoderDREDSequenceSourceNone, 0,
					libopusDecoderDREDSequenceSourceNone, 0, false,
					libopusDecoderDREDSequenceSampleFormatFloat32,
					libopusDecoderDREDSequenceSampleFormatFloat32,
					libopusDecoderDREDSequenceSampleFormatFloat32,
					true, libopusDecoderDREDSequenceOptions{
						seedRepeats:        seedRepeats,
						complexity:         options.complexity,
						loadModelAfterSeed: true,
						useWeightsFile:     true,
						decoderModelBlob:   options.decoderModelBlob,
					},
				)
				if err != nil {
					t.Fatalf("post-carrier C probe: %v", err)
				}
				requireLibopusDREDSequenceParsed(t, postCarrier, "late-model post-carrier history")
				if !dec.ensureDREDNeuralConcealmentRuntime() || !dec.refreshDREDHistoryFromSILKDecoder() {
					t.Fatal("failed to replay retained SILK history after late model load")
				}
				state := requireDecoderDREDState(t, dec)
				plcSnapshot := state.dredPLC.Snapshot()
				assertDecoderDREDPLCStateBitsMatch(t, plcSnapshot, postCarrier.step0.state, "late-model post-carrier PLC")
			}
			if dec.rawSILKHistoryFill != tc.wantHistoryFill || dec.rawSILKHistoryPos != tc.wantHistoryPos {
				t.Fatalf("retained history fill/pos=%d/%d want %d/%d", dec.rawSILKHistoryFill, dec.rawSILKHistoryPos, tc.wantHistoryFill, tc.wantHistoryPos)
			}
			dred := parseCarrierDREDForExplicitDecode(t, 48000, packetInfo)
			assertDREDHistoryInt24MatchesC(t, dec, dred, packetInfo, want, tc.name)
		})
	}
}

func TestDREDHistoryModeTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name        string
		seedMode    Mode
		seedBW      Bandwidth
		carrierMode Mode
		carrierBW   Bandwidth
	}{
		{name: "silk_to_celt", seedMode: ModeSILK, seedBW: BandwidthWideband, carrierMode: ModeCELT, carrierBW: BandwidthFullband},
		{name: "celt_to_silk", seedMode: ModeCELT, seedBW: BandwidthFullband, carrierMode: ModeSILK, carrierBW: BandwidthWideband},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: 960, ForceMode: tc.carrierMode, Bandwidth: tc.carrierBW,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, tc.name+" DRED carrier", err)
			}
			seedPacket := makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t, tc.seedMode, tc.seedBW, 960)
			want := probeDREDHistoryCarrier(t, seedPacket, packetInfo, libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 10})
			dec := newDREDHistoryDecoder(t, 10, true)
			pcm := make([]float32, dec.maxPacketSamples)
			if got, err := dec.Decode(seedPacket, pcm); err != nil || got != 960 {
				t.Fatalf("Decode(seed)=(%d,%v) want (960,nil)", got, err)
			}
			if got, err := dec.Decode(packetInfo.packet, pcm); err != nil || got != 960 {
				t.Fatalf("Decode(carrier)=(%d,%v) want (960,nil)", got, err)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
			postCarrier, err := probeLibopusDecoderDREDSequenceWithOptions(
				seedPacket, packetInfo.packet, nil,
				maxDRED, oracleRate, 960,
				libopusDecoderDREDSequenceSourceNone, 0,
				libopusDecoderDREDSequenceSourceNone, 0, false,
				libopusDecoderDREDSequenceSampleFormatFloat32,
				libopusDecoderDREDSequenceSampleFormatFloat32,
				libopusDecoderDREDSequenceSampleFormatFloat32,
				true, libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 10},
			)
			if err != nil {
				t.Fatalf("post-carrier C probe: %v", err)
			}
			requireLibopusDREDSequenceParsed(t, postCarrier, "post-carrier DRED history")
			if postCarrier.carrierRet != 960 {
				t.Fatalf("post-carrier C return=%d want 960", postCarrier.carrierRet)
			}
			state := requireDecoderDREDState(t, dec)
			assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), postCarrier.step0.state, tc.name+" post-carrier PLC")
			assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), postCarrier.step0.fargan, tc.name+" post-carrier FARGAN")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, postCarrier.step0.celt48k, tc.name+" post-carrier CELT")
			assertDecoderDREDSILKStateBitsMatch(t, dec, postCarrier.step0.silk, silkpkg.BandwidthWideband, tc.name+" post-carrier SILK")
			if got := dec.FinalRange(); got != postCarrier.step0.finalRange {
				t.Fatalf("%s post-carrier final range=%08x C=%08x", tc.name, got, postCarrier.step0.finalRange)
			}
			dred := parseCarrierDREDForExplicitDecode(t, 48000, packetInfo)
			assertDREDHistoryInt24MatchesC(t, dec, dred, packetInfo, want, fmt.Sprintf("%s explicit DRED", tc.name))
		})
	}
}

func TestDREDHistoryClassicalLossCapturesPreCNGPCMMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED carrier", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	want, err := probeLibopusDecoderDREDSequenceWithOptions(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceLost, n,
		libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true,
		libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 0},
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "classical SILK loss sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "classical SILK loss sequence")
	if want.carrierRet != n || want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("classical C carrier/loss/recovery=%d/%d/%d want %d/%d/%d", want.carrierRet, want.step0.ret, want.step1.ret, n, n, n)
	}

	dec := newDREDHistoryDecoder(t, 0, true)
	pcm := make([]float32, dec.maxPacketSamples)
	if got, err := dec.Decode(seedPacket, pcm); err != nil || got != n {
		t.Fatalf("Decode(seed)=(%d,%v) want (%d,nil)", got, err, n)
	}
	if got, err := dec.Decode(packetInfo.packet, pcm); err != nil || got != n {
		t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", got, err, n)
	}
	lostPCM := make([]float32, n)
	if got, err := dec.Decode(nil, lostPCM); err != nil || got != n {
		t.Fatalf("Decode(nil)=(%d,%v) want (%d,nil)", got, err, n)
	}
	assertFloat32BitsEqual(t, lostPCM, want.step0.pcm, "classical pre-CNG loss PCM")
	if dec.rawSILKHistoryFill != 3*320 {
		t.Fatalf("raw SILK history after seed/carrier/loss=%d want %d", dec.rawSILKHistoryFill, 3*320)
	}
	dred := parseCarrierDREDForExplicitDecode(t, 48000, packetInfo)
	recoveryPCM := make([]float32, n)
	if got, err := dec.DecodeDRED(dred, 2*n, recoveryPCM, n); err != nil || got != want.step1.ret {
		t.Fatalf("DecodeDRED after classical loss=(%d,%v) C=%d", got, err, want.step1.ret)
	}
	assertFloat32BitsEqual(t, recoveryPCM, want.step1.pcm, "classical loss DRED recovery PCM")
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step1.state, "classical loss DRED PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step1.fargan, "classical loss DRED FARGAN")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "classical loss DRED SILK")
	if got := dec.FinalRange(); got != want.step1.finalRange {
		t.Fatalf("classical loss DRED final range=%08x C=%08x", got, want.step1.finalRange)
	}
}

func TestDREDHistoryUnloadedClassicalLossIsExcludedAfterLateModelLoadMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED carrier", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	options := libopusDecoderDREDSequenceOptions{
		seedRepeats:         1,
		complexity:          10,
		loadModelAfterSeed:  true,
		useWeightsFile:      true,
		lossBeforeModelLoad: true,
		decoderModelBlob:    dredHistoryDecoderModelBlob(t),
	}
	want, err := probeLibopusDecoderDREDSequenceWithOptions(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceLost, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true, options,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "pre-load classical loss sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "pre-load classical loss sequence")
	if want.carrierRet != n || want.step0.ret != n {
		t.Fatalf("dynamic C carrier/neural-loss returns %d/%d want %d/%d", want.carrierRet, want.step0.ret, n, n)
	}

	dec := newDREDHistoryDecoder(t, 10, false)
	pcm := make([]float32, dec.maxPacketSamples)
	if got, err := dec.Decode(seedPacket, pcm); err != nil || got != n {
		t.Fatalf("Decode(seed)=(%d,%v) want (%d,nil)", got, err, n)
	}
	unloadedLoss := make([]float32, n)
	if got, err := dec.Decode(nil, unloadedLoss); err != nil || got != n {
		t.Fatalf("Decode(nil before model)=(%d,%v) want (%d,nil)", got, err, n)
	}
	if dec.rawSILKHistoryFill != 320 {
		t.Fatalf("pre-model classical loss entered retained history: fill=%d want seed-only 320", dec.rawSILKHistoryFill)
	}
	if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob after classical loss: %v", err)
	}
	if got, err := dec.Decode(packetInfo.packet, pcm); err != nil || got != n {
		t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", got, err, n)
	}
	neuralLoss := make([]float32, n)
	if got, err := dec.Decode(nil, neuralLoss); err != nil || got != want.step0.ret {
		t.Fatalf("Decode(nil after model)=(%d,%v) C=%d", got, err, want.step0.ret)
	}
	assertFloat32BitsEqual(t, neuralLoss, want.step0.pcm, "late-model neural loss PCM")
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, "late-model neural loss PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, "late-model neural loss FARGAN")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "late-model neural loss CELT")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "late-model neural loss SILK")
	if got := dec.FinalRange(); got != want.step0.finalRange {
		t.Fatalf("late-model neural loss final range=%08x C=%08x", got, want.step0.finalRange)
	}
}

func TestDREDLongLossPCMMatchesLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredDecoderBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	goDecoderBlob := append(append([]byte(nil), decoderBlob...), dredDecoderBlob...)

	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	got := decodeDREDQualityPackets(t, packets, reference, goDecoderBlob, true)
	want := decodeLibopusDREDQualityPackets(t, packets, reference, decoderBlob, dredDecoderBlob, true)
	if len(packets) != 220 || got.lossFrames != 119 || want.lossFrames != 119 {
		t.Fatalf("sequence shape packets=%d loss frames Go=%d C=%d want 220/119/119", len(packets), got.lossFrames, want.lossFrames)
	}
	if got.dredFrames != want.dredFrames || got.fallbackFrames != want.fallbackFrames {
		t.Fatalf("concealment kinds DRED Go/C=%d/%d fallback Go/C=%d/%d", got.dredFrames, want.dredFrames, got.fallbackFrames, want.fallbackFrames)
	}
	if len(got.lossDecoded) != len(want.lossDecoded) {
		t.Fatalf("concealed sample count Go=%d C=%d", len(got.lossDecoded), len(want.lossDecoded))
	}
	if len(got.lossDecoded) != 119*dredQualityFrameSize*dredQualityChannels {
		t.Fatalf("concealed sample count=%d want %d", len(got.lossDecoded), 119*dredQualityFrameSize*dredQualityChannels)
	}
	for i := range got.lossDecoded {
		gb, cb := math.Float32bits(got.lossDecoded[i]), math.Float32bits(want.lossDecoded[i])
		if gb != cb {
			t.Fatalf("lost frame=%d sample=%d Go=%08x C=%08x", i/dredQualityFrameSize, i%dredQualityFrameSize, gb, cb)
		}
	}
}

func TestDecoderMarkDREDUpdatedPCMRefreshesNeuralHistory(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	var pcm [2 * lpcnetplc.FrameSize]float32
	edge := []float32{-1.0, -0.9999847412109375, -1.5 / 32768.0, -0.5 / 32768.0, 0, 0.5 / 32768.0, 1.5 / 32768.0, 0.999969482421875, 1.0}
	for i := range pcm {
		pcm[i] = edge[i%len(edge)]
	}
	dec.ensureDREDRecoveryState()
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.MarkUpdatedFrameFloat(pcm[:lpcnetplc.FrameSize]); got != lpcnetplc.FrameSize {
		t.Fatalf("MarkUpdatedFrameFloat()=%d want %d", got, lpcnetplc.FrameSize)
	}
	state.dredPLC.MarkConcealed()
	var beforeHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(beforeHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(before)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	before := state.dredPLC.Snapshot()
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)
	after := state.dredPLC.Snapshot()
	if after.Blend != 0 {
		t.Fatalf("Blend=%d want 0", after.Blend)
	}
	wantAnalysisPos := max(0, before.AnalysisPos-len(pcm))
	if after.AnalysisPos != wantAnalysisPos {
		t.Fatalf("AnalysisPos=%d want %d", after.AnalysisPos, wantAnalysisPos)
	}
	wantPredictPos := max(0, before.PredictPos-len(pcm))
	if after.PredictPos != wantPredictPos {
		t.Fatalf("PredictPos=%d want %d", after.PredictPos, wantPredictPos)
	}
	if after.LossCount != 0 {
		t.Fatalf("LossCount=%d want 0", after.LossCount)
	}
	var history [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(history[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory()=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	for i := 0; i < len(pcm); i++ {
		want := lpcnetplcTestQuantizePCMUpdateFloat(pcm[i])
		got := history[len(history)-len(pcm)+i]
		if got != want {
			t.Fatalf("history tail[%d]=%v want %v", i, got, want)
		}
	}
	if slices.Equal(history[:], beforeHistory[:]) {
		t.Fatal("history unexpectedly stayed unchanged after good-frame update")
	}
}

func TestDecoderMarkDREDUpdatedPCMSkipsPublicFallbackAfterRawHistory(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dec.ensureDREDRecoveryState()
	state := requireDecoderDREDState(t, dec)
	var raw [lpcnetplc.FrameSize]int16
	for i := range raw {
		raw[i] = int16((i%17 - 8) * 321)
	}
	dec.primeDREDPCMHistoryInt16(raw[:])
	before := state.dredPLC.Snapshot()
	var beforeHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(beforeHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(before)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	state.dredPLC.MarkConcealed()

	public := make([]float32, 2*lpcnetplc.FrameSize)
	for i := range public {
		public[i] = float32((i%31)-15) / 7
	}
	dec.markDREDUpdatedPCM(public, lpcnetplc.FrameSize, ModeSILK)

	after := state.dredPLC.Snapshot()
	if after.Blend != 0 {
		t.Fatalf("Blend=%d want 0", after.Blend)
	}
	if after.AnalysisPos != before.AnalysisPos || after.PredictPos != before.PredictPos || after.LossCount != before.LossCount {
		t.Fatalf("public fallback advanced raw-updated history: before=%+v after=%+v", before, after)
	}
	var afterHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(afterHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(after)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	if !slices.Equal(afterHistory[:], beforeHistory[:]) {
		t.Fatal("public fallback rewrote raw-updated DRED PCM history")
	}
}

func TestDecoderMarkDREDUpdatedPCMCELTKeepsBridgeOwnedHistory(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%23)-11) / 23
	}
	dec.ensureDREDRecoveryState()
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.MarkUpdatedFrameFloat(pcm[:lpcnetplc.FrameSize]); got != lpcnetplc.FrameSize {
		t.Fatalf("MarkUpdatedFrameFloat()=%d want %d", got, lpcnetplc.FrameSize)
	}
	state.dredPLC.MarkConcealed()
	before := state.dredPLC.Snapshot()
	var beforeHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(beforeHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(before)=%d want %d", n, lpcnetplc.PLCBufSize)
	}

	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeCELT)

	after := state.dredPLC.Snapshot()
	if after.Blend != 0 {
		t.Fatalf("Blend=%d want 0", after.Blend)
	}
	if after.AnalysisPos != before.AnalysisPos || after.PredictPos != before.PredictPos || after.LossCount != before.LossCount {
		t.Fatalf("unexpected CELT history cursor update: before=%+v after=%+v", before, after)
	}
	var history [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(history[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(after)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	if !slices.Equal(history[:], beforeHistory[:]) {
		t.Fatal("CELT good-frame mark unexpectedly rewrote DRED PCM history")
	}
}

func TestDecoderMarkDREDUpdatedPCMDormantWithoutSidecar(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%13)-6) / 13
	}
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)
	if dec.dredState() != nil {
		t.Fatalf("dred sidecar awakened without sidecar request: %+v", dec.dredState())
	}
}

func TestDecoderMarkDREDUpdatedPCMDoesNotTrackHistoryWithoutNeuralConcealment(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%17)-8) / 17
	}
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)

	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDRecoveryState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated recovery state after markDREDUpdatedPCM: %+v", state.decoderDREDRecoveryState)
	}
}

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

const (
	dredQualitySampleRate = 48000
	dredQualityFrameSize  = 960
	dredQualityChannels   = 1
	// OPUS_GET_LOOKAHEAD returns Fs/400 for RESTRICTED_LOWDELAY; the quality
	// reference follows the C decoder output timeline after this encoder delay.
	dredQualityLookaheadSamples = dredQualitySampleRate / 400
)

type dredQualityRun struct {
	decoded        []float32
	frameIndices   []int
	frameKinds     []uint32
	frameSamples   []int
	frameRanges    []uint32
	lossReference  []float32
	lossDecoded    []float32
	lossFrames     int
	dredFrames     int
	fallbackFrames int
	dredPackets    int
}

type dredQualityMetrics struct {
	SNRDB       float64
	Correlation float64
	Envelope    float64
	OpusQ       float64
	OpusQOK     bool
}

func requireDREDAudioQualityGate(t *testing.T) {
	t.Helper()
	switch strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_DRED_AUDIO_QUALITY"))) {
	case "1", "true", "yes":
		return
	default:
		t.Skip("DRED long-sequence audio quality gate is experimental; set GOPUS_DRED_AUDIO_QUALITY=1 to run it")
	}
}

func TestExplicitDREDImprovesConcealedAudioQualityAtSixtyPercentLoss(t *testing.T) {
	requireDREDAudioQualityGate(t)
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredDecoderBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	decoderBlob = append(append([]byte(nil), decoderBlob...), dredDecoderBlob...)

	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	if len(packets) == 0 {
		t.Fatal("no packets encoded")
	}

	plc := decodeDREDQualityPackets(t, packets, reference, decoderBlob, false)
	dred := decodeDREDQualityPackets(t, packets, reference, decoderBlob, true)
	zeroOffsetReference := dredQualityLossReferenceAtOffset(t, reference, len(packets), 0)
	t.Logf("uncompensated zero-offset envelope diagnostic: PLC=%.5f DRED=%.5f",
		dredQualityEnvelope(zeroOffsetReference, plc.lossDecoded, dredQualitySampleRate),
		dredQualityEnvelope(zeroOffsetReference, dred.lossDecoded, dredQualitySampleRate))
	if dred.dredFrames == 0 {
		t.Fatal("explicit DRED did not recover any lost frames")
	}
	if plc.lossFrames != dred.lossFrames {
		t.Fatalf("loss frame count mismatch: plc=%d dred=%d", plc.lossFrames, dred.lossFrames)
	}
	if dred.lossFrames < 80 {
		t.Fatalf("loss pattern recovered too few frames: got %d", dred.lossFrames)
	}

	plcMetrics := measureDREDQuality(t, plc.lossReference, plc.lossDecoded)
	dredMetrics := measureDREDQuality(t, dred.lossReference, dred.lossDecoded)
	t.Logf("PLC loss quality:  snr=%.3f dB corr=%.5f env=%.5f opusQ=%s",
		plcMetrics.SNRDB, plcMetrics.Correlation, plcMetrics.Envelope, formatOptionalQuality(plcMetrics))
	t.Logf("DRED loss quality: snr=%.3f dB corr=%.5f env=%.5f opusQ=%s recovered=%d fallback=%d packets=%d",
		dredMetrics.SNRDB, dredMetrics.Correlation, dredMetrics.Envelope, formatOptionalQuality(dredMetrics),
		dred.dredFrames, dred.fallbackFrames, dred.dredPackets)

	if dredMetrics.Envelope < plcMetrics.Envelope+0.010 {
		t.Fatalf("DRED envelope quality did not improve enough: dred=%.5f plc=%.5f", dredMetrics.Envelope, plcMetrics.Envelope)
	}
	if !dredMetrics.OpusQOK || !plcMetrics.OpusQOK {
		t.Skip("opus_compare unavailable; envelope quality gate passed")
	}
	if dredMetrics.OpusQ < plcMetrics.OpusQ+20.0 {
		t.Fatalf("DRED opus_compare quality did not improve enough: dred=%.3f plc=%.3f", dredMetrics.OpusQ, plcMetrics.OpusQ)
	}
}

func encodeDREDQualityPackets(t *testing.T, encoderBlob []byte) ([]float32, [][]byte) {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  dredQualitySampleRate,
		Channels:    dredQualityChannels,
		Application: ApplicationLowDelay,
	})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(dredQualityFrameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetMaxBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetMaxBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(48000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatalf("SetComplexity error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(60); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("SetDNNBlob encoder error: %v", err)
	}
	if err := enc.SetDREDDuration(80); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	enc.enc.SetMode(encpkg.ModeCELT)

	const frames = 220
	packets := make([][]byte, 0, frames)
	reference := make([]float32, 0, frames*dredQualityFrameSize)
	pcm := make([]float32, dredQualityFrameSize*dredQualityChannels)
	packet := make([]byte, maxPacketBytesPerStream)
	for frame := 0; frame < frames; frame++ {
		fillDREDQualitySpeechFrame(pcm, frame)
		reference = append(reference, pcm...)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frame, err)
		}
		packets = append(packets, append([]byte(nil), packet[:n]...))
	}
	return reference, packets
}

func decodeDREDQualityPackets(t *testing.T, packets [][]byte, reference []float32, decoderBlob []byte, useDRED bool) dredQualityRun {
	t.Helper()
	return decodeDREDQualityPacketsWithTrailingPLC(t, packets, reference, decoderBlob, useDRED, false)
}

func decodeDREDQualityPacketsWithTrailingPLC(t *testing.T, packets [][]byte, reference []float32, decoderBlob []byte, useDRED, flushTrailing bool) dredQualityRun {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(dredQualitySampleRate, dredQualityChannels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob decoder error: %v", err)
	}
	probe := NewDREDDecoder()
	if err := probe.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("DREDDecoder SetDNNBlob error: %v", err)
	}
	dredState := NewDRED()

	var run dredQualityRun
	pcm := make([]float32, dec.maxPacketSamples*dredQualityChannels)
	expected := 0
	haveExpected := false
	for frame, packet := range packets {
		if !dredQualityPacketDelivered(frame) {
			continue
		}

		if haveExpected {
			missing := frame - expected
			if missing > 0 {
				available := 0
				dredReady := false
				if useDRED {
					var err error
					available, _, err = probe.Parse(dredState, packet, missing*dredQualityFrameSize, dredQualitySampleRate, false)
					dredReady = err == nil && available > 0 && dredState.Processed()
				}
				for lostAgo := missing; lostAgo >= 1; lostAgo-- {
					originalFrame := frame - lostAgo
					kindDRED := false
					var n int
					var err error
					if useDRED && dredReady && available >= lostAgo*dredQualityFrameSize {
						n, err = dec.DecodeDRED(dredState, lostAgo*dredQualityFrameSize, pcm, dredQualityFrameSize)
						if err == nil {
							kindDRED = true
						}
					}
					if !kindDRED {
						n, err = dec.Decode(nil, pcm)
					}
					if err != nil {
						t.Fatalf("decode loss frame=%d useDRED=%v: %v", originalFrame, useDRED, err)
					}
					run.appendDecodedFrame(reference, originalFrame, pcm[:n*dredQualityChannels], dec.FinalRange(), true, kindDRED)
				}
			}
		}

		n, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(frame=%d useDRED=%v) error: %v", frame, useDRED, err)
		}
		run.appendDecodedFrame(reference, frame, pcm[:n*dredQualityChannels], dec.FinalRange(), false, false)
		if payload, _, ok, err := findDREDPayload(packet); err != nil {
			t.Fatalf("findDREDPayload(frame=%d): %v", frame, err)
		} else if ok && len(payload) > 0 {
			run.dredPackets++
		}
		expected = frame + 1
		haveExpected = true
	}
	if flushTrailing && haveExpected {
		for frame := expected; frame < len(packets); frame++ {
			n, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("decode trailing loss frame=%d: %v", frame, err)
			}
			run.appendDecodedFrame(reference, frame, pcm[:n*dredQualityChannels], dec.FinalRange(), true, false)
		}
	}
	return run
}

func (r *dredQualityRun) appendDecodedFrame(reference []float32, frame int, decoded []float32, finalRange uint32, lost, dred bool) {
	r.decoded = append(r.decoded, decoded...)
	r.frameIndices = append(r.frameIndices, frame)
	kind := uint32(0)
	if lost {
		kind = 2
		if dred {
			kind = 1
		}
	}
	r.frameKinds = append(r.frameKinds, kind)
	r.frameSamples = append(r.frameSamples, len(decoded))
	r.frameRanges = append(r.frameRanges, finalRange)
	if !lost {
		return
	}
	if refFrame, ok := dredQualityFrameReference(reference, frame); ok {
		r.lossReference = append(r.lossReference, refFrame...)
		r.lossDecoded = append(r.lossDecoded, decoded...)
		r.lossFrames++
		if dred {
			r.dredFrames++
		} else {
			r.fallbackFrames++
		}
	}
}

func dredQualityFrameReference(reference []float32, frame int) ([]float32, bool) {
	return dredQualityFrameReferenceWithOffset(reference, frame, dredQualityLookaheadSamples)
}

func dredQualityFrameReferenceWithOffset(reference []float32, frame, offset int) ([]float32, bool) {
	start := (frame*dredQualityFrameSize - offset) * dredQualityChannels
	end := start + dredQualityFrameSize*dredQualityChannels
	if start < 0 || end > len(reference) {
		return nil, false
	}
	return reference[start:end], true
}

func dredQualityPacketDelivered(frame int) bool {
	if frame < 20 {
		return true
	}
	switch (frame - 20) % 10 {
	case 0, 3, 6, 8:
		return true
	default:
		return false
	}
}

func fillDREDQualitySpeechFrame(dst []float32, frame int) {
	for i := range dst {
		n := frame*dredQualityFrameSize + i
		t := float64(n) / dredQualitySampleRate
		phase := 2 * math.Pi * (150.0*t -
			28.0*math.Cos(2*math.Pi*0.61*t)/(2*math.Pi*0.61) -
			9.0*math.Cos(2*math.Pi*2.3*t+0.4)/(2*math.Pi*2.3))
		voiced := 0.0
		for h := 1; h <= 6; h++ {
			amp := math.Exp(-0.34 * float64(h-1))
			voiced += amp * math.Sin(float64(h)*phase+0.17*float64(h*h))
		}
		formants := 0.16*math.Sin(2*math.Pi*(620.0+90.0*math.Sin(2*math.Pi*0.41*t))*t+0.4) +
			0.10*math.Sin(2*math.Pi*(1320.0+180.0*math.Sin(2*math.Pi*0.27*t))*t+0.9)
		syllable := 0.55 + 0.45*math.Pow(0.5+0.5*math.Sin(2*math.Pi*2.7*t+0.2), 2)
		onset := 1.0
		if n < dredQualitySampleRate/5 {
			onset = float64(n) / float64(dredQualitySampleRate/5)
		}
		dst[i] = float32(0.20 * onset * syllable * (0.68*voiced + formants))
	}
}

func measureDREDQuality(t *testing.T, reference, decoded []float32) dredQualityMetrics {
	t.Helper()
	if len(reference) == 0 || len(decoded) == 0 {
		t.Fatal("empty quality input")
	}
	n := len(reference)
	if len(decoded) < n {
		n = len(decoded)
	}
	reference = reference[:n]
	decoded = decoded[:n]
	metrics := dredQualityMetrics{
		SNRDB:       dredQualitySNR(reference, decoded),
		Correlation: dredQualityCorrelation(reference, decoded),
		Envelope:    dredQualityEnvelope(reference, decoded, dredQualitySampleRate),
	}
	if q, ok := runDREDOpusCompare(t, reference, decoded); ok {
		metrics.OpusQ = q
		metrics.OpusQOK = true
	}
	return metrics
}

func dredQualitySNR(reference, decoded []float32) float64 {
	var signal, errPower float64
	for i := range reference {
		r := float64(reference[i])
		d := float64(decoded[i])
		diff := r - d
		signal += r * r
		errPower += diff * diff
	}
	if errPower == 0 {
		return 120
	}
	return 10 * math.Log10(signal/errPower)
}

func dredQualityCorrelation(reference, decoded []float32) float64 {
	var dot, refPower, decPower float64
	for i := range reference {
		r := float64(reference[i])
		d := float64(decoded[i])
		dot += r * d
		refPower += r * r
		decPower += d * d
	}
	if refPower == 0 || decPower == 0 {
		return 0
	}
	return dot / math.Sqrt(refPower*decPower)
}

func dredQualityEnvelope(reference, decoded []float32, sampleRate int) float64 {
	const (
		window = 480
		hop    = 240
		bands  = 12
	)
	if len(reference) < window || len(decoded) < window {
		return 0
	}
	var sum float64
	var count int
	for band := 0; band < bands; band++ {
		center := 150.0 * math.Pow(2, float64(band)/3)
		refEnv := dredBandEnvelope(reference, sampleRate, center, window, hop)
		decEnv := dredBandEnvelope(decoded, sampleRate, center, window, hop)
		if len(refEnv) == len(decEnv) && len(refEnv) > 4 {
			if c := dredPearson(refEnv, decEnv); c > -2 {
				sum += c
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	score := sum / float64(count)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func dredBandEnvelope(samples []float32, sampleRate int, center float64, window, hop int) []float64 {
	frames := 1 + (len(samples)-window)/hop
	env := make([]float64, frames)
	lo := center / math.Pow(2, 1.0/6)
	hi := center * math.Pow(2, 1.0/6)
	binHz := float64(sampleRate) / float64(window)
	for frame := 0; frame < frames; frame++ {
		start := frame * hop
		var energy float64
		for bin := 1; bin <= window/2; bin++ {
			freq := float64(bin) * binHz
			if freq < lo || freq >= hi {
				continue
			}
			var re, im float64
			for i := 0; i < window; i++ {
				w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(window-1))
				v := float64(samples[start+i]) * w
				angle := -2 * math.Pi * float64(bin*i) / float64(window)
				re += v * math.Cos(angle)
				im += v * math.Sin(angle)
			}
			energy += re*re + im*im
		}
		env[frame] = math.Sqrt(energy)
	}
	return env
}

func dredPearson(x, y []float64) float64 {
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(len(x))
	my /= float64(len(y))
	var num, vx, vy float64
	for i := range x {
		dx := x[i] - mx
		dy := y[i] - my
		num += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx == 0 || vy == 0 {
		return -3
	}
	return num / math.Sqrt(vx*vy)
}

func runDREDOpusCompare(t *testing.T, reference, decoded []float32) (float64, bool) {
	t.Helper()
	opusCompare, err := libopustooling.FindOrEnsureOpusCompare(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if err != nil {
		var configErr *libopustooling.LibopusReferenceConfigError
		if errors.As(err, &configErr) {
			t.Fatalf("libopus opus_compare reference configuration is invalid: %v", err)
		}
		t.Log("opus_compare unavailable; using in-process quality metrics only")
		return 0, false
	}
	tmpDir := t.TempDir()
	refPath := filepath.Join(tmpDir, "reference.sw")
	decPath := filepath.Join(tmpDir, "decoded.sw")
	if err := writeDREDQualityPCM16(refPath, duplicateDREDQualityMono(float32ToDREDPCM16(reference))); err != nil {
		t.Fatalf("write opus_compare reference: %v", err)
	}
	if err := writeDREDQualityPCM16(decPath, float32ToDREDPCM16(decoded)); err != nil {
		t.Fatalf("write opus_compare decoded: %v", err)
	}
	cmd := exec.Command(opusCompare, refPath, decPath)
	out, err := cmd.CombinedOutput()
	q, parseErr := parseDREDOpusCompareQuality(out)
	if parseErr != nil {
		t.Logf("opus_compare output unavailable: %v (%s)", parseErr, bytes.TrimSpace(out))
		return 0, false
	}
	if err != nil {
		t.Logf("opus_compare exited non-zero but returned quality: %v", err)
	}
	return q, true
}

func parseDREDOpusCompareQuality(out []byte) (float64, error) {
	const prefix = "Opus quality metric:"
	text := string(out)
	idx := bytes.Index([]byte(text), []byte(prefix))
	if idx >= 0 {
		rest := text[idx+len(prefix):]
		var q float64
		if _, err := fmt.Sscanf(rest, "%f", &q); err != nil {
			return 0, err
		}
		return q, nil
	}
	const errorPrefix = "Internal weighted error is"
	idx = bytes.Index(bytes.ToLower(out), []byte("internal weighted error is"))
	if idx < 0 {
		return 0, fmt.Errorf("quality metric not found")
	}
	rest := text[idx+len(errorPrefix):]
	var internalErr float64
	if _, err := fmt.Sscanf(rest, "%f", &internalErr); err != nil {
		return 0, err
	}
	return 100.0 * (1.0 - 0.5*math.Log(1.0+internalErr)/math.Log(1.13)), nil
}

func float32ToDREDPCM16(samples []float32) []int16 {
	out := make([]int16, len(samples))
	for i, sample := range samples {
		if sample >= 1 {
			out[i] = math.MaxInt16
			continue
		}
		if sample <= -1 {
			out[i] = math.MinInt16
			continue
		}
		out[i] = int16(math.Round(float64(sample) * 32767))
	}
	return out
}

func duplicateDREDQualityMono(samples []int16) []int16 {
	out := make([]int16, len(samples)*2)
	for i, sample := range samples {
		out[2*i] = sample
		out[2*i+1] = sample
	}
	return out
}

func writeDREDQualityPCM16(path string, samples []int16) error {
	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		u := uint16(sample)
		data[2*i] = byte(u)
		data[2*i+1] = byte(u >> 8)
	}
	return os.WriteFile(path, data, 0o600)
}

func formatOptionalQuality(m dredQualityMetrics) string {
	if !m.OpusQOK {
		return "unavailable"
	}
	return fmt.Sprintf("%.3f", m.OpusQ)
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithFrameSize(480)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "16k_celt_10ms", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle16kCELTStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo dred packet", err)
	}
	if !ParseTOC(packetInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo CELT recovery lifecycle packet produced mono TOC (toc=0x%02x)", packetInfo.packet[0])
	}
	assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "16k_celt_stereo", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle16kHybrid(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "16k_hybrid", packetInfo, 16000)
		})
	}
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle48kCELT(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "48k_celt", packetInfo, packetInfo.sampleRate)
		})
	}
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle48kCELTStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo dred packet", err)
	}
	if !ParseTOC(packetInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo CELT recovery lifecycle packet produced mono TOC (toc=0x%02x)", packetInfo.packet[0])
	}
	assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "48k_celt_stereo", packetInfo, packetInfo.sampleRate)
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle48kHybrid(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "48k_hybrid", packetInfo, packetInfo.sampleRate)
		})
	}
}

func assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t *testing.T, label string, packetInfo libopusDREDPacket, decoderSampleRate int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("%s NewDecoder error: %v", label, err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("%s SetDNNBlob error: %v", label, err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("%s dnnblob.Clone(real model) error: %v", label, err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("%s ValidateDREDDecoderControl(real model) error: %v", label, err)
	}
	dec.setDREDDecoderBlob(blob)
	if !requireDecoderDREDState(t, dec).dredModelLoaded {
		t.Fatalf("%s standalone DRED blob did not arm decoder retention path", label)
	}

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("%s Decode error: %v", label, err)
	}
	if n <= 0 {
		t.Fatalf("%s Decode returned no audio", label)
	}
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode did not retain DRED payload", label)
	}
	if requireDecoderDREDState(t, dec).dredDecoded.NbLatents <= 0 {
		t.Fatalf("%s Decode did not retain processed DRED latents", label)
	}
	if got := requireDecoderDREDState(t, dec).dredPLC.Blend(); got != 0 {
		t.Fatalf("%s Blend after good decode=%d want 0", label, got)
	}

	assertDecoderCachedDREDRecoveryMatchesLibopus(t, dec, packetInfo, false)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("%s Decode(nil) error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode(nil) dropped cached DRED payload before recovery scheduling", label)
	}
	if got := requireDecoderDREDState(t, dec).dredPLC.Blend(); got != 1 {
		t.Fatalf("%s Blend after PLC=%d want 1", label, got)
	}

	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("%s Decode after PLC error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode after PLC did not re-retain DRED payload", label)
	}
	assertDecoderCachedDREDRecoveryMatchesLibopus(t, dec, packetInfo, true)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses16kCELT(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "16k_celt", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses16kCELTStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo dred packet", err)
	}
	if !ParseTOC(packetInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo CELT recovery cursor packet produced mono TOC (toc=0x%02x)", packetInfo.packet[0])
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "16k_celt_stereo", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses16kHybrid(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "16k_hybrid", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses16kHybridStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid dred packet", err)
	}
	if !ParseTOC(packetInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo Hybrid recovery cursor packet produced mono TOC (toc=0x%02x)", packetInfo.packet[0])
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "16k_hybrid_stereo", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses48kCELT(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "48k_celt", packetInfo, packetInfo.sampleRate)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses48kCELTStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo dred packet", err)
	}
	if !ParseTOC(packetInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo CELT recovery cursor packet produced mono TOC (toc=0x%02x)", packetInfo.packet[0])
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "48k_celt_stereo", packetInfo, packetInfo.sampleRate)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses48kHybrid(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "48k_hybrid", packetInfo, packetInfo.sampleRate)
}

func TestDecoderCachedDREDRecoveryCursorTracksLosses48kHybridStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid dred packet", err)
	}
	if !ParseTOC(packetInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo Hybrid recovery cursor packet produced mono TOC (toc=0x%02x)", packetInfo.packet[0])
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "48k_hybrid_stereo", packetInfo, packetInfo.sampleRate)
}

func assertDecoderCachedDREDRecoveryCursorAcrossLosses(t *testing.T, label string, packetInfo libopusDREDPacket, decoderSampleRate int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	// libopus opus_decode(NULL,...) passes dred==NULL, so the cached-DRED FEC
	// feed gated on `dred != NULL && process_stage == 2` (opus_decoder.c:736) is
	// skipped: a public packet-loss decode runs PLAIN PLC and queues NO cached
	// DRED FEC features. Mirror cc04ecf0's SILK reconciliation for CELT/hybrid;
	// the recovery cursor stays at 0 across public losses.
	wantQueuedFEC := false

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("%s NewDecoder error: %v", label, err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("%s SetDNNBlob error: %v", label, err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("%s dnnblob.Clone(real model) error: %v", label, err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("%s ValidateDREDDecoderControl(real model) error: %v", label, err)
	}
	dec.setDREDDecoderBlob(blob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("%s Decode error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredRecovery != 0 {
		t.Fatalf("%s dredRecovery after good decode=%d want 0", label, requireDecoderDREDState(t, dec).dredRecovery)
	}

	lossPCM := pcm[:n*channels]
	if _, err := dec.Decode(nil, lossPCM); err != nil {
		t.Fatalf("%s Decode(nil, first) error: %v", label, err)
	}
	// Public loss = plain PLC, no cached DRED applied: recovery cursor stays 0.
	if requireDecoderDREDState(t, dec).dredRecovery != 0 {
		t.Fatalf("%s dredRecovery after first loss=%d want 0", label, requireDecoderDREDState(t, dec).dredRecovery)
	}
	if skip := requireDecoderDREDState(t, dec).dredPLC.FECSkip(); skip != 0 {
		t.Fatalf("%s FECSkip after first loss=%d want 0", label, skip)
	}
	assertCachedDREDLossFECCursor(t, label, "first", dec, wantQueuedFEC)

	if _, err := dec.Decode(nil, lossPCM); err != nil {
		t.Fatalf("%s Decode(nil, second) error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredRecovery != 0 {
		t.Fatalf("%s dredRecovery after second loss=%d want 0", label, requireDecoderDREDState(t, dec).dredRecovery)
	}
	if skip := requireDecoderDREDState(t, dec).dredPLC.FECSkip(); skip != 0 {
		t.Fatalf("%s FECSkip after second loss=%d want 0", label, skip)
	}
	assertCachedDREDLossFECCursor(t, label, "second", dec, wantQueuedFEC)

	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("%s Decode(after losses) error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredRecovery != 0 {
		t.Fatalf("%s dredRecovery after re-decode=%d want 0", label, requireDecoderDREDState(t, dec).dredRecovery)
	}
}

func assertCachedDREDLossFECCursor(t *testing.T, label, loss string, dec *Decoder, wantQueued bool) {
	t.Helper()
	state := requireDecoderDREDState(t, dec)
	fill := state.dredPLC.FECFillPos()
	if !wantQueued {
		if fill != 0 {
			t.Fatalf("%s FECFillPos after %s loss=%d want 0", label, loss, fill)
		}
		return
	}
	if fill == 0 {
		t.Fatalf("%s FECFillPos after %s loss=0 want queued cached DRED features", label, loss)
	}
}

func assertDecoderCachedDREDRecoveryMatchesLibopus(t *testing.T, dec *Decoder, packetInfo libopusDREDPacket, blend bool) {
	t.Helper()

	tests := []struct {
		name            string
		decodeOffset48k int
		frameSize48k    int
	}{
		{name: "negative_offset", decodeOffset48k: -480, frameSize48k: 960},
		{name: "negative_full_frame", decodeOffset48k: -960, frameSize48k: 960},
		{name: "current_frame", decodeOffset48k: 0, frameSize48k: 960},
		{name: "current_10ms", decodeOffset48k: 0, frameSize48k: 480},
		{name: "current_60ms", decodeOffset48k: 0, frameSize48k: 2880},
		{name: "late_recovery", decodeOffset48k: 3840, frameSize48k: 960},
		{name: "beyond_payload", decodeOffset48k: 9600, frameSize48k: 960},
		{name: "half_frame", decodeOffset48k: 960, frameSize48k: 480},
		{name: "late_10ms", decodeOffset48k: 1920, frameSize48k: 480},
	}

	maxDREDSamples, sampleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frameSizeSamples := scaleDREDSampleCount(tc.frameSize48k, 48000, sampleRate)
			decodeOffsetSamples := scaleDREDSampleOffset(tc.decodeOffset48k, 48000, sampleRate)
			want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, maxDREDSamples, sampleRate, frameSizeSamples, decodeOffsetSamples, blend)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred recovery", err)
			}

			got := dec.cachedDREDRecoveryWindow(maxDREDSamples, decodeOffsetSamples, frameSizeSamples)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("cachedDREDRecoveryWindow=%+v want %+v", got, want)
			}

			queued := dec.queueCachedDREDRecovery(maxDREDSamples, decodeOffsetSamples, frameSizeSamples)
			if queued != got {
				t.Fatalf("queueCachedDREDRecovery=%+v want %+v", queued, got)
			}
			if requireDecoderDREDState(t, dec).dredPLC.FECFillPos() != want.recoverableFeatureFrames {
				t.Fatalf("FECFillPos()=%d want %d", requireDecoderDREDState(t, dec).dredPLC.FECFillPos(), want.recoverableFeatureFrames)
			}
			if requireDecoderDREDState(t, dec).dredPLC.FECSkip() != want.missingPositiveFrames {
				t.Fatalf("FECSkip()=%d want %d", requireDecoderDREDState(t, dec).dredPLC.FECSkip(), want.missingPositiveFrames)
			}

			queuedFeature := 0
			var gotFeatures [lpcnetplc.NumFeatures]float32
			for _, featureOffset := range want.featureOffsets {
				if featureOffset < 0 || featureOffset > want.maxFeatureIndex {
					continue
				}
				if n := requireDecoderDREDState(t, dec).dredPLC.FillQueuedFeatures(queuedFeature, gotFeatures[:]); n != lpcnetplc.NumFeatures {
					t.Fatalf("FillQueuedFeatures(%d) count=%d want %d", queuedFeature, n, lpcnetplc.NumFeatures)
				}
				start := featureOffset * lpcnetplc.NumFeatures
				assertFloat32BitsEqual(t, gotFeatures[:], requireDecoderDREDState(t, dec).dredDecoded.Features[start:start+lpcnetplc.NumFeatures], "queued features")
				queuedFeature++
			}
		})
	}
}

const (
	libopusDecoderDREDSequenceInputMagic  = "GDSI"
	libopusDecoderDREDSequenceOutputMagic = "GDSO"

	libopusDecoderDREDSequenceSampleFormatFloat32 = uint32(0)
	libopusDecoderDREDSequenceSampleFormatInt16   = uint32(1)
	libopusDecoderDREDSequenceSampleFormatInt24   = uint32(2)

	libopusDecoderDREDSequenceSourceNone             = 0
	libopusDecoderDREDSequenceSourceLost             = 1
	libopusDecoderDREDSequenceSourceNextDRED         = 2
	libopusDecoderDREDSequenceSourceCarrierDRED      = 3
	libopusDecoderDREDSequenceSourceQueueCarrierDRED = 4
)

type libopusDecoderDREDSequenceStepInfo struct {
	ret        int
	finalRange uint32
	state      lpcnetplc.StateSnapshot
	fargan     lpcnetplc.FARGANSnapshot
	celt48k    libopusDecoderDREDCELTSnapshot
	silk       libopusDecoderDREDSILKSnapshot
	pcm        []float32
	pcm16      []int16
	pcm24      []int32
}

type libopusDecoderDREDSILKSnapshot struct {
	LagPrev        int32
	LastGainIndex  int
	LossCount      int
	PrevSignalType int
	SMid           [2]float32
	OutBuf         [480]float32
	SLPCQ14        [16]int32
	ExcQ14         [320]int32
	ResamplerIIR   [6]int32
	ResamplerFIR   [8]float32
	ResamplerDelay [96]float32
}

type libopusDecoderDREDSequenceInfo struct {
	carrierParseRet int
	carrierDredEnd  int
	nextParseRet    int
	nextDredEnd     int
	carrierRet      int
	channels        int
	step0           libopusDecoderDREDSequenceStepInfo
	step1           libopusDecoderDREDSequenceStepInfo
	next            libopusDecoderDREDSequenceStepInfo
}

type libopusDecoderDREDSequenceOptions struct {
	seedRepeats         int
	complexity          int
	resetAfterSeed      bool
	loadModelAfterSeed  bool
	useWeightsFile      bool
	lossBeforeModelLoad bool
	decoderModelBlob    []byte
}

var (
	libopusDecoderDREDSequenceHelper            libopustest.HelperCache
	libopusDecoderDREDWeightsFileSequenceHelper libopustest.HelperCache
)

func getLibopusDecoderDREDSequenceHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDecoderDREDSequenceHelper, "libopus_decoder_dred_sequence_info.c", "gopus_libopus_decoder_dred_sequence", true)
}

func getLibopusDecoderDREDWeightsFileSequenceHelperPath() (string, error) {
	return libopusDecoderDREDWeightsFileSequenceHelper.Path(func() (string, error) {
		return libopustest.BuildDREDWeightsFileHelper("", "libopus_decoder_dred_sequence_info.c", "gopus_libopus_decoder_dred_sequence_weights_file", true)
	})
}

func probeLibopusDecoderDREDSequence(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, libopusDecoderDREDSequenceSampleFormatFloat32, true)
}

func probeLibopusDecoderDREDSequenceInt16(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, libopusDecoderDREDSequenceSampleFormatInt16, true)
}

func probeLibopusDecoderDREDSequenceInt24(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, libopusDecoderDREDSequenceSampleFormatInt24, true)
}

// probeLibopusDecoderDREDSequenceInt24WithoutCarrierDecode mirrors the explicit
// DRED API path: decode the seed in int24 mode, parse the carrier payload, and
// call opus_decoder_dred_decode24 without first decoding the carrier packet.
func probeLibopusDecoderDREDSequenceInt24WithoutCarrierDecode(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, libopusDecoderDREDSequenceSampleFormatInt24, false)
}

func probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool, sampleFormat uint32, decodeCarrierPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormats(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, sampleFormat, sampleFormat, sampleFormat, decodeCarrierPacket)
}

func probeLibopusDecoderDREDSequenceWithSampleFormats(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool, stepSampleFormat, seedSampleFormat, carrierSampleFormat uint32, decodeCarrierPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithOptions(seedPacket, carrierPacket, nextPacket,
		maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples,
		step1Source, step1OffsetSamples, decodeNextPacket, stepSampleFormat,
		seedSampleFormat, carrierSampleFormat, decodeCarrierPacket,
		libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 10})
}

func probeLibopusDecoderDREDSequenceWithOptions(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool, stepSampleFormat, seedSampleFormat, carrierSampleFormat uint32, decodeCarrierPacket bool, options libopusDecoderDREDSequenceOptions) (libopusDecoderDREDSequenceInfo, error) {
	var binPath string
	var err error
	if options.useWeightsFile {
		binPath, err = getLibopusDecoderDREDWeightsFileSequenceHelperPath()
	} else {
		binPath, err = getLibopusDecoderDREDSequenceHelperPath()
	}
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	validSampleFormat := func(format uint32) bool {
		return format == libopusDecoderDREDSequenceSampleFormatFloat32 || format == libopusDecoderDREDSequenceSampleFormatInt16 || format == libopusDecoderDREDSequenceSampleFormatInt24
	}
	if !validSampleFormat(stepSampleFormat) || !validSampleFormat(seedSampleFormat) || !validSampleFormat(carrierSampleFormat) {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("invalid decoder DRED sequence sample formats: step=%d seed=%d carrier=%d", stepSampleFormat, seedSampleFormat, carrierSampleFormat)
	}
	if options.seedRepeats < 0 || options.seedRepeats > 1024 {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("invalid decoder DRED sequence seed repeat count %d", options.seedRepeats)
	}
	if options.complexity < 0 || options.complexity > 10 {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("invalid decoder DRED sequence complexity %d", options.complexity)
	}
	if options.lossBeforeModelLoad && (!options.useWeightsFile || !options.loadModelAfterSeed) {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("pre-load classical loss requires a deferred USE_WEIGHTS_FILE model load")
	}
	decoderModelBlob := options.decoderModelBlob
	if decoderModelBlob == nil {
		decoderModelBlob, err = probeLibopusDecoderNeuralModelBlob()
		if err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	}
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}

	payloadVersion := uint32(7)
	payload := libopustest.NewOraclePayloadVersion(libopusDecoderDREDSequenceInputMagic, payloadVersion,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(len(seedPacket)),
		uint32(len(carrierPacket)),
		uint32(len(nextPacket)),
		uint32(len(decoderModelBlob)),
		uint32(len(dredModelBlob)),
		uint32(step0Source),
	)
	payload.I32(int32(step0OffsetSamples))
	payload.U32(uint32(step1Source))
	payload.I32(int32(step1OffsetSamples))
	var nextFlag uint32
	if decodeNextPacket {
		nextFlag = 1
	}
	payload.U32(nextFlag)
	payload.U32(stepSampleFormat)
	var decodeCarrierFlag uint32
	if decodeCarrierPacket {
		decodeCarrierFlag = 1
	}
	payload.U32(decodeCarrierFlag)
	payload.U32(seedSampleFormat)
	payload.U32(carrierSampleFormat)
	payload.U32(uint32(options.seedRepeats))
	payload.U32(uint32(options.complexity))
	var resetAfterSeed uint32
	if options.resetAfterSeed {
		resetAfterSeed = 1
	}
	payload.U32(resetAfterSeed)
	var loadModelAfterSeed uint32
	if options.loadModelAfterSeed {
		loadModelAfterSeed = 1
	}
	payload.U32(loadModelAfterSeed)
	var lossBeforeModelLoad uint32
	if options.lossBeforeModelLoad {
		lossBeforeModelLoad = 1
	}
	payload.U32(lossBeforeModelLoad)
	for _, chunk := range [][]byte{
		seedPacket,
		carrierPacket,
		nextPacket,
		decoderModelBlob,
		dredModelBlob,
	} {
		payload.Raw(chunk)
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "decoder dred sequence", libopusDecoderDREDSequenceOutputMagic, 2)
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	info := libopusDecoderDREDSequenceInfo{
		carrierParseRet: int(reader.I32()),
		carrierDredEnd:  int(reader.I32()),
		nextParseRet:    int(reader.I32()),
		nextDredEnd:     int(reader.I32()),
		carrierRet:      int(reader.I32()),
	}
	info.step0.ret = int(reader.I32())
	info.step1.ret = int(reader.I32())
	info.next.ret = int(reader.I32())
	info.channels = int(reader.I32())

	readBits := func(dst []float32) error {
		for i := range dst {
			dst[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return err
		}
		return nil
	}
	readPCM := func(ret int) ([]float32, error) {
		if ret <= 0 || info.channels <= 0 {
			return nil, nil
		}
		dst := make([]float32, ret*info.channels)
		if err := readBits(dst); err != nil {
			return nil, err
		}
		return dst, nil
	}
	readPCM16 := func(ret int) ([]int16, error) {
		if ret <= 0 || info.channels <= 0 {
			return nil, nil
		}
		dst := make([]int16, ret*info.channels)
		for i := range dst {
			dst[i] = reader.I16()
		}
		if err := reader.Err(); err != nil {
			return nil, err
		}
		return dst, nil
	}
	readPCM24 := func(ret int) ([]int32, error) {
		if ret <= 0 || info.channels <= 0 {
			return nil, nil
		}
		dst := make([]int32, ret*info.channels)
		for i := range dst {
			dst[i] = reader.I32()
		}
		if err := reader.Err(); err != nil {
			return nil, err
		}
		return dst, nil
	}
	parseSnapshot := func(step *libopusDecoderDREDSequenceStepInfo) error {
		step.ret = int(reader.I32())
		step.state.Blend = int(reader.I32())
		step.state.LossCount = int(reader.I32())
		step.state.AnalysisGap = int(reader.I32())
		step.state.AnalysisPos = int(reader.I32())
		step.state.PredictPos = int(reader.I32())
		step.state.FECReadPos = int(reader.I32())
		step.state.FECFillPos = int(reader.I32())
		step.state.FECSkip = int(reader.I32())
		for i := range step.state.FEC {
			if err := readBits(step.state.FEC[i][:]); err != nil {
				return err
			}
		}
		step.fargan.ContInitialized = reader.I32() != 0
		step.fargan.LastPeriod = int(reader.I32())
		step.celt48k.LastFrameType = int(reader.I32())
		step.celt48k.PLCFill = int(reader.I32())
		step.celt48k.PLCDuration = int(reader.I32())
		step.celt48k.SkipPLC = int(reader.I32())
		step.celt48k.PLCPreemphasisMem = reader.Float32()
		for _, dst := range [][]float32{
			step.state.Features[:],
			step.state.Cont[:],
			step.state.PCM[:],
			step.state.PLCNet.GRU1[:],
			step.state.PLCNet.GRU2[:],
			step.state.PLCBak[0].GRU1[:],
			step.state.PLCBak[0].GRU2[:],
			step.state.PLCBak[1].GRU1[:],
			step.state.PLCBak[1].GRU2[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		step.fargan.DeemphMem = reader.Float32()
		for _, dst := range [][]float32{
			step.fargan.PitchBuf[:],
			step.fargan.CondConv1State[:],
			step.fargan.FWC0Mem[:],
			step.fargan.GRU1State[:],
			step.fargan.GRU2State[:],
			step.fargan.GRU3State[:],
			step.celt48k.PreemphMem[:],
			step.celt48k.PLCPCM[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		step.silk.LagPrev = reader.I32()
		step.silk.LastGainIndex = int(reader.I32())
		step.silk.LossCount = int(reader.I32())
		step.silk.PrevSignalType = int(reader.I32())
		for _, dst := range [][]float32{
			step.silk.SMid[:],
			step.silk.OutBuf[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		for _, dst := range [][]int32{step.silk.SLPCQ14[:], step.silk.ExcQ14[:], step.silk.ResamplerIIR[:]} {
			for i := range dst {
				dst[i] = reader.I32()
			}
		}
		for _, dst := range [][]float32{
			step.silk.ResamplerFIR[:],
			step.silk.ResamplerDelay[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		step.finalRange = reader.U32()
		return reader.Err()
	}

	switch stepSampleFormat {
	case libopusDecoderDREDSequenceSampleFormatInt16:
		if info.step0.pcm16, err = readPCM16(info.step0.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.step1.pcm16, err = readPCM16(info.step1.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.next.pcm16, err = readPCM16(info.next.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	case libopusDecoderDREDSequenceSampleFormatInt24:
		if info.step0.pcm24, err = readPCM24(info.step0.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.step1.pcm24, err = readPCM24(info.step1.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.next.pcm24, err = readPCM24(info.next.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	default:
		if info.step0.pcm, err = readPCM(info.step0.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.step1.pcm, err = readPCM(info.step1.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.next.pcm, err = readPCM(info.next.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	}
	for _, step := range []*libopusDecoderDREDSequenceStepInfo{&info.step0, &info.step1, &info.next} {
		if err := parseSnapshot(step); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	return info, nil
}

func requireLibopusDREDSequenceParsed(t testing.TB, info libopusDecoderDREDSequenceInfo, label string) {
	t.Helper()
	if info.carrierParseRet <= 0 {
		t.Fatalf("%s libopus DRED carrier parse ret=%d want >0 (dredEnd=%d)", label, info.carrierParseRet, info.carrierDredEnd)
	}
	if info.carrierParseRet < info.carrierDredEnd {
		t.Fatalf("%s libopus DRED carrier parse ret=%d before dredEnd=%d", label, info.carrierParseRet, info.carrierDredEnd)
	}
}

func prepareCachedDREDDecodeInt16ParityStateForDecoderRateAndPacketWithChannels(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket, wantChannels int) (*Decoder, int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}
	if wantChannels > 0 && channels != wantChannels {
		t.Fatalf("cached DRED int16 parity requires %d-channel packet, got sampleRate=%d channels=%d", wantChannels, packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	setDREDDecoderBlobFromBytesForTest(t, dec, modelBlob)

	pcm := make([]int16, dec.maxPacketSamples*channels)
	n, err := dec.DecodeInt16(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("DecodeInt16(DRED packet) error: %v", err)
	}
	if n <= 0 {
		t.Fatal("DecodeInt16(DRED packet) returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("DecodeInt16(DRED packet) did not retain processed DRED state")
	}
	return dec, n
}

func assertDecoderCachedDREDDecodeInt16LossesMatchLiveSequenceOracle(t *testing.T, label string, decoderSampleRate int, packetInfo libopusDREDPacket, wantChannels, maxDiff int) {
	t.Helper()

	dec, n := prepareCachedDREDDecodeInt16ParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, wantChannels)
	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderSampleRate)
	if err != nil {
		t.Fatalf("%s packetSamplesAtRate: %v", label, err)
	}
	if n != wantFrame {
		t.Fatalf("%s warmup samples=%d want %d at %d Hz", label, n, wantFrame, decoderSampleRate)
	}

	// A public DecodeInt16(nil) runs PLAIN PLC and consumes no cached DRED:
	// opus_decode(NULL,...) passes dred==NULL so the FEC feed gated on
	// `dred != NULL && process_stage == 2` is skipped (opus_decoder.c:736).
	//
	// SILK public loss already matches libopus plain PLC tightly (cc04ecf0), so
	// SILK keeps the public DecodeInt16(nil) path against the SourceLost oracle
	// and asserts no DRED recovery was scheduled.
	//
	// CELT/Hybrid: the test's intent is DRED RECOVERY of the lost int16 audio.
	// There is no public int16 explicit-DRED entry point, so drive the explicit
	// DRED-decode path -- the libopus opus_decoder_dred_decode equivalent the
	// SourceCarrierDRED oracle exercises -- and quantize the recovered float
	// frames to int16 via RES2INT16/FLOAT2INT16 (opus_decoder.c:1628).
	toc := ParseTOC(packetInfo.packet[0])
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	if toc.Mode == ModeSILK {
		want, err := probeLibopusDecoderDREDSequenceInt16(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceLost, 2*n, false)
		if err != nil {
			libopustest.HelperUnavailable(t, label+" decoder DRED int16 sequence", err)
		}
		requireLibopusDREDSequenceParsed(t, want, label+" int16 cached losses")
		if want.channels != wantChannels {
			t.Fatalf("%s libopus channels=%d want %d", label, want.channels, wantChannels)
		}
		if want.step0.ret != n || want.step1.ret != n {
			t.Fatalf("%s libopus cached int16 ret=(%d,%d) want (%d,%d)", label, want.step0.ret, want.step1.ret, n, n)
		}

		pcm0 := make([]int16, n*dec.Channels())
		got0, err := dec.DecodeInt16(nil, pcm0)
		if err != nil {
			t.Fatalf("%s DecodeInt16(nil, first) error: %v", label, err)
		}
		if got0 != n {
			t.Fatalf("%s DecodeInt16(nil, first)=%d want %d", label, got0, n)
		}
		assertInt16WithinLSB(t, pcm0[:got0*dec.Channels()], want.step0.pcm16[:got0*dec.Channels()], maxDiff, label+" cached first-loss int16")
		assertDecoderPublicLossLeavesNoDREDRecovery(t, dec, label+" cached first-loss int16")

		pcm1 := make([]int16, n*dec.Channels())
		got1, err := dec.DecodeInt16(nil, pcm1)
		if err != nil {
			t.Fatalf("%s DecodeInt16(nil, second) error: %v", label, err)
		}
		if got1 != n {
			t.Fatalf("%s DecodeInt16(nil, second)=%d want %d", label, got1, n)
		}
		assertInt16WithinLSB(t, pcm1[:got1*dec.Channels()], want.step1.pcm16[:got1*dec.Channels()], maxDiff, label+" cached second-loss int16")
		assertDecoderPublicLossLeavesNoDREDRecovery(t, dec, label+" cached second-loss int16")
		return
	}

	dred := parseCarrierDREDForExplicitDecode(t, decoderSampleRate, packetInfo)
	want, err := probeLibopusDecoderDREDSequenceInt16(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED int16 sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" int16 cached losses")
	if want.channels != wantChannels {
		t.Fatalf("%s libopus channels=%d want %d", label, want.channels, wantChannels)
	}
	if want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("%s libopus cached int16 ret=(%d,%d) want (%d,%d)", label, want.step0.ret, want.step1.ret, n, n)
	}

	pcm0 := decodeExplicitCachedDREDToInt16(t, dec, dred, n, n, label+" first")
	assertInt16WithinLSB(t, pcm0[:n*dec.Channels()], want.step0.pcm16[:n*dec.Channels()], maxDiff, label+" cached first-loss int16")

	pcm1 := decodeExplicitCachedDREDToInt16(t, dec, dred, 2*n, n, label+" second")
	assertInt16WithinLSB(t, pcm1[:n*dec.Channels()], want.step1.pcm16[:n*dec.Channels()], maxDiff, label+" cached second-loss int16")
}

// assertDecoderPublicLossLeavesNoDREDRecovery verifies a public packet-loss
// decode applied no cached DRED, mirroring libopus opus_decode(NULL,...) where
// dred==NULL skips the FEC feed (opus_decoder.c:736). Same invariants the SILK
// public-loss tests check after cc04ecf0.
func assertDecoderPublicLossLeavesNoDREDRecovery(t *testing.T, dec *Decoder, label string) {
	t.Helper()
	state := requireDecoderDREDState(t, dec)
	if state.dredRecovery != 0 {
		t.Fatalf("%s dredRecovery=%d want 0", label, state.dredRecovery)
	}
	if fill := state.dredPLC.FECFillPos(); fill != 0 {
		t.Fatalf("%s FECFillPos=%d want 0", label, fill)
	}
	if skip := state.dredPLC.FECSkip(); skip != 0 {
		t.Fatalf("%s FECSkip=%d want 0", label, skip)
	}
}

// decodeExplicitCachedDREDToInt16 recovers one lost frame through the explicit
// DRED-decode path and quantizes it to int16 with RES2INT16/FLOAT2INT16 (no
// soft-clip), matching libopus opus_decoder_dred_decode (opus_decoder.c:1628,
// `pcm[i] = RES2INT16(out[i])`).
func decodeExplicitCachedDREDToInt16(t *testing.T, dec *Decoder, dred *DRED, dredOffsetSamples, frameSizeSamples int, label string) []int16 {
	t.Helper()
	channels := dec.Channels()
	flo := make([]float32, frameSizeSamples*channels)
	got, err := dec.decodeExplicitDREDFloat(dred, dredOffsetSamples, flo, frameSizeSamples)
	if err != nil {
		t.Fatalf("%s explicit DRED decode error: %v", label, err)
	}
	if got != frameSizeSamples {
		t.Fatalf("%s explicit DRED decode=%d want %d", label, got, frameSizeSamples)
	}
	out := make([]int16, got*channels)
	float32ToInt16NoSoftClip(out, flo, got, channels)
	return out
}

func assertInt16WithinLSB(t *testing.T, got, want []int16, maxDiff int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	worstIdx := -1
	worstDiff := 0
	for i := range got {
		diff := int(got[i]) - int(want[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > worstDiff {
			worstIdx = i
			worstDiff = diff
		}
	}
	if worstDiff > maxDiff {
		t.Fatalf("%s[%d]=%d want %d (max diff=%d > %d)", label, worstIdx, got[worstIdx], want[worstIdx], worstDiff, maxDiff)
	}
}

func TestDecoderSILKDecodeInt16NilWithCachedDREDLossesMatchLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1, 2} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "SILK DRED int16 packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("SILK DRED int16 packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				label := fmt.Sprintf("cached SILK int16 channels=%d decoder=%d", channels, sampleRate)
				assertDecoderCachedDREDDecodeInt16LossesMatchLiveSequenceOracle(t, label, sampleRate, packetInfo, channels, 2)
			})
		}
	}
}

func TestDecoderSILKDecodeInt16NilWithCachedDREDRequestedPLCDurationMatchesLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "SILK DRED int16 requested packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("SILK DRED int16 requested packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			for _, requested := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				requested := requested
				t.Run(fmt.Sprintf("channels_%d_decoder_%d_request_%d", channels, sampleRate, requested), func(t *testing.T) {
					dec, n := prepareCachedDREDDecodeInt16ParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, channels)
					packetFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					if n != packetFrame {
						t.Fatalf("cached SILK int16 requested warmup samples=%d want %d at %d Hz", n, packetFrame, sampleRate)
					}

					maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
					want, err := probeLibopusDecoderDREDSequenceInt16(nil, packetInfo.packet, nil, maxDRED, oracleRate, requested, libopusDecoderDREDSequenceSourceLost, requested, libopusDecoderDREDSequenceSourceLost, 2*requested, false)
					if err != nil {
						libopustest.HelperUnavailable(t, "cached SILK int16 requested decoder DRED sequence", err)
					}
					requireLibopusDREDSequenceParsed(t, want, "cached SILK int16 requested losses")
					if want.channels != channels {
						t.Fatalf("libopus cached SILK int16 requested channels=%d want %d", want.channels, channels)
					}
					if want.step0.ret != requested || want.step1.ret != requested {
						t.Fatalf("libopus cached SILK int16 requested ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, requested, requested)
					}

					pcm0 := make([]int16, requested*dec.Channels())
					got0, err := dec.DecodeInt16(nil, pcm0)
					if err != nil {
						t.Fatalf("DecodeInt16(nil, requested first) error: %v", err)
					}
					if got0 != requested {
						t.Fatalf("DecodeInt16(nil, requested first)=%d want %d", got0, requested)
					}
					assertInt16WithinLSB(t, pcm0[:got0*dec.Channels()], want.step0.pcm16[:got0*dec.Channels()], 2, "cached SILK int16 requested first-loss")

					pcm1 := make([]int16, requested*dec.Channels())
					got1, err := dec.DecodeInt16(nil, pcm1)
					if err != nil {
						t.Fatalf("DecodeInt16(nil, requested second) error: %v", err)
					}
					if got1 != requested {
						t.Fatalf("DecodeInt16(nil, requested second)=%d want %d", got1, requested)
					}
					assertInt16WithinLSB(t, pcm1[:got1*dec.Channels()], want.step1.pcm16[:got1*dec.Channels()], 2, "cached SILK int16 requested second-loss")
				})
			}
		}
	}
}

func TestDecoderCachedCELTDREDDecodeInt16TracksLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1, 2} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeCELT,
			Bandwidth:     BandwidthFullband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "CELT DRED int16 packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband || toc.Stereo != (channels == 2) {
			t.Fatalf("CELT DRED int16 packet TOC=%+v, want channels=%d CELT FB", toc, channels)
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				label := fmt.Sprintf("cached CELT int16 channels=%d decoder=%d", channels, sampleRate)
				maxDiff := 192
				if channels == 2 && sampleRate == 8000 {
					maxDiff = 640
				}
				assertDecoderCachedDREDDecodeInt16LossesMatchLiveSequenceOracle(t, label, sampleRate, packetInfo, channels, maxDiff)
			})
		}
	}
}

func TestDecoderFirstLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	dred := parseCarrierDREDForExplicitDecode(t, dec.SampleRate(), packetInfo)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "first-loss")
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
	}

	// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736):
	// a public Decode(nil) runs plain PLC and consumes no cached DRED.
	gotN := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if gotN != n {
		t.Fatalf("explicit DRED decode=%d want %d", gotN, n)
	}

	// END-TO-END concealed-audio gate via the trusted quality comparator (16 kHz
	// corr/RMS; opus_compare Q only valid at 48 kHz). Internal-state oracles below
	// stay bit-exact.
	assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step0.pcm[:n], dec.Channels(), "concealed pcm live-sequence oracle")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "live 16k first-loss sequence oracle plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "live 16k first-loss sequence oracle fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "live 16k first-loss sequence oracle celt")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "live 16k first-loss sequence oracle silk")
}

func TestDecoderSecondLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	dred := parseCarrierDREDForExplicitDecode(t, dec.SampleRate(), packetInfo)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "second-loss")
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
	}
	if want.step1.ret != n {
		t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
	}

	// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736):
	// two consecutive recoveries at carrier-frame offsets n and 2n.
	if got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n); got != n {
		t.Fatalf("explicit DRED decode(first)=%d want %d", got, n)
	}
	gotN := decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm, n)
	if gotN != n {
		t.Fatalf("explicit DRED decode(second)=%d want %d", gotN, n)
	}

	assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step1.pcm[:n], dec.Channels(), "second concealed pcm live-sequence oracle")
	assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "live 16k second-loss sequence oracle plc")
	assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "live 16k second-loss sequence oracle fargan")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "live 16k second-loss sequence oracle celt")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "live 16k second-loss sequence oracle silk")
}

func TestDecoderFirstLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			dred := parseCarrierDREDForExplicitDecode(t, dec.SampleRate(), packetInfo)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("first-loss carrier_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736).
			gotN := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if gotN != n {
				t.Fatalf("explicit DRED decode=%d want %d", gotN, n)
			}

			assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step0.pcm[:n], dec.Channels(), fmt.Sprintf("concealed frame-size %d pcm live-sequence oracle", frameSize))
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "live 16k first-loss frame-size sequence oracle plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "live 16k first-loss frame-size sequence oracle fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "live 16k first-loss frame-size sequence oracle celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "live 16k first-loss frame-size sequence oracle silk")
		})
	}
}

func TestDecoderSecondLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			dred := parseCarrierDREDForExplicitDecode(t, dec.SampleRate(), packetInfo)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("second-loss carrier_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736).
			if got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n); got != n {
				t.Fatalf("explicit DRED decode(first)=%d want %d", got, n)
			}
			gotN := decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm, n)
			if gotN != n {
				t.Fatalf("explicit DRED decode(second)=%d want %d", gotN, n)
			}

			assertConcealedAudioMatchesLibopus(t, pcm[:n], want.step1.pcm[:n], dec.Channels(), fmt.Sprintf("second concealed frame-size %d pcm live-sequence oracle", frameSize))
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "live 16k second-loss frame-size sequence oracle plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "live 16k second-loss frame-size sequence oracle fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step1.celt48k, "live 16k second-loss frame-size sequence oracle celt")
			assertDecoderDREDSILKStateBitsMatch(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "live 16k second-loss frame-size sequence oracle silk")
		})
	}
}

func lpcnetplcTestQuantizePCMUpdateFloat(sample float32) float32 {
	return float32(float32ToInt16(sample)) * (1.0 / 32768.0)
}

func requireDREDRuntimeForTest(t *testing.T) {
	t.Helper()
	if !extsupport.DREDRuntime {
		t.Skip("DRED runtime disabled in this build")
	}
}

func makeExperimentalDREDPayloadBodyForTest(t *testing.T, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	rawOffset := 16 - dredOffset + dredFrameOffset
	if rawOffset < 0 || rawOffset >= 32 {
		t.Fatalf("rawOffset=%d out of range for dredOffset=%d frameOffset=%d", rawOffset, dredOffset, dredFrameOffset)
	}

	var enc rangecoding.Encoder
	enc.Init(make([]byte, internaldred.MinBytes))
	enc.EncodeUniform(6, 16)
	enc.EncodeUniform(3, 8)
	enc.EncodeUniform(0, 2)
	enc.EncodeUniform(uint32(rawOffset), 32)
	enc.Shrink(internaldred.MinBytes)
	return enc.Done()
}

func makeValidDREDDecoderTestDNNBlob() []byte {
	var blob []byte
	for _, spec := range rdovae.DecoderLayerSpecs() {
		blob = appendDREDDecoderLayerTestRecords(blob, spec)
	}
	return blob
}

func makeValidDecoderControlWithDREDDecoderTestDNNBlob() []byte {
	blob := append([]byte(nil), makeValidDecoderTestDNNBlob()...)
	for _, spec := range rdovae.DecoderLayerSpecs() {
		blob = appendDREDDecoderLayerTestRecords(blob, spec)
	}
	return blob
}

func appendDREDDecoderLayerTestRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendDREDDecoderRecord(dst, spec.Bias, dnnblob.TypeFloat, encodeTestFloat32Payload(spec.NbOutputs))
	}
	if spec.Subias != "" {
		dst = appendDREDDecoderRecord(dst, spec.Subias, dnnblob.TypeFloat, encodeTestFloat32Payload(spec.NbOutputs))
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = appendDREDDecoderRecord(dst, spec.WeightsIdx, dnnblob.TypeInt, encodeTestInt32Payload(idx))
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendDREDDecoderRecord(dst, spec.Weights, dnnblob.TypeInt8, make([]byte, size))
		dst = appendDREDDecoderRecord(dst, spec.Scale, dnnblob.TypeFloat, encodeTestFloat32Payload(spec.NbOutputs))
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendDREDDecoderRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, make([]byte, 4*size))
	}
	return dst
}

func appendDREDDecoderRecord(dst []byte, name string, typ int32, payload []byte) []byte {
	const headerSize = 64
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	copy(out[headerSize:], payload)
	return append(dst, out...)
}

func encodeTestFloat32Payload(n int) []byte {
	return make([]byte, 4*n)
}

func encodeTestInt32Payload(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}

func setValidDREDDecoderBlobForTest(t *testing.T, dec *Decoder) {
	t.Helper()

	blob, err := dnnblob.Clone(makeValidDREDDecoderTestDNNBlob())
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
}

func makeValidCELTPacketForDREDTest(t *testing.T) []byte {
	t.Helper()

	enc := encpkg.NewEncoder(48000, 2)
	enc.SetMode(encpkg.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	pcm := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[2*i] = float32(0.45 * math.Sin(phase))
		pcm[2*i+1] = float32(0.35 * math.Sin(phase+0.37))
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode(CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(CELT) returned empty packet")
	}
	return packet
}

func makeValidMonoCELTPacketForDREDTest(t *testing.T) []byte {
	return makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 960)
}

func makeValidMonoCELTPacketForFrameSizeForDREDTest(t *testing.T, frameSize int) []byte {
	return makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthFullband)
}

func makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	enc := encpkg.NewEncoder(48000, 1)
	enc.SetMode(encpkg.ModeCELT)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(128000)

	pcm := make([]float32, frameSize)
	for i := range pcm {
		phase := 2 * math.Pi * 823 * float64(i) / 48000.0
		pcm[i] = float32(0.41 * math.Sin(phase))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(mono CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(mono CELT) returned empty packet")
	}
	return packet
}

func makeValidStereoCELTPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	enc := encpkg.NewEncoder(48000, 2)
	enc.SetMode(encpkg.ModeCELT)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(256000)
	enc.SetForceChannels(2)

	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		pcm[2*i] = float32(0.41 * math.Sin(2*math.Pi*823*tm))
		pcm[2*i+1] = float32(0.29 * math.Sin(2*math.Pi*617*tm+0.31))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(stereo CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(stereo CELT) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize || !toc.Stereo {
		t.Fatalf("Encode(stereo CELT) produced mode=%v bandwidth=%v frame=%d stereo=%t, want mode=%v bandwidth=%v frame=%d stereo=true", toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo, ModeCELT, bandwidth, frameSize)
	}
	return packet
}

func makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 {
		t.Fatalf("hybrid DRED test packet requires 10ms/20ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthSuperwideband && bandwidth != BandwidthFullband {
		t.Fatalf("hybrid DRED test packet requires SWB/FB bandwidth, got %v", bandwidth)
	}

	enc := encpkg.NewEncoder(48000, 1)
	enc.SetMode(encpkg.ModeHybrid)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(48000)

	pcm := make([]float32, frameSize)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = float32(0.28*math.Sin(2*math.Pi*173*tm) +
			0.17*math.Sin(2*math.Pi*347*tm+0.13) +
			0.09*math.Sin(2*math.Pi*521*tm+0.29))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(mono Hybrid): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(mono Hybrid) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeHybrid || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize {
		t.Fatalf("Encode(mono Hybrid) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeHybrid, bandwidth, frameSize)
	}
	return packet
}

func makeValidStereoHybridPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 {
		t.Fatalf("hybrid DRED test packet requires 10ms/20ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthSuperwideband && bandwidth != BandwidthFullband {
		t.Fatalf("hybrid DRED test packet requires SWB/FB bandwidth, got %v", bandwidth)
	}

	enc := encpkg.NewEncoder(48000, 2)
	enc.SetMode(encpkg.ModeHybrid)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(96000)
	enc.SetForceChannels(2)

	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		pcm[2*i] = float32(0.28*math.Sin(2*math.Pi*173*tm) +
			0.17*math.Sin(2*math.Pi*347*tm+0.13) +
			0.09*math.Sin(2*math.Pi*521*tm+0.29))
		pcm[2*i+1] = float32(0.25*math.Sin(2*math.Pi*211*tm+0.07) +
			0.14*math.Sin(2*math.Pi*431*tm+0.19) +
			0.07*math.Sin(2*math.Pi*653*tm+0.41))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(stereo Hybrid): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(stereo Hybrid) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeHybrid || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize || !toc.Stereo {
		t.Fatalf("Encode(stereo Hybrid) produced mode=%v bandwidth=%v frame=%d stereo=%t, want mode=%v bandwidth=%v frame=%d stereo=true", toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo, ModeHybrid, bandwidth, frameSize)
	}
	return packet
}

func makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t *testing.T, mode Mode, bandwidth Bandwidth, frameSize int) []byte {
	t.Helper()

	switch mode {
	case ModeCELT:
		return makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)
	case ModeHybrid:
		return makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, bandwidth)
	case ModeSILK:
		return makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, bandwidth)
	default:
		t.Fatalf("unsupported DRED test packet mode %v", mode)
		return nil
	}
}

func makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 && frameSize != 1920 && frameSize != 2880 {
		t.Fatalf("silk DRED test packet requires 10/20/40/60ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthNarrowband && bandwidth != BandwidthMediumband && bandwidth != BandwidthWideband {
		t.Fatalf("silk DRED test packet requires NB/MB/WB bandwidth, got %v", bandwidth)
	}

	enc := encpkg.NewEncoder(48000, 1)
	enc.SetMode(encpkg.ModeSILK)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(32000)

	pcm := make([]float32, frameSize)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = float32(0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(mono SILK): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(mono SILK) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize {
		t.Fatalf("Encode(mono SILK) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeSILK, bandwidth, frameSize)
	}
	return packet
}

func makeValidMono16kPacketForDREDTest(t *testing.T) []byte {
	t.Helper()

	enc := encpkg.NewEncoder(16000, 1)
	enc.SetMode(encpkg.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(24000)

	// One 20 ms frame at the native 16 kHz rate (320 samples). The encoder
	// honours the native-Fs frame-size contract, so the input length selects
	// the packet duration directly.
	const frameSize = 16000 / 50
	pcm := make([]float32, frameSize)
	for i := range pcm {
		phase := 2 * math.Pi * 613 * float64(i) / 16000.0
		pcm[i] = float32(0.42 * math.Sin(phase))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(16k mono): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(16k mono) returned empty packet")
	}
	return packet
}

func makeValidMono48kSILKPacketForDREDTest(t *testing.T) []byte {
	t.Helper()

	enc := encpkg.NewEncoder(48000, 1)
	enc.SetMode(encpkg.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(32000)

	const frameSize = 960
	pcm := make([]float32, frameSize)
	for i := range pcm {
		tm := float64(i) / 48000.0
		pcm[i] = float32(0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23))
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(48k SILK): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(48k SILK) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.FrameSize != frameSize {
		t.Fatalf("Encode(48k SILK) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeSILK, BandwidthWideband, frameSize)
	}
	return packet
}

func buildSingleFramePacketWithExtensionsForDREDTest(t *testing.T, packet []byte, extensions []packetExtensionData) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	dst := make([]byte, len(packet)+64)
	n, err := buildPacketWithOptions(packet[0]&0xFC, [][]byte{packet[1:]}, dst, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions: %v", err)
	}
	return dst[:n]
}

func TestDecoderCachesDREDPayloadWhenDREDModelLoaded(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDRecoveryState == nil {
		t.Fatal("Decode with cached DRED payload did not activate recovery state")
	}
	if state.dredCache.Len != len(body) {
		t.Fatalf("dredCache.Len=%d want %d", state.dredCache.Len, len(body))
	}
	if state.dredCache.Parsed.Header.DredOffset != 4 {
		t.Fatalf("dredCache.Parsed.Header.DredOffset=%d want 4", state.dredCache.Parsed.Header.DredOffset)
	}
	if state.dredCache.Parsed.Header.DredFrameOffset != 0 || state.dredCache.Parsed.Header.Q0 != 6 || state.dredCache.Parsed.Header.DQ != 3 || state.dredCache.Parsed.Header.QMax != 15 {
		t.Fatalf("dredCache.Parsed.Header=(frame=%d q0=%d dq=%d qmax=%d) want (0,6,3,15)", state.dredCache.Parsed.Header.DredFrameOffset, state.dredCache.Parsed.Header.Q0, state.dredCache.Parsed.Header.DQ, state.dredCache.Parsed.Header.QMax)
	}
	if !bytes.Equal(state.dredData[:state.dredCache.Len], body) {
		t.Fatalf("cached dred payload=%x want %x", state.dredData[:state.dredCache.Len], body)
	}
	result := dec.cachedDREDResult(960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != 480 || result.Availability.EndSamples != 0 || result.Availability.AvailableSamples != 0 {
		t.Fatalf("cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:480 EndSamples:0 AvailableSamples:0}", result)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples=%d want 0", got)
	}
	quant := make([]int32, 6)
	if n := dec.cachedDREDResult(10080).FillQuantizerLevels(quant); n != 0 {
		t.Fatalf("cachedDREDResult.FillQuantizerLevels count=%d want 0", n)
	}
	if want := []int32{0, 0, 0, 0, 0, 0}; !slices.Equal(quant, want) {
		t.Fatalf("cachedDREDResult.FillQuantizerLevels=%v want %v", quant, want)
	}
	window := dec.cachedDREDFeatureWindow(960, 960, 960, 0)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("cachedDREDFeatureWindow=%+v want base=1 recoverable=0 missing=2", window)
	}

	dec.Reset()
	if got := requireDecoderDREDState(t, dec).dredCache; got != (internaldred.Cache{}) {
		t.Fatalf("Reset left DRED cache=%+v want zero state", got)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples after Reset=%d want 0", got)
	}
}

func TestDecoderDREDRecoveryBlendFollowsLifecycle(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	state := requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload after successful decode")
	}
	if got := state.dredPLC.Blend(); got != 0 {
		t.Fatalf("Blend after good decode=%d want 0", got)
	}
	window := dec.cachedDREDRecoveryWindow(960, 960, 960)
	if window.FeatureOffsetBase != 3 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 4 {
		t.Fatalf("cachedDREDRecoveryWindow=%+v want base=3 recoverable=0 missing=4", window)
	}
	queued := dec.queueCachedDREDRecovery(960, 960, 960)
	if queued != window {
		t.Fatalf("queueCachedDREDRecovery=%+v want %+v", queued, window)
	}
	if state.dredPLC.FECFillPos() != 0 || state.dredPLC.FECSkip() != 4 {
		t.Fatalf("queued plc state=(fill=%d skip=%d) want (0,4)", state.dredPLC.FECFillPos(), state.dredPLC.FECSkip())
	}

	plcPCM := make([]float32, 960*2)
	if _, err := dec.Decode(nil, plcPCM); err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("Decode(nil) dropped cached DRED payload before recovery scheduling")
	}
	if got := state.dredPLC.Blend(); got != 1 {
		t.Fatalf("Blend after PLC=%d want 1", got)
	}

	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode after PLC error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload after re-decoding packet")
	}
	window = dec.cachedDREDRecoveryWindow(960, 960, 960)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("cachedDREDRecoveryWindow after PLC and re-decode=%+v want base=1 recoverable=0 missing=2", window)
	}
	queued = dec.queueCachedDREDRecovery(960, 960, 960)
	if queued != window {
		t.Fatalf("queueCachedDREDRecovery after PLC and re-decode=%+v want %+v", queued, window)
	}
	if state.dredPLC.FECFillPos() != 0 || state.dredPLC.FECSkip() != 2 {
		t.Fatalf("queued plc state after PLC and re-decode=(fill=%d skip=%d) want (0,2)", state.dredPLC.FECFillPos(), state.dredPLC.FECSkip())
	}
}

func TestDecoderPrimeDREDCELTEntryHistoryUsesCELTBridge(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode returned no audio")
	}

	var want [4 * lpcnetplc.FrameSize]float32
	if got := dec.celtDecoder.FillPLCUpdate16kMono(want[:]); got != len(want) {
		t.Fatalf("FillPLCUpdate16kMono()=%d want %d", got, len(want))
	}

	beforeAnalysis := lpcnetplc.PLCBufSize
	beforePredict := lpcnetplc.PLCBufSize
	if state := dec.dredState(); state != nil && state.decoderDREDRecoveryState != nil {
		beforeAnalysis = state.dredPLC.AnalysisPos()
		beforePredict = state.dredPLC.PredictPos()
	}
	if got := dec.primeDREDCELTEntryHistory(ModeCELT, false); got != len(want) {
		t.Fatalf("primeDREDCELTEntryHistory()=%d want %d", got, len(want))
	}
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.AnalysisPos(); got != max(0, beforeAnalysis-len(want)) {
		t.Fatalf("AnalysisPos=%d want %d", got, max(0, beforeAnalysis-len(want)))
	}
	if got := state.dredPLC.PredictPos(); got != max(0, beforePredict-len(want)) {
		t.Fatalf("PredictPos=%d want %d", got, max(0, beforePredict-len(want)))
	}

	var history [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(history[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory()=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	for i := range want {
		if history[lpcnetplc.PLCBufSize-len(want)+i] != want[i] {
			t.Fatalf("history tail[%d]=%v want %v", i, history[lpcnetplc.PLCBufSize-len(want)+i], want[i])
		}
	}
}

func TestDecoderDecodePLCAppliesNeuralConcealmentWhenReady(t *testing.T) {
	requireDREDRuntimeForTest(t)

	packet := makeValidMono16kPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity error: %v", err)
	}

	frameSize := 16000 / 50
	pcm := make([]float32, frameSize)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode()=%d want %d", n, frameSize)
	}
	if state := dec.dredState(); state != nil {
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("good decode eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("good decode eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
	}
	setValidDREDDecoderBlobForTest(t, dec)

	n, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode(nil)=%d want %d", n, frameSize)
	}
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.Blend(); got != 1 {
		t.Fatalf("Blend after neural PLC=%d want 1", got)
	}
	if got := state.dredPLC.PredictPos(); got != lpcnetplc.PLCBufSize {
		t.Fatalf("PredictPos after neural PLC=%d want %d", got, lpcnetplc.PLCBufSize)
	}
}

func TestDecoderCachesDREDSampleTimingForLaterFrame(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := testStereoCELTPacket()
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}
	body := makeExperimentalDREDPayloadBodyForTest(t, 8, -4)
	packet := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, packet, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 1920*2)
	got, err := dec.Decode(packet[:n], pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got != 1920 {
		t.Fatalf("Decode samples=%d want 1920", got)
	}
	state := requireDecoderDREDState(t, dec)
	if state.dredCache.Parsed.Header.DredOffset != -4 {
		t.Fatalf("dredCache.Parsed.Header.DredOffset=%d want -4", state.dredCache.Parsed.Header.DredOffset)
	}
	if state.dredCache.Parsed.Header.DredFrameOffset != 8 {
		t.Fatalf("dredCache.Parsed.Header.DredFrameOffset=%d want 8", state.dredCache.Parsed.Header.DredFrameOffset)
	}
	if state.dredCache.Parsed.Header.QMax != 15 {
		t.Fatalf("dredCache.Parsed.Header.QMax=%d want 15", state.dredCache.Parsed.Header.QMax)
	}
	result := dec.cachedDREDResult(960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", result)
	}
	window := dec.cachedDREDFeatureWindow(960, 3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("cachedDREDFeatureWindow=%+v want base=5 recoverable=0 missing=2", window)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 480 {
		t.Fatalf("cachedDREDMaxAvailableSamples=%d want 480", got)
	}
}

func TestDecoderDREDCacheFollowsStandaloneModelAndIgnoreExtensions(t *testing.T) {
	requireDREDRuntimeForTest(t)

	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(extended, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	state := requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload before main blob change")
	}

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(non_dred) error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if !state.dredModelLoaded {
		t.Fatal("main decoder SetDNNBlob cleared standalone DRED model state")
	}
	if state.dredCache.Empty() {
		t.Fatal("main decoder SetDNNBlob cleared cached DRED payload")
	}
	dec.setDREDDecoderBlob(nil)
	if state := dec.dredState(); state != nil && state.decoderDREDPayloadState != nil {
		t.Fatalf("clearing standalone DRED model left payload sidecar=%+v", state.decoderDREDPayloadState)
	}
	if dec.dredCachedPayloadActive() {
		t.Fatal("clearing standalone DRED model left cached payload active")
	}

	setValidDREDDecoderBlobForTest(t, dec)
	if _, err := dec.Decode(extended, pcm); err != nil {
		t.Fatalf("Decode after standalone rearm error: %v", err)
	}
	state = requireDecoderDREDState(t, dec)
	if state.dredCache.Empty() {
		t.Fatal("expected cached DRED payload before ignore toggle")
	}
	dec.SetIgnoreExtensions(true)
	if got := requireDecoderDREDState(t, dec).dredCache; got != (internaldred.Cache{}) {
		t.Fatalf("SetIgnoreExtensions(true) left DRED cache=%+v want zero state", got)
	}
}

// minimalHybridTestPacket20ms creates a test packet for Hybrid FB 20ms mono (config 15).
// This is a manually constructed packet that produces valid decoder output.
// The TOC byte (0x78) indicates: config=15 (Hybrid FB 20ms), mono, code 0 (single frame).

func requireDecoderDREDState(t testing.TB, dec *Decoder) *decoderDREDState {
	t.Helper()
	if dec == nil {
		t.Fatal("decoder is nil")
	}
	s := dec.dredState()
	if s == nil {
		t.Fatal("decoder DRED sidecar is nil")
	}
	return s
}

func assertDecoderDREDRuntimeLoadedForTest(t testing.TB, dec *Decoder, label string) {
	t.Helper()
	state := requireDecoderDREDState(t, dec)
	if !state.dredAnalysis.Loaded() || !state.dredPredictor.Loaded() || !state.dredFARGAN.Loaded() {
		t.Fatalf("decoder runtime models not loaded after %s", label)
	}
}

func setDecoderComplexityForLibopusDREDParityTest(t testing.TB, dec *Decoder) {
	t.Helper()
	if err := dec.SetComplexity(10); err != nil {
		t.Fatalf("SetComplexity(10) error: %v", err)
	}
}

func assertFloat32BitsEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%g want %g", label, i, got[i], want[i])
		}
	}
}

func TestEncoderSetDNNBlobRejectsNameOnlyModelBlob(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeNameCompleteEncoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(name-only encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
	if enc.dnnBlob != nil || enc.enc.DNNBlobLoaded() {
		t.Fatal("encoder retained name-only DNN blob")
	}
}

func TestDecoderSetDNNBlobRejectsNameOnlyModelBlob(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	if err := dec.SetDNNBlob(makeNameCompleteDecoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(name-only decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
	if dec.dnnBlob != nil || dec.pitchDNNLoaded || dec.plcModelLoaded || dec.farganModelLoaded {
		t.Fatal("decoder retained name-only DNN blob")
	}
}

func TestDecoderSetDNNBlobIgnoresNameOnlyDREDDecoderFamily(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	blob := append([]byte(nil), makeValidDecoderTestDNNBlob()...)
	blob = append(blob, makeNameCompleteDREDDecoderTestDNNBlob()...)

	if err := dec.SetDNNBlob(blob); err != nil {
		t.Fatalf("SetDNNBlob(core blob with name-only DRED decoder family) error=%v want nil", err)
	}
	if dec.dnnBlob == nil || !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder did not retain valid core DNN blob with ignored DRED decoder extras")
	}
	if dec.dredPayloadScannerActive() || dec.dredCachedPayloadActive() {
		t.Fatal("decoder armed standalone DRED payload state from ignored DRED decoder extras")
	}
	if state := dec.dredState(); state != nil {
		t.Fatalf("decoder allocated DRED sidecar from ignored DRED decoder extras: %+v", state)
	}
}

func TestEncoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	enc.Reset()
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func TestDecoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	dec := mustNewTestDecoder(t, 16000, 1)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags not armed from validated blob")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder eagerly allocated DRED sidecar on SetDNNBlob: %+v", dec.dredState())
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("decoder failed to lazily materialize neural concealment runtime")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "lazy materialization")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags cleared by Reset")
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("decoder failed to rematerialize neural concealment runtime after Reset")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "Reset rematerialization")
	}
}

func TestDecoderSetDNNBlobStereoRuntimeRetainedAcrossReset(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags not armed from validated blob")
	}
	if dec.dredState() != nil {
		t.Fatalf("stereo decoder eagerly allocated DRED sidecar on SetDNNBlob: %+v", dec.dredState())
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("stereo decoder failed to lazily materialize neural concealment runtime")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "stereo lazy materialization")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags cleared by Reset")
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("stereo decoder failed to rematerialize neural concealment runtime after Reset")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "stereo Reset rematerialization")
	}
}

func TestMultistreamEncoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	enc.Reset()
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func TestMultistreamDecoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.dec.PitchDNNLoaded() || !dec.dec.PLCModelLoaded() || !dec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder runtime models not loaded from retained DNN blob")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.dec.PitchDNNLoaded() || !dec.dec.PLCModelLoaded() || !dec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder runtime models cleared by Reset")
	}
}

func TestHotPathAllocsDecodePLCDNNReadyAtMostBaseline(t *testing.T) {
	baseline, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	armed, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := armed.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	packet := testCELTPacket()
	baselinePCM := make([]float32, 960)
	armedPCM := make([]float32, 960)

	if _, err := baseline.Decode(packet, baselinePCM); err != nil {
		t.Fatalf("baseline warmup Decode: %v", err)
	}
	if _, err := baseline.Decode(nil, baselinePCM); err != nil {
		t.Fatalf("baseline warmup Decode PLC: %v", err)
	}
	if _, err := armed.Decode(packet, armedPCM); err != nil {
		t.Fatalf("armed warmup Decode: %v", err)
	}
	if _, err := armed.Decode(nil, armedPCM); err != nil {
		t.Fatalf("armed warmup Decode PLC: %v", err)
	}

	baselineAllocs := testing.AllocsPerRun(100, func() {
		if _, err := baseline.Decode(nil, baselinePCM); err != nil {
			t.Fatalf("baseline Decode PLC: %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(100, func() {
		if _, err := armed.Decode(nil, armedPCM); err != nil {
			t.Fatalf("armed Decode PLC: %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("Decode(PLC, DNN ready) allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
}

var (
	defaultLibopusPitchDNNBlobHelper    libopustest.HelperCache
	defaultLibopusPLCBlobHelper         libopustest.HelperCache
	defaultLibopusFARGANBlobHelper      libopustest.HelperCache
	defaultLibopusDREDEncoderBlobHelper libopustest.HelperCache
)

func TestDNNBlobControlAcceptsLibopusModelBlobs(t *testing.T) {
	if !libopustest.StrictRefRequired() {
		t.Skip("requires GOPUS_STRICT_LIBOPUS_REF=1")
	}

	encoderBlob := requireDefaultLibopusEncoderDNNBlob(t)
	decoderBlob := requireDefaultLibopusDecoderDNNBlob(t)

	encoderParsed, err := dnnblob.Clone(encoderBlob)
	if err != nil {
		t.Fatalf("Clone(libopus encoder blob) error: %v", err)
	}
	if err := encoderParsed.ValidateEncoderControl(); err != nil {
		t.Fatalf("ValidateEncoderControl(libopus encoder blob) error: %v", err)
	}
	if !encoderParsed.SupportsPitchDNN() || !encoderParsed.SupportsDREDEncoder() {
		t.Fatal("libopus encoder blob missing expected PitchDNN or DRED encoder families")
	}

	decoderParsed, err := dnnblob.Clone(decoderBlob)
	if err != nil {
		t.Fatalf("Clone(libopus decoder blob) error: %v", err)
	}
	if err := decoderParsed.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(libopus decoder blob) error: %v", err)
	}
	models := decoderParsed.DecoderModels()
	if !models.PitchDNN || !models.PLC || !models.FARGAN {
		t.Fatalf("libopus decoder blob model families=%+v, want PitchDNN/PLC/FARGAN", models)
	}
	if models.DRED || models.OSCE || models.OSCEBWE {
		t.Fatalf("default libopus decoder blob unexpectedly reports extra-control-only families: %+v", models)
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := enc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("Encoder.SetDNNBlob(libopus encoder blob) error: %v", err)
	}
	if enc.dnnBlob == nil || !enc.enc.DNNBlobLoaded() {
		t.Fatal("encoder did not retain libopus encoder DNN blob")
	}
	rejectEnc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := rejectEnc.SetDNNBlob(decoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Encoder.SetDNNBlob(libopus decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	dec := newMonoTestDecoder(t)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("Decoder.SetDNNBlob(libopus decoder blob) error: %v", err)
	}
	if dec.dnnBlob == nil || !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder did not retain libopus decoder DNN blob model state")
	}
	if dec.dredState() != nil {
		t.Fatalf("default decoder SetDNNBlob allocated DRED sidecar: %+v", dec.dredState())
	}
	rejectDec := newMonoTestDecoder(t)
	if err := rejectDec.SetDNNBlob(encoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Decoder.SetDNNBlob(libopus encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := msEnc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("MultistreamEncoder.SetDNNBlob(libopus encoder blob) error: %v", err)
	}
	if msEnc.dnnBlob == nil || !msEnc.enc.DNNBlobLoaded() {
		t.Fatal("multistream encoder did not retain libopus encoder DNN blob")
	}
	rejectMSEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := rejectMSEnc.SetDNNBlob(decoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MultistreamEncoder.SetDNNBlob(libopus decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := msDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("MultistreamDecoder.SetDNNBlob(libopus decoder blob) error: %v", err)
	}
	if msDec.dnnBlob == nil || !msDec.dec.PitchDNNLoaded() || !msDec.dec.PLCModelLoaded() || !msDec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder did not retain libopus decoder DNN blob model state")
	}
	rejectMSDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := rejectMSDec.SetDNNBlob(encoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MultistreamDecoder.SetDNNBlob(libopus encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
}

func requireDefaultLibopusEncoderDNNBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeDefaultLibopusEncoderDNNBlob()
	if err != nil {
		t.Fatalf("libopus encoder DNN blob helper unavailable: %v", err)
	}
	return blob
}

func requireDefaultLibopusDecoderDNNBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeDefaultLibopusDecoderDNNBlob()
	if err != nil {
		t.Fatalf("libopus decoder DNN blob helper unavailable: %v", err)
	}
	return blob
}

func probeDefaultLibopusEncoderDNNBlob() ([]byte, error) {
	pitchBlob, err := runDefaultLibopusPitchDNNBlobHelper()
	if err != nil {
		return nil, err
	}
	dredEncoderBlob, err := runDefaultLibopusDREDEncoderBlobHelper()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredEncoderBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredEncoderBlob...)
	return blob, nil
}

func probeDefaultLibopusDecoderDNNBlob() ([]byte, error) {
	pitchBlob, err := runDefaultLibopusPitchDNNBlobHelper()
	if err != nil {
		return nil, err
	}
	plcBlob, err := runDefaultLibopusPLCBlobHelper()
	if err != nil {
		return nil, err
	}
	farganBlob, err := runDefaultLibopusFARGANBlobHelper()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func runDefaultLibopusPitchDNNBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusPitchDNNBlobHelper, "libopus_pitchdnn_model_blob.c", "gopus_default_libopus_pitchdnn_model_blob")
}

func runDefaultLibopusPLCBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusPLCBlobHelper, "libopus_plc_model_blob.c", "gopus_default_libopus_plc_model_blob")
}

func runDefaultLibopusFARGANBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusFARGANBlobHelper, "libopus_fargan_model_blob.c", "gopus_default_libopus_fargan_model_blob")
}

func runDefaultLibopusDREDEncoderBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusDREDEncoderBlobHelper, "libopus_dred_encoder_model_blob.c", "gopus_default_libopus_dred_encoder_model_blob")
}

func runCachedDefaultLibopusDNNBlobHelper(cache *libopustest.HelperCache, sourceFile, outputBase string) ([]byte, error) {
	binPath, err := cache.Path(func() (string, error) {
		return buildDefaultLibopusDNNHelper(sourceFile, outputBase)
	})
	if err != nil {
		return nil, err
	}
	return runDefaultLibopusDNNBlobHelper(binPath)
}

func runDefaultLibopusDNNBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
}

func buildDefaultLibopusDNNHelper(sourceFile, outputBase string) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, true)
}

func TestDecodeDREDInt24NoOverflowOrPrematureAlloc(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	initialCap := cap(dec.scratchPCM)

	// Overflowing frameSizeSamples * channels must return an error, never panic.
	pcm24 := make([]int32, 960*2)
	if _, err := dec.DecodeDREDInt24(NewDRED(), 0, pcm24, math.MaxInt/2+1); err == nil {
		t.Fatal("DecodeDREDInt24 accepted overflowing frameSizeSamples")
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeDREDInt24 grew scratchPCM cap from %d to %d on invalid call", initialCap, cap(dec.scratchPCM))
	}

	pcmF32 := make([]float32, 960*2)
	if _, err := dec.DecodeDRED(NewDRED(), 0, pcmF32, math.MaxInt/2+1); err == nil {
		t.Fatal("DecodeDRED accepted overflowing frameSizeSamples")
	}
}

func TestDREDDecoderParseAndProcessRetainsMetadata(t *testing.T) {
	libopustest.RequireOracle(t)
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	dred := NewDRED()

	available, dredEnd, err := dec.Parse(dred, packet, 960, 48000, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if available != 480 || dredEnd != 480 {
		t.Fatalf("Parse=(available=%d,dredEnd=%d) want (480,480)", available, dredEnd)
	}
	if dred.ProcessStage() != DREDProcessStageDeferred || !dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("stage after deferred parse = (%d, needs=%v, processed=%v) want (%d,true,false)", dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed(), DREDProcessStageDeferred)
	}
	if dred.RawProcessStage() != 1 {
		t.Fatalf("RawProcessStage()=%d want 1", dred.RawProcessStage())
	}
	if dred.Len() == 0 {
		t.Fatal("deferred parse did not retain DRED payload")
	}
	if got := dred.Parsed().Header.DredOffset; got != -4 {
		t.Fatalf("Parsed().Header.DredOffset=%d want -4", got)
	}
	if got := dred.Parsed().Header.DredFrameOffset; got != 8 {
		t.Fatalf("Parsed().Header.DredFrameOffset=%d want 8", got)
	}
	decodeWant, err := probeLibopusDREDDecode(packet, 960, 48000)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred decode", err)
	}
	if decodeWant.availableSamples < 0 {
		t.Fatalf("libopus dred decode returned error %d", decodeWant.availableSamples)
	}
	if got := dred.LatentCount(); got != decodeWant.nbLatents {
		t.Fatalf("LatentCount()=%d want %d", got, decodeWant.nbLatents)
	}
	state := make([]float32, internaldred.StateDim)
	if n := dred.FillState(state); n != internaldred.StateDim {
		t.Fatalf("FillState count=%d want %d", n, internaldred.StateDim)
	}
	assertFloat32BitsEqual(t, state, decodeWant.state[:], "state")
	latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
	wantLatents := decodeWant.nbLatents * internaldred.LatentStride
	if n := dred.FillLatents(latents); n != wantLatents {
		t.Fatalf("FillLatents count=%d want %d", n, wantLatents)
	}
	assertFloat32BitsEqual(t, latents[:wantLatents], decodeWant.latents, "latents")

	processed := NewDRED()
	if err := dec.Process(dred, processed); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if processed.ProcessStage() != DREDProcessStageProcessed || processed.NeedsProcessing() || !processed.Processed() {
		t.Fatalf("processed stage = (%d, needs=%v, processed=%v) want (%d,false,true)", processed.ProcessStage(), processed.NeedsProcessing(), processed.Processed(), DREDProcessStageProcessed)
	}
	if processed.RawProcessStage() != 2 {
		t.Fatalf("RawProcessStage()=%d want 2", processed.RawProcessStage())
	}
	result := processed.Result(960, 48000)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("Result=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", result)
	}
	if got := processed.Availability(960, 48000); got != result.Availability {
		t.Fatalf("Availability()=%+v want %+v", got, result.Availability)
	}
	if got := processed.MaxAvailableSamples(960, 48000); got != 480 {
		t.Fatalf("MaxAvailableSamples()=%d want 480", got)
	}
	quant := make([]int32, 6)
	if n := processed.FillQuantizerLevels(quant, 10080, 48000); n != 0 {
		t.Fatalf("FillQuantizerLevels count=%d want 0", n)
	}
	window := result.FeatureWindow(3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("FeatureWindow=%+v want base=5 recoverable=0 missing=2", window)
	}
	if got := processed.FeatureWindow(960, 48000, 3840, 960, 0); got != window {
		t.Fatalf("FeatureWindow()=%+v want %+v", got, window)
	}
	if err := dec.Process(processed, processed); err != nil {
		t.Fatalf("Process(processed, processed) error: %v", err)
	}
	processedState := make([]float32, internaldred.StateDim)
	if n := processed.FillState(processedState); n != internaldred.StateDim {
		t.Fatalf("processed FillState count=%d want %d", n, internaldred.StateDim)
	}
	assertFloat32BitsEqual(t, processedState, state, "processed state")
	processedLatents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
	if n := processed.FillLatents(processedLatents); n != wantLatents {
		t.Fatalf("processed FillLatents count=%d want %d", n, wantLatents)
	}
	assertFloat32BitsEqual(t, processedLatents[:wantLatents], latents[:wantLatents], "processed latents")
	if got, want := processed.FeatureCount(), decodeWant.nbLatents*4*internaldred.NumFeatures; got != want {
		t.Fatalf("FeatureCount()=%d want %d", got, want)
	}
	features := make([]float32, processed.FeatureCount())
	if n := processed.FillFeatures(features); n != len(features) {
		t.Fatalf("FillFeatures count=%d want %d", n, len(features))
	}
	for i, v := range features {
		if v != 0 {
			t.Fatalf("FillFeatures[%d]=%v want 0 for zeroed synthetic model", i, v)
		}
	}
}

func TestStandaloneDREDParseMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	base := makeValidCELTPacketForDREDTest(t)
	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "valid_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "first_extension_invalid_second_valid",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "short_experimental_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}},
			}),
			maxDREDSamples: 960,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dred := NewDRED()
			available, dredEnd, err := dec.Parse(dred, tc.packet, tc.maxDREDSamples, 48000, true)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if dred.ProcessStage() != DREDProcessStageDeferred || !dred.NeedsProcessing() || dred.Processed() {
				t.Fatalf("stage after deferred Parse = (%d, needs=%v, processed=%v) want (%d,true,false)", dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed(), DREDProcessStageDeferred)
			}

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred parse", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}
			if available != want.availableSamples {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if dredEnd != want.dredEndSamples {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}

			decodeWant, err := probeLibopusDREDDecode(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred decode", err)
			}
			if decodeWant.availableSamples < 0 {
				t.Fatalf("libopus dred decode returned error %d", decodeWant.availableSamples)
			}
			if dred.RawProcessStage() != decodeWant.processStage {
				t.Fatalf("RawProcessStage()=%d want %d", dred.RawProcessStage(), decodeWant.processStage)
			}
			if got := dred.Parsed().Header.DredOffset; got != int32(decodeWant.dredOffset) {
				t.Fatalf("Parsed().Header.DredOffset=%d want %d", got, decodeWant.dredOffset)
			}
			if got := dred.LatentCount(); got != decodeWant.nbLatents {
				t.Fatalf("LatentCount()=%d want %d", got, decodeWant.nbLatents)
			}

			state := make([]float32, internaldred.StateDim)
			if n := dred.FillState(state); n != internaldred.StateDim {
				t.Fatalf("FillState count=%d want %d", n, internaldred.StateDim)
			}
			assertFloat32BitsEqual(t, state, decodeWant.state[:], "state")

			latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
			wantLatents := decodeWant.nbLatents * internaldred.LatentStride
			if n := dred.FillLatents(latents); n != wantLatents {
				t.Fatalf("FillLatents count=%d want %d", n, wantLatents)
			}
			assertFloat32BitsEqual(t, latents[:wantLatents], decodeWant.latents, "latents")
		})
	}
}

func makeTwoFramePacketWithDREDForStandaloneTest(t *testing.T, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}
	body := makeExperimentalDREDPayloadBodyForTest(t, dredFrameOffset, dredOffset)
	packet := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, packet, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	return packet[:n]
}

func TestDREDDecoderParseRequiresModel(t *testing.T) {
	dec := NewDREDDecoder()
	dred := NewDRED()
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)

	if _, _, err := dec.Parse(dred, packet, 960, 48000, true); !errors.Is(err, ErrDREDModelNotLoaded) {
		t.Fatalf("Parse without model error=%v want=%v", err, ErrDREDModelNotLoaded)
	}
	if dred.RawProcessStage() != -1 {
		t.Fatalf("RawProcessStage()=%d want -1", dred.RawProcessStage())
	}
	if dred.ProcessStage() != DREDProcessStageEmpty || !dred.Empty() || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse without model mutated dred state: stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderSetDNNBlobRejectsNameOnlyModelBlob(t *testing.T) {
	dec := NewDREDDecoder()

	if err := dec.SetDNNBlob(makeNameCompleteDREDDecoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(name-only DRED decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
	if dec.ModelLoaded() {
		t.Fatal("DRED decoder retained name-only model blob")
	}
}

func TestDREDDecoderParseClearsStateWhenPacketHasNoDRED(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	withDRED := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(dred, withDRED, 960, 48000, false); err != nil {
		t.Fatalf("Parse(withDRED) error: %v", err)
	}
	if dred.Empty() {
		t.Fatal("expected retained DRED payload after Parse(withDRED)")
	}

	withoutDRED := makeValidCELTPacketForDREDTest(t)
	available, dredEnd, err := dec.Parse(dred, withoutDRED, 960, 48000, false)
	if err != nil {
		t.Fatalf("Parse(withoutDRED) error: %v", err)
	}
	if available != 0 || dredEnd != 0 {
		t.Fatalf("Parse(withoutDRED)=(available=%d,dredEnd=%d) want (0,0)", available, dredEnd)
	}
	if !dred.Empty() || dred.ProcessStage() != DREDProcessStageEmpty || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse(withoutDRED) left dred state stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderProcessRejectsEmptyState(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	if err := dec.Process(dred, dred); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Process(empty, empty) error=%v want=%v", err, ErrInvalidArgument)
	}
}

func TestDREDDecoderProcessDoesNotAllocate(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	src := NewDRED()
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(src, packet, 960, 48000, true); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	dst := NewDRED()

	allocs := testing.AllocsPerRun(1000, func() {
		if err := dec.Process(src, dst); err != nil {
			t.Fatalf("Process error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestDREDDecoderParseAndProcessDoesNotAllocate(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	dred := NewDRED()

	allocs := testing.AllocsPerRun(1000, func() {
		if _, _, err := dec.Parse(dred, packet, 960, 48000, false); err != nil {
			t.Fatalf("Parse error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestDREDDecoderParseClearsStateOnMalformedPacket(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	valid := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(dred, valid, 960, 48000, false); err != nil {
		t.Fatalf("Parse(valid) error: %v", err)
	}
	if dred.Empty() || !dred.Processed() {
		t.Fatal("expected processed retained DRED state after valid parse")
	}

	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, makeValidCELTPacketForDREDTest(t))
	if _, _, err := dec.Parse(dred, malformed, 960, 48000, false); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Parse(malformed) error=%v want=%v", err, ErrInvalidPacket)
	}
	if !dred.Empty() || dred.Len() != 0 || dred.ProcessStage() != DREDProcessStageEmpty || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse(malformed) left stale dred state: len=%d stage=%d needs=%v processed=%v", dred.Len(), dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed())
	}
}

func buildLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, includeInternal)
}

func cachedLibopusDREDHelperPath(cache *libopustest.HelperCache, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return cache.Path(func() (string, error) {
		return buildLibopusDREDHelper(sourceFile, outputBase, includeInternal)
	})
}

var (
	libopusPitchDNNModelBlobHelper libopustest.HelperCache
	libopusPLCModelBlobHelper      libopustest.HelperCache
	libopusFARGANModelBlobHelper   libopustest.HelperCache
)

func getLibopusPitchDNNModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusPitchDNNModelBlobHelper, "libopus_pitchdnn_model_blob.c", "gopus_libopus_pitchdnn_model_blob", true)
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusPLCModelBlobHelper, "libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob", true)
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusFARGANModelBlobHelper, "libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob", true)
}

func runModelBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDecoderNeuralModelBlob() ([]byte, error) {
	pitchPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	plcPath, err := getLibopusPLCModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	farganPath, err := getLibopusFARGANModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	pitchBlob, err := runModelBlobHelper(pitchPath)
	if err != nil {
		return nil, err
	}
	plcBlob, err := runModelBlobHelper(plcPath)
	if err != nil {
		return nil, err
	}
	farganBlob, err := runModelBlobHelper(farganPath)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func requireLibopusDecoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder neural model", err)
	}
	return blob
}

const libopusDREDPacketOutputMagic = "GODP"
const libopusDREDPacketMaxFramesToTry = 640

type libopusDREDPacket struct {
	sampleRate     int
	maxDREDSamples int
	frameIndex     int
	packet         []byte
}

func scaleDREDSampleCount(samples, fromRate, toRate int) int {
	if samples <= 0 || fromRate <= 0 || toRate <= 0 || fromRate == toRate {
		return samples
	}
	return samples * toRate / fromRate
}

func scaleDREDSampleOffset(samples, fromRate, toRate int) int {
	if samples == 0 || fromRate <= 0 || toRate <= 0 || fromRate == toRate {
		return samples
	}
	return samples * toRate / fromRate
}

func libopusDREDRequestForDecoder(packetInfo libopusDREDPacket, decoderSampleRate int) (maxDREDSamples, sampleRate int) {
	sampleRate = decoderSampleRate
	if sampleRate <= 0 {
		sampleRate = packetInfo.sampleRate
	}
	maxDREDSamples = packetInfo.maxDREDSamples
	if packetInfo.sampleRate > 0 && sampleRate > 0 {
		maxDREDSamples = scaleDREDSampleCount(maxDREDSamples, packetInfo.sampleRate, sampleRate)
	}
	return maxDREDSamples, sampleRate
}

type libopusDREDPacketConfig struct {
	FrameSize     int
	ForceMode     Mode
	Bandwidth     Bandwidth
	Channels      int
	ForceChannels int
	Bitrate       int
	CBR           bool
	Multistream   bool
	// DREDDuration is the OPUS_SET_DRED_DURATION value passed to libopus. Zero
	// selects the helper default (80).
	DREDDuration int
}

var libopusDREDEmitPacketHelper libopustest.HelperCache

func getLibopusDREDEmitPacketHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDEmitPacketHelper, "libopus_dred_emit_packet.c", "gopus_libopus_dred_emit_packet", true)
}

func emitLibopusDREDPacket() (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func emitLibopusDREDPacketWithFrameSize(frameSize int) (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func emitLibopusDREDPacketWithConfig(cfg libopusDREDPacketConfig) (libopusDREDPacket, error) {
	return emitLibopusDREDPacketWithConfigRecord(cfg, false)
}

// The record variant returns frameIndex=640 and an empty packet when the
// selected libopus encoder emitted no DRED across the complete search window.
func emitLibopusDREDPacketWithConfigRecord(cfg libopusDREDPacketConfig, recordAbsence bool) (libopusDREDPacket, error) {
	binPath, err := getLibopusDREDEmitPacketHelperPath()
	if err != nil {
		return libopusDREDPacket{}, err
	}
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = encoderDREDBitrateForFrameSize(cfg.FrameSize)
	}
	forceModeEnv, err := libopusDREDForceModeEnv(cfg.ForceMode)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	bandwidthEnv, err := libopusDREDBandwidthEnv(cfg.Bandwidth)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	env := []string{
		fmt.Sprintf("GOPUS_DRED_FRAME_SIZE=%d", cfg.FrameSize),
		fmt.Sprintf("GOPUS_DRED_CHANNELS=%d", cfg.Channels),
		fmt.Sprintf("GOPUS_DRED_FORCE_MODE=%s", forceModeEnv),
		fmt.Sprintf("GOPUS_DRED_BANDWIDTH=%s", bandwidthEnv),
		fmt.Sprintf("GOPUS_DRED_BITRATE=%d", cfg.Bitrate),
		"GOPUS_DRED_PCM_STDIN=1",
	}
	if cfg.CBR {
		env = append(env, "GOPUS_DRED_CBR=1")
	}
	if cfg.ForceChannels != 0 {
		env = append(env, fmt.Sprintf("GOPUS_DRED_FORCE_CHANNELS=%d", cfg.ForceChannels))
	}
	if cfg.Multistream {
		env = append(env, "GOPUS_DRED_MULTISTREAM=1")
	}
	if cfg.DREDDuration != 0 {
		env = append(env, fmt.Sprintf("GOPUS_DRED_DURATION=%d", cfg.DREDDuration))
	}
	if recordAbsence {
		env = append(env, "GOPUS_DRED_EMISSION_RECORD=1")
	}
	out, err := libopustest.RunHelperEnv(binPath, libopusDREDPacketPCMInput(cfg, libopusDREDPacketMaxFramesToTry), env)
	if err != nil {
		return libopusDREDPacket{}, fmt.Errorf("run dred emit helper: %w", err)
	}

	reader, version, err := libopustest.NewOracleReaderVersion("dred emit", libopusDREDPacketOutputMagic, out)
	if err != nil {
		return libopusDREDPacket{}, err
	}
	if recordAbsence && version != 3 || !recordAbsence && version != 1 && version != 2 {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper version=%d recordAbsence=%t", version, recordAbsence)
	}
	info := libopusDREDPacket{
		sampleRate:     int(reader.U32()),
		maxDREDSamples: int(reader.U32()),
	}
	packetLen := int(reader.U32())
	if version >= 2 {
		info.frameIndex = int(reader.U32())
	}
	packet := reader.Bytes(packetLen)
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDPacket{}, err
	}
	info.packet = append([]byte(nil), packet...)
	if len(info.packet) == 0 && recordAbsence && info.frameIndex == libopusDREDPacketMaxFramesToTry {
		return info, nil
	}
	if len(info.packet) == 0 {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper returned an empty packet")
	}
	toc := ParseTOC(info.packet[0])
	if toc.Mode != cfg.ForceMode {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper mode=%v want %v", toc.Mode, cfg.ForceMode)
	}
	if toc.Bandwidth != cfg.Bandwidth {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper bandwidth=%v want %v", toc.Bandwidth, cfg.Bandwidth)
	}
	if cfg.ForceChannels != 0 && toc.Stereo != (cfg.ForceChannels == 2) {
		gotChannels := 1
		if toc.Stereo {
			gotChannels = 2
		}
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper channels=%d want %d", gotChannels, cfg.ForceChannels)
	}
	packetDuration, err := opusPacketDurationSamples(info.packet)
	if err != nil {
		return libopusDREDPacket{}, fmt.Errorf("parse dred emit helper packet duration: %w", err)
	}
	if packetDuration != cfg.FrameSize {
		return libopusDREDPacket{}, fmt.Errorf("dred emit helper packet duration=%d want %d", packetDuration, cfg.FrameSize)
	}
	return info, nil
}

func libopusDREDPacketPCMInput(cfg libopusDREDPacketConfig, maxFrames int) []byte {
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 960
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1
	}
	out := make([]byte, 0, maxFrames*cfg.FrameSize*cfg.Channels*4)
	var tmp [4]byte
	for frameIdx := 0; frameIdx < maxFrames; frameIdx++ {
		for sampleIdx := 0; sampleIdx < cfg.FrameSize; sampleIdx++ {
			bits := math.Float32bits(encoderDREDVoicedSample(frameIdx, sampleIdx, cfg.FrameSize, 48000))
			binary.LittleEndian.PutUint32(tmp[:], bits)
			for ch := 0; ch < cfg.Channels; ch++ {
				out = append(out, tmp[:]...)
			}
		}
	}
	return out
}

func opusPacketDurationSamples(packet []byte) (int, error) {
	info, err := ParsePacket(packet)
	if err != nil {
		return 0, err
	}
	return info.TOC.FrameSize * info.FrameCount, nil
}

func libopusDREDForceModeEnv(mode Mode) (string, error) {
	switch mode {
	case ModeCELT:
		return "celt", nil
	case ModeHybrid:
		return "hybrid", nil
	case ModeSILK:
		return "silk", nil
	default:
		return "", fmt.Errorf("extra libopus dred packet force mode %v", mode)
	}
}

func libopusDREDBandwidthEnv(bandwidth Bandwidth) (string, error) {
	switch bandwidth {
	case BandwidthWideband:
		return "wb", nil
	case BandwidthSuperwideband:
		return "swb", nil
	case BandwidthFullband:
		return "fb", nil
	default:
		return "", fmt.Errorf("extra libopus dred packet bandwidth %v", bandwidth)
	}
}

func TestParsedDREDAvailabilityMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}

	twoFramePacket := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, twoFramePacket, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 8, -4)...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	twoFramePacket = twoFramePacket[:n]

	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "single_frame_offset_positive",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name:           "single_frame_offset_positive_large_request",
			packet:         buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)}}),
			maxDREDSamples: 10080,
		},
		{
			name:           "second_frame_negative_offset",
			packet:         twoFramePacket,
			maxDREDSamples: 960,
		},
		{
			name: "first_dred_extension_invalid_second_valid",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "short_experimental_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}},
			}),
			maxDREDSamples: 960,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, frameOffset, ok, err := findDREDPayload(tc.packet)
			if err != nil {
				t.Fatalf("findDREDPayload error: %v", err)
			}
			if !ok {
				t.Fatal("findDREDPayload returned ok=false")
			}

			parsed, err := internaldred.ParsePayload(payload, frameOffset)
			if err != nil {
				t.Fatalf("ParsePayload error: %v", err)
			}
			got := parsed.Availability(tc.maxDREDSamples, 48000)

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred parse", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}

			if got.AvailableSamples != want.availableSamples {
				t.Fatalf("AvailableSamples=%d want %d", got.AvailableSamples, want.availableSamples)
			}
			if got.EndSamples != want.dredEndSamples {
				t.Fatalf("EndSamples=%d want %d", got.EndSamples, want.dredEndSamples)
			}

			span := internaldred.LatentSpanSamples(48000)
			if span <= 0 {
				t.Fatal("invalid latent span")
			}
			wantLatents := (want.availableSamples + got.OffsetSamples) / span
			if wantLatents < 0 {
				wantLatents = 0
			}
			if got.MaxLatents != wantLatents {
				t.Fatalf("MaxLatents=%d want %d", got.MaxLatents, wantLatents)
			}
		})
	}
}

const (
	libopusDREDParseInputMagic  = "GODI"
	libopusDREDParseOutputMagic = "GODO"
)

type libopusDREDParseInfo struct {
	availableSamples int
	dredEndSamples   int
}

type libopusDREDDecodeInfo struct {
	availableSamples int
	dredEndSamples   int
	processStage     int
	nbLatents        int
	dredOffset       int
	state            [internaldred.StateDim]float32
	latents          []float32
}

var (
	libopusDREDParseHelper  libopustest.HelperCache
	libopusDREDDecodeHelper libopustest.HelperCache
)

func getLibopusDREDParseHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDParseHelper, "libopus_dred_parse_info.c", "gopus_libopus_dred_parse", false)
}

func getLibopusDREDDecodeHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDDecodeHelper, "libopus_dred_decode_info.c", "gopus_libopus_dred_decode", true)
}

func probeLibopusDREDParse(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDParseInfo, error) {
	binPath, err := getLibopusDREDParseHelperPath()
	if err != nil {
		return libopusDREDParseInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDParseInfo{}, fmt.Errorf("run dred helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred parse", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDParseInfo{}, err
	}
	reader.ExpectRemaining(8)
	ret := int(reader.I32())
	dredEnd := int(reader.I32())
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDParseInfo{}, err
	}
	return libopusDREDParseInfo{
		availableSamples: ret,
		dredEndSamples:   dredEnd,
	}, nil
}

func probeLibopusDREDDecode(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDDecodeInfo, error) {
	binPath, err := getLibopusDREDDecodeHelperPath()
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDDecodeInfo{}, fmt.Errorf("run dred decode helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred decode", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	info := libopusDREDDecodeInfo{
		availableSamples: int(reader.I32()),
		dredEndSamples:   int(reader.I32()),
		processStage:     int(reader.I32()),
		nbLatents:        int(reader.I32()),
		dredOffset:       int(reader.I32()),
	}

	for i := range info.state {
		info.state[i] = reader.Float32()
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		info.latents[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDDecodeInfo{}, err
	}
	return info, nil
}

type libopusDREDProcessInfo struct {
	availableSamples  int
	dredEndSamples    int
	processRet        int
	processStage      int
	nbLatents         int
	dredOffset        int
	secondProcessRet  int
	secondStage       int
	cloneProcessRet   int
	cloneStage        int
	secondStateHash   uint32
	secondLatentHash  uint32
	secondFeatureHash uint32
	cloneStateHash    uint32
	cloneLatentHash   uint32
	cloneFeatureHash  uint32
	state             [internaldred.StateDim]float32
	latents           []float32
	features          []float32
}

type libopusDREDRecoveryWindowInfo struct {
	availableSamples         int
	dredEndSamples           int
	processRet               int
	processStage             int
	nbLatents                int
	dredOffset               int
	featuresPerFrame         int
	neededFeatureFrames      int
	featureOffsetBase        int
	maxFeatureIndex          int
	recoverableFeatureFrames int
	missingPositiveFrames    int
	featureOffsets           []int
}

var (
	libopusDREDModelBlobHelper      libopustest.HelperCache
	libopusDREDProcessHelper        libopustest.HelperCache
	libopusDREDRecoveryWindowHelper libopustest.HelperCache
)

func getLibopusDREDModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDModelBlobHelper, "libopus_dred_model_blob.c", "gopus_libopus_dred_model_blob", true)
}

func getLibopusDREDProcessHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDProcessHelper, "libopus_dred_process_info.c", "gopus_libopus_dred_process", true)
}

func getLibopusDREDRecoveryWindowHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDRecoveryWindowHelper, "libopus_dred_recovery_window_info.c", "gopus_libopus_dred_recovery_window", true)
}

func probeLibopusDREDModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run dred model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDREDProcess(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDProcessInfo, error) {
	binPath, err := getLibopusDREDProcessHelperPath()
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDProcessInfo{}, fmt.Errorf("run dred process helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred process", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	info := libopusDREDProcessInfo{
		availableSamples:  int(reader.I32()),
		dredEndSamples:    int(reader.I32()),
		processRet:        int(reader.I32()),
		processStage:      int(reader.I32()),
		nbLatents:         int(reader.I32()),
		dredOffset:        int(reader.I32()),
		secondProcessRet:  int(reader.I32()),
		secondStage:       int(reader.I32()),
		cloneProcessRet:   int(reader.I32()),
		cloneStage:        int(reader.I32()),
		secondStateHash:   reader.U32(),
		secondLatentHash:  reader.U32(),
		secondFeatureHash: reader.U32(),
		cloneStateHash:    reader.U32(),
		cloneLatentHash:   reader.U32(),
		cloneFeatureHash:  reader.U32(),
	}

	for i := range info.state {
		info.state[i] = reader.Float32()
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		info.latents[i] = reader.Float32()
	}

	featureValues := info.nbLatents * 4 * internaldred.NumFeatures
	info.features = make([]float32, featureValues)
	for i := 0; i < featureValues; i++ {
		info.features[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDProcessInfo{}, err
	}
	return info, nil
}

func probeLibopusDREDRecoveryWindow(packet []byte, maxDREDSamples, sampleRate, frameSizeSamples, decodeOffsetSamples int, blend bool) (libopusDREDRecoveryWindowInfo, error) {
	binPath, err := getLibopusDREDRecoveryWindowHelperPath()
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	var blendFlag uint32
	if blend {
		blendFlag = 1
	}
	payload := libopustest.NewOraclePayload(
		libopusDREDParseInputMagic,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(int32(decodeOffsetSamples)),
		blendFlag,
		uint32(len(packet)),
	)
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("run dred recovery helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred recovery", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	info := libopusDREDRecoveryWindowInfo{
		availableSamples:         int(reader.I32()),
		dredEndSamples:           int(reader.I32()),
		processRet:               int(reader.I32()),
		processStage:             int(reader.I32()),
		nbLatents:                int(reader.I32()),
		dredOffset:               int(reader.I32()),
		featuresPerFrame:         int(reader.I32()),
		neededFeatureFrames:      int(reader.I32()),
		featureOffsetBase:        int(reader.I32()),
		maxFeatureIndex:          int(reader.I32()),
		recoverableFeatureFrames: int(reader.I32()),
		missingPositiveFrames:    int(reader.I32()),
	}

	info.featureOffsets = make([]int, info.neededFeatureFrames)
	for i := range info.featureOffsets {
		info.featureOffsets[i] = int(reader.I32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}
	return info, nil
}

func TestDREDControlModelBlobsMatchPinnedLibopusDigests(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name        string
		probe       func() ([]byte, error)
		validate    func(*dnnblob.Blob) error
		wantRecords int
		wantBytes   int
		wantSHA256  string
	}{
		{
			name:        "encoder",
			probe:       probeLibopusEncoderNeuralModelBlob,
			validate:    func(blob *dnnblob.Blob) error { return blob.ValidateEncoderControl() },
			wantRecords: 133,
			wantBytes:   317696,
			wantSHA256:  "164bfeab6f7d20932b006e9cdcaa1d26f2fd2f02d5e33cafee4fbb580be4c0b2",
		},
		{
			name:        "decoder",
			probe:       probeLibopusDecoderNeuralModelBlob,
			validate:    func(blob *dnnblob.Blob) error { return blob.ValidateDecoderControl(false) },
			wantRecords: 114,
			wantBytes:   1432768,
			wantSHA256:  "42d935199c9d86519ccd7be3bc8df0090553319d3e23b2e93cdfdf7a06c9ab95",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.probe()
			if err != nil {
				libopustest.HelperUnavailable(t, tc.name+" control model", err)
			}
			blob, err := dnnblob.Clone(raw)
			if err != nil {
				t.Fatalf("Clone(%s control model blob) error: %v", tc.name, err)
			}
			if err := tc.validate(blob); err != nil {
				t.Fatalf("Validate(%s control model blob) error: %v", tc.name, err)
			}
			if len(blob.Records) != tc.wantRecords {
				t.Fatalf("%s DRED control model records=%d want %d", tc.name, len(blob.Records), tc.wantRecords)
			}
			if len(raw) != tc.wantBytes {
				t.Fatalf("%s DRED control model bytes=%d want %d", tc.name, len(raw), tc.wantBytes)
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])
			if got != tc.wantSHA256 {
				t.Fatalf("%s DRED control model blob sha256=%s want %s", tc.name, got, tc.wantSHA256)
			}
		})
	}
}

func addDREDPayloadFuzzSeed(f *testing.F, extensions []packetExtensionData) {
	frames := [][]byte{{0x11, 0x22, 0x33}, {0x44, 0x55}}
	packet := make([]byte, 128)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err == nil {
		f.Add(append([]byte(nil), packet[:n]...))
	}
}

func FuzzFindDREDPayload_NoPanic(f *testing.F) {
	validPayload := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, validPayload...)},
	})
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0xaa}},
	})
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, validPayload...)},
	})
	f.Add([]byte{0x7c, 0xc1, 0x03, 0x02, 0xff})
	f.Add([]byte{0xff, 0xff, 0x00, 'D', internaldred.ExperimentalVersion})
	f.Add([]byte{GenerateTOC(31, false, 3), 0xc1, 0x02, 0x01})

	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > 4096 {
			packet = packet[:4096]
		}
		payload, frameOffset, ok, err := findDREDPayload(packet)
		if err != nil {
			return
		}
		if !ok {
			if payload != nil || frameOffset != 0 {
				t.Fatalf("findDREDPayload returned stale data with ok=false: payload=%x frameOffset=%d", payload, frameOffset)
			}
			return
		}
		if frameOffset < 0 {
			t.Fatalf("negative DRED frame offset: %d", frameOffset)
		}
		if len(payload) > len(packet) {
			t.Fatalf("DRED payload length=%d exceeds packet length=%d", len(payload), len(packet))
		}
	})
}

func TestFindDREDPayload(t *testing.T) {
	frames := [][]byte{
		{0x11, 0x22, 0x33},
		{0x44, 0x55, 0x66},
	}
	wantPayload := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x12, 0x34}
	extensions := []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok {
		t.Fatal("findDREDPayload ok=false want true")
	}
	if frameOffset != 8 {
		t.Fatalf("frameOffset=%d want 8", frameOffset)
	}
	if !bytes.Equal(payload, wantPayload) {
		t.Fatalf("payload=%x want %x", payload, wantPayload)
	}
}

func TestFindDREDPayloadSkipsWrongExperimentalHeader(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	extensions := []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0xaa}},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if ok || payload != nil || frameOffset != 0 {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (nil,0,false)", payload, frameOffset, ok)
	}
}

func TestFindDREDPayloadSkipsEarlierInvalidDREDExtension(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	wantPayload := []byte{0xaa, 0xbb, 0xcc}
	extensions := []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok || frameOffset != 0 || !bytes.Equal(payload, wantPayload) {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (%x,0,true)", payload, frameOffset, ok, wantPayload)
	}
}

func TestFindDREDPayloadKeepsShortExperimentalPayload(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	wantPayload := []byte{0xaa, 0xbb}
	extensions := []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok || frameOffset != 0 || !bytes.Equal(payload, wantPayload) {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (%x,0,true)", payload, frameOffset, ok, wantPayload)
	}
}

func TestStandaloneDREDProcessMatchesLibopusOnRealPacket(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	want, err := probeLibopusDREDProcess(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred process", err)
	}
	if want.availableSamples < 0 {
		t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
	}
	if want.processRet != 0 {
		t.Fatalf("libopus dred process returned error %d", want.processRet)
	}
	if want.processStage != 2 {
		t.Fatalf("libopus processStage=%d want 2", want.processStage)
	}

	dec := NewDREDDecoder()
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		for _, name := range dnnblob.RequiredDREDDecoderRecordNames() {
			if !blob.HasRecord(name) {
				t.Fatalf("ValidateDREDDecoderControl(real model) error: %v (missing record %q)", err, name)
			}
		}
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	if _, err := rdovae.LoadDecoder(blob); err != nil {
		t.Fatalf("rdovae.LoadDecoder(real model) error: %v", err)
	}
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if available != want.availableSamples {
		t.Fatalf("available=%d want %d", available, want.availableSamples)
	}
	if dredEnd != want.dredEndSamples {
		t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
	}
	if got := dred.LatentCount(); got != want.nbLatents {
		t.Fatalf("LatentCount()=%d want %d", got, want.nbLatents)
	}
	state := make([]float32, len(want.state))
	if n := dred.FillState(state); n != len(want.state) {
		t.Fatalf("FillState count=%d want %d", n, len(want.state))
	}
	assertFloat32BitsEqual(t, state, want.state[:], "state")
	latents := make([]float32, want.nbLatents*internaldred.LatentStride)
	if n := dred.FillLatents(latents); n != len(latents) {
		t.Fatalf("FillLatents count=%d want %d", n, len(latents))
	}
	assertFloat32BitsEqual(t, latents, want.latents, "latents")

	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if got := dred.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := dred.FeatureCount(); got != len(want.features) {
		t.Fatalf("FeatureCount()=%d want %d", got, len(want.features))
	}
	features := make([]float32, dred.FeatureCount())
	if n := dred.FillFeatures(features); n != len(features) {
		t.Fatalf("FillFeatures count=%d want %d", n, len(features))
	}
	assertFloat32BitsEqual(t, features, want.features, "features")
}

func TestStandaloneDREDProcessMatchesLibopusOnQualityPacket(t *testing.T) {
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	_, packets := encodeDREDQualityPackets(t, encoderBlob)

	expected := 0
	haveExpected := false
	for frame, packet := range packets {
		if !dredQualityPacketDelivered(frame) {
			continue
		}
		if haveExpected {
			missing := frame - expected
			if missing > 0 {
				want, err := probeLibopusDREDProcess(packet, missing*dredQualityFrameSize, dredQualitySampleRate)
				if err != nil {
					libopustest.HelperUnavailable(t, "quality dred process", err)
				}
				if want.availableSamples <= 0 || want.processRet != 0 || want.processStage != 2 {
					t.Fatalf("libopus quality DRED process=(available=%d ret=%d stage=%d)", want.availableSamples, want.processRet, want.processStage)
				}

				dec := NewDREDDecoder()
				if err := dec.SetDNNBlob(modelBlob); err != nil {
					t.Fatalf("SetDNNBlob(real model): %v", err)
				}
				dred := NewDRED()
				available, dredEnd, err := dec.Parse(dred, packet, missing*dredQualityFrameSize, dredQualitySampleRate, true)
				if err != nil {
					t.Fatalf("Parse quality packet frame=%d: %v", frame, err)
				}
				if available != want.availableSamples || dredEnd != want.dredEndSamples {
					t.Fatalf("quality availability=(%d,%d) want (%d,%d)", available, dredEnd, want.availableSamples, want.dredEndSamples)
				}
				if err := dec.Process(dred, dred); err != nil {
					t.Fatalf("Process quality packet frame=%d: %v", frame, err)
				}
				features := make([]float32, dred.FeatureCount())
				if n := dred.FillFeatures(features); n != len(features) {
					t.Fatalf("FillFeatures count=%d want %d", n, len(features))
				}
				assertFloat32BitsEqual(t, features, want.features, "quality features")
				return
			}
		}
		expected = frame + 1
		haveExpected = true
	}
	t.Fatal("no delivered quality packet with preceding loss")
}

func TestStandaloneDREDProcessLifecycleMatchesLibopusOnRealPacket(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	want, err := probeLibopusDREDProcess(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred process", err)
	}
	if want.processRet != 0 || want.processStage != 2 {
		t.Fatalf("libopus process=(ret=%d, stage=%d) want (0,2)", want.processRet, want.processStage)
	}
	if want.secondProcessRet != 0 || want.secondStage != 2 {
		t.Fatalf("libopus second process=(ret=%d, stage=%d) want (0,2)", want.secondProcessRet, want.secondStage)
	}
	if want.cloneProcessRet != 0 || want.cloneStage != 2 {
		t.Fatalf("libopus clone process=(ret=%d, stage=%d) want (0,2)", want.cloneProcessRet, want.cloneStage)
	}

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	if _, _, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("first Process error: %v", err)
	}
	firstState := append([]float32(nil), dred.decoded.State[:]...)
	firstLatents := append([]float32(nil), dred.decoded.Latents[:dred.decoded.NbLatents*internaldred.LatentStride]...)
	firstFeatures := append([]float32(nil), dred.decoded.Features[:dred.decoded.NbLatents*4*internaldred.NumFeatures]...)
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("second Process error: %v", err)
	}
	if got := dred.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := dred.decoded.NbLatents; got != want.nbLatents {
		t.Fatalf("second NbLatents=%d want %d", got, want.nbLatents)
	}
	secondLatents := dred.decoded.Latents[:dred.decoded.NbLatents*internaldred.LatentStride]
	secondFeatures := dred.decoded.Features[:dred.decoded.NbLatents*4*internaldred.NumFeatures]
	assertLibopusDREDFloat32Hash(t, "second state", dred.decoded.State[:], want.secondStateHash)
	assertLibopusDREDFloat32Hash(t, "second latents", secondLatents, want.secondLatentHash)
	assertLibopusDREDFloat32Hash(t, "second features", secondFeatures, want.secondFeatureHash)
	assertFloat32BitsEqual(t, dred.decoded.State[:], firstState, "second state")
	assertFloat32BitsEqual(t, secondLatents, firstLatents, "second latents")
	assertFloat32BitsEqual(t, secondFeatures, firstFeatures, "second features")

	clone := NewDRED()
	if err := dec.Process(dred, clone); err != nil {
		t.Fatalf("clone Process error: %v", err)
	}
	if got := clone.ProcessStage(); got != DREDProcessStageProcessed {
		t.Fatalf("clone ProcessStage()=%d want %d", got, DREDProcessStageProcessed)
	}
	if got := clone.decoded.NbLatents; got != want.nbLatents {
		t.Fatalf("clone NbLatents=%d want %d", got, want.nbLatents)
	}
	cloneLatents := clone.decoded.Latents[:clone.decoded.NbLatents*internaldred.LatentStride]
	cloneFeatures := clone.decoded.Features[:clone.decoded.NbLatents*4*internaldred.NumFeatures]
	assertLibopusDREDFloat32Hash(t, "clone state", clone.decoded.State[:], want.cloneStateHash)
	assertLibopusDREDFloat32Hash(t, "clone latents", cloneLatents, want.cloneLatentHash)
	assertLibopusDREDFloat32Hash(t, "clone features", cloneFeatures, want.cloneFeatureHash)
	assertFloat32BitsEqual(t, clone.decoded.State[:], firstState, "clone state")
	assertFloat32BitsEqual(t, cloneLatents, firstLatents, "clone latents")
	assertFloat32BitsEqual(t, cloneFeatures, firstFeatures, "clone features")
}

func assertLibopusDREDFloat32Hash(t *testing.T, label string, data []float32, want uint32) {
	t.Helper()
	if got := libopusDREDFloat32Hash(data); got != want {
		t.Fatalf("%s hash=0x%08x want 0x%08x", label, got, want)
	}
}

func libopusDREDFloat32Hash(data []float32) uint32 {
	h := uint32(2166136261)
	for _, sample := range data {
		h ^= math.Float32bits(sample)
		h *= 16777619
	}
	return h
}

func TestStandaloneDREDRecoveryQueueMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	type queueCase struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}
	scenarios := []struct {
		name           string
		decoderRate    int
		maxDREDSamples int
		sampleRate     int
		tests          []queueCase
	}{
		{
			name:           "48k",
			decoderRate:    48000,
			maxDREDSamples: packetInfo.maxDREDSamples,
			sampleRate:     packetInfo.sampleRate,
			tests: []queueCase{
				{name: "blend_off_negative_full_frame", decodeOffsetSamples: -960, frameSizeSamples: 960, blend: false},
				{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
				{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 480, blend: false},
				{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 2880, blend: false},
				{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: true},
				{name: "blend_off_late_recovery", decodeOffsetSamples: 3840, frameSizeSamples: 960, blend: false},
				{name: "blend_off_beyond_payload", decodeOffsetSamples: 9600, frameSizeSamples: 960, blend: false},
				{name: "blend_on_half_frame", decodeOffsetSamples: 960, frameSizeSamples: 480, blend: true},
				{name: "blend_on_late_10ms", decodeOffsetSamples: 1920, frameSizeSamples: 480, blend: true},
			},
		},
		{
			name:        "16k",
			decoderRate: 16000,
			tests: []queueCase{
				{name: "blend_off_negative_full_frame", decodeOffsetSamples: -320, frameSizeSamples: 320, blend: false},
				{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: false},
				{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 160, blend: false},
				{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
				{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: true},
				{name: "blend_off_late_recovery", decodeOffsetSamples: 1280, frameSizeSamples: 320, blend: false},
				{name: "blend_on_half_frame", decodeOffsetSamples: 320, frameSizeSamples: 160, blend: true},
			},
		},
	}
	for i := range scenarios {
		if scenarios[i].maxDREDSamples == 0 || scenarios[i].sampleRate == 0 {
			scenarios[i].maxDREDSamples, scenarios[i].sampleRate = libopusDREDRequestForDecoder(packetInfo, scenarios[i].decoderRate)
		}
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			dec := NewDREDDecoder()
			if err := dec.SetDNNBlob(modelBlob); err != nil {
				t.Fatalf("SetDNNBlob(real model) error: %v", err)
			}

			dred := NewDRED()
			available, dredEnd, err := dec.Parse(dred, packetInfo.packet, scenario.maxDREDSamples, scenario.sampleRate, true)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if err := dec.Process(dred, dred); err != nil {
				t.Fatalf("Process error: %v", err)
			}
			result := dred.Result(scenario.maxDREDSamples, scenario.sampleRate)

			for _, tc := range scenario.tests {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, scenario.maxDREDSamples, scenario.sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
					if err != nil {
						libopustest.HelperUnavailable(t, "dred recovery", err)
					}
					if want.availableSamples != available {
						t.Fatalf("available=%d want %d", available, want.availableSamples)
					}
					if want.dredEndSamples != dredEnd {
						t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
					}

					var plc lpcnetplc.State
					allocs := testing.AllocsPerRun(1000, func() {
						if tc.blend {
							plc.MarkConcealed()
						} else {
							plc.MarkUpdated()
						}
						internaldred.QueueProcessedFeatures(&plc, result, &dred.decoded, tc.decodeOffsetSamples, tc.frameSizeSamples)
					})
					if allocs != 0 {
						t.Fatalf("AllocsPerRun=%v want 0", allocs)
					}

					if tc.blend {
						plc.MarkConcealed()
					} else {
						plc.MarkUpdated()
					}
					got := internaldred.QueueProcessedFeatures(&plc, result, &dred.decoded, tc.decodeOffsetSamples, tc.frameSizeSamples)
					if got.FeaturesPerFrame != want.featuresPerFrame ||
						got.NeededFeatureFrames != want.neededFeatureFrames ||
						got.FeatureOffsetBase != want.featureOffsetBase ||
						got.MaxFeatureIndex != want.maxFeatureIndex ||
						got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
						got.MissingPositiveFrames != want.missingPositiveFrames {
						t.Fatalf("QueueProcessedFeatures window=%+v want %+v", got, want)
					}
					if plc.FECFillPos() != want.recoverableFeatureFrames {
						t.Fatalf("FECFillPos()=%d want %d", plc.FECFillPos(), want.recoverableFeatureFrames)
					}
					if plc.FECSkip() != want.missingPositiveFrames {
						t.Fatalf("FECSkip()=%d want %d", plc.FECSkip(), want.missingPositiveFrames)
					}

					queued := 0
					var gotFeatures [lpcnetplc.NumFeatures]float32
					for _, featureOffset := range want.featureOffsets {
						if featureOffset < 0 || featureOffset > want.maxFeatureIndex {
							continue
						}
						if n := plc.FillQueuedFeatures(queued, gotFeatures[:]); n != lpcnetplc.NumFeatures {
							t.Fatalf("FillQueuedFeatures(%d) count=%d want %d", queued, n, lpcnetplc.NumFeatures)
						}
						start := featureOffset * internaldred.NumFeatures
						assertFloat32BitsEqual(t, gotFeatures[:], dred.decoded.Features[start:start+internaldred.NumFeatures], "queued features")
						queued++
					}
					if queued != plc.FECFillPos() {
						t.Fatalf("queued=%d want fill=%d", queued, plc.FECFillPos())
					}
				})
			}
		})
	}
}

func TestStandaloneDREDRecoveryWindowMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}

	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}{
		{name: "blend_off_negative_offset", decodeOffsetSamples: -480, frameSizeSamples: 960, blend: false},
		{name: "blend_off_negative_full_frame", decodeOffsetSamples: -960, frameSizeSamples: 960, blend: false},
		{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
		{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 480, blend: false},
		{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 2880, blend: false},
		{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: true},
		{name: "blend_off_late_recovery", decodeOffsetSamples: 3840, frameSizeSamples: 960, blend: false},
		{name: "blend_off_beyond_payload", decodeOffsetSamples: 9600, frameSizeSamples: 960, blend: false},
		{name: "blend_on_half_frame", decodeOffsetSamples: 960, frameSizeSamples: 480, blend: true},
		{name: "blend_on_late_10ms", decodeOffsetSamples: 1920, frameSizeSamples: 480, blend: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred recovery", err)
			}
			if want.availableSamples != available {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if want.dredEndSamples != dredEnd {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}
			if want.processRet != 0 {
				t.Fatalf("libopus processRet=%d want 0", want.processRet)
			}
			if want.processStage != 2 {
				t.Fatalf("libopus processStage=%d want 2", want.processStage)
			}

			initFrames := 2
			if tc.blend {
				initFrames = 0
			}
			got := dred.FeatureWindow(packetInfo.maxDREDSamples, packetInfo.sampleRate, tc.decodeOffsetSamples, tc.frameSizeSamples, initFrames)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("FeatureWindow=%+v want %+v", got, want)
			}

			offsets := make([]int32, got.NeededFeatureFrames)
			if n := got.FillFeatureOffsets(offsets); n != len(want.featureOffsets) {
				t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(want.featureOffsets))
			}
			for i, wantOffset := range want.featureOffsets {
				if offsets[i] != int32(wantOffset) {
					t.Fatalf("featureOffsets[%d]=%d want %d", i, offsets[i], wantOffset)
				}
			}
		})
	}
}

func TestStandaloneDREDRecoveryWindow16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	maxDREDSamples, sampleRate := libopusDREDRequestForDecoder(packetInfo, 16000)

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}

	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, maxDREDSamples, sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}{
		{name: "blend_off_negative_offset", decodeOffsetSamples: -160, frameSizeSamples: 320, blend: false},
		{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: false},
		{name: "blend_off_current_10ms", decodeOffsetSamples: 0, frameSizeSamples: 160, blend: false},
		{name: "blend_off_current_60ms", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
		{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 320, blend: true},
		{name: "blend_off_late_recovery", decodeOffsetSamples: 1280, frameSizeSamples: 320, blend: false},
		{name: "blend_on_half_frame", decodeOffsetSamples: 320, frameSizeSamples: 160, blend: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, maxDREDSamples, sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
			if err != nil {
				libopustest.HelperUnavailable(t, "dred recovery", err)
			}
			if want.availableSamples != available {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if want.dredEndSamples != dredEnd {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}
			if want.processRet != 0 {
				t.Fatalf("libopus processRet=%d want 0", want.processRet)
			}
			if want.processStage != 2 {
				t.Fatalf("libopus processStage=%d want 2", want.processStage)
			}

			initFrames := 2
			if tc.blend {
				initFrames = 0
			}
			got := dred.FeatureWindow(maxDREDSamples, sampleRate, tc.decodeOffsetSamples, tc.frameSizeSamples, initFrames)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("FeatureWindow=%+v want %+v", got, want)
			}

			offsets := make([]int32, got.NeededFeatureFrames)
			if n := got.FillFeatureOffsets(offsets); n != len(want.featureOffsets) {
				t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(want.featureOffsets))
			}
			for i, wantOffset := range want.featureOffsets {
				if offsets[i] != int32(wantOffset) {
					t.Fatalf("featureOffsets[%d]=%d want %d", i, offsets[i], wantOffset)
				}
			}
		})
	}
}

func TestEncoderDREDDurationDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	// DRED durations in 10 ms units. libopus clamps to [0,104]; the high end
	// exercises the maximum 104-unit redundancy window. 0 means "no DRED" and is
	// excluded — this sweep targets the carried payload across the active range.
	//
	// The full active range includes short CBR durations. If no DRED fits in
	// the 640-frame window, both encoders must agree on that absence.
	durations := []int{8, 16, 32, 48, 64, 80, 96, 104}

	modes := []struct {
		name      string
		mode      encpkg.Mode
		public    Mode
		bandwidth Bandwidth
		channels  int
	}{
		{"silk_wb_mono", encpkg.ModeSILK, ModeSILK, BandwidthWideband, 1},
		{"silk_wb_stereo", encpkg.ModeSILK, ModeSILK, BandwidthWideband, 2},
		{"hybrid_fb_mono", encpkg.ModeHybrid, ModeHybrid, BandwidthFullband, 1},
		{"celt_fb_mono", encpkg.ModeCELT, ModeCELT, BandwidthFullband, 1},
		{"celt_fb_stereo", encpkg.ModeCELT, ModeCELT, BandwidthFullband, 2},
	}

	rcModes := []struct {
		name string
		cbr  bool
	}{
		{"vbr", false},
		{"cbr", true},
	}

	const frameSize = 960 // 20 ms at 48 kHz
	var (
		tested        int
		payloadOK     int
		indexMismatch int
		offsetMismat  int
		payloadFails  int
		absentOK      int
	)

	for _, m := range modes {
		for _, rc := range rcModes {
			for _, dur := range durations {
				name := fmt.Sprintf("%s/%s/dur%d", m.name, rc.name, dur)
				t.Run(name, func(t *testing.T) {
					tested++
					cfg := libopusDREDPacketConfig{
						FrameSize:    frameSize,
						ForceMode:    m.public,
						Bandwidth:    m.bandwidth,
						Channels:     m.channels,
						CBR:          rc.cbr,
						DREDDuration: dur,
					}
					if rc.cbr {
						cfg.Bitrate = 64000
						if m.public == ModeSILK {
							cfg.Bitrate = 32000
						}
					}
					packetInfo, err := emitLibopusDREDPacketWithConfigRecord(cfg, true)
					if err != nil {
						t.Fatalf("DRED duration packet oracle: %v", err)
					}
					bitrate := 0
					if rc.cbr {
						bitrate = cfg.Bitrate
					}
					settings := encoderDREDPacketSettings{
						mode: m.mode, bandwidth: m.bandwidth, frameSize: frameSize,
						channels: m.channels, bitrate: bitrate, cbr: rc.cbr, dredDuration: dur,
					}
					if len(packetInfo.packet) == 0 {
						gotPacket, gotPayload, _, gotFrameIndex := scanDREDEmissionWithSettings(t, settings, false)
						if gotPacket != nil || gotPayload != nil || gotFrameIndex != packetInfo.frameIndex {
							t.Fatalf("DRED absence mismatch: Go packet=%d payload=%d frame=%d C frame=%d",
								len(gotPacket), len(gotPayload), gotFrameIndex, packetInfo.frameIndex)
						}
						absentOK++
						return
					}
					wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
					if err != nil {
						t.Fatalf("findDREDPayload(libopus) error: %v", err)
					}
					if !ok {
						t.Fatalf("libopus %s packet missing DRED payload at duration %d", m.name, dur)
					}

					gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithSettings(t, settings)
					if ParseTOC(gotPacket[0]).Mode != m.public {
						t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, m.public)
					}
					if gotFrameIndex != packetInfo.frameIndex {
						indexMismatch++
						t.Fatalf("DRED emission frame index=%d want %d (dur=%d)", gotFrameIndex, packetInfo.frameIndex, dur)
					}
					if gotOffset != wantOffset {
						offsetMismat++
						t.Fatalf("DRED frameOffset=%d want %d (dur=%d)", gotOffset, wantOffset, dur)
					}
					if !bytes.Equal(gotPayload, wantPayload) {
						payloadFails++
						t.Fatalf("DRED payload mismatch at duration %d\n got=%x\nwant=%x", dur, gotPayload, wantPayload)
					}
					payloadOK++
				})
			}
		}
	}
	t.Logf("DRED duration differential sweep: %d specs; payload-exact=%d absent-exact=%d index-mismatch=%d offset-mismatch=%d payload-fails=%d",
		tested, payloadOK, absentOK, indexMismatch, offsetMismat, payloadFails)
}

const (
	dredEncoderSequenceFrameSize = 960
	dredEncoderSequenceFrames    = 220
)

var libopusDREDFullEncoderSequenceHelper libopustest.HelperCache

type libopusDREDFullEncoderFrame struct {
	index      int
	finalRange uint32
	packet     []byte
}

func TestDREDLowDelayFullSequenceEncoderMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm := dredEncoderSequencePCM(t, channels)
			want := runLibopusDREDFullEncoderSequence(t, channels, pcm)
			got, enc := encodeDREDFullSequenceGo(t, channels, pcm)
			if len(got) != len(want) {
				t.Fatalf("frame records=%d want %d", len(got), len(want))
			}

			carriedDRED := 0
			for frame := range want {
				if got[frame].index != frame || want[frame].index != frame {
					t.Fatalf("record %d indices Go=%d C=%d", frame, got[frame].index, want[frame].index)
				}
				if got[frame].finalRange != want[frame].finalRange {
					limit := min(len(got[frame].packet), len(want[frame].packet))
					diff := 0
					for diff < limit && got[frame].packet[diff] == want[frame].packet[diff] {
						diff++
					}
					t.Fatalf("frame %d final range Go=%08x C=%08x; packet lens Go=%d C=%d TOCs Go=%02x C=%02x first diff=%d prefixes Go=%x C=%x",
						frame, got[frame].finalRange, want[frame].finalRange, len(got[frame].packet), len(want[frame].packet),
						got[frame].packet[0], want[frame].packet[0], diff,
						got[frame].packet[:min(len(got[frame].packet), 32)], want[frame].packet[:min(len(want[frame].packet), 32)])
				}
				if !bytes.Equal(got[frame].packet, want[frame].packet) {
					limit := min(len(got[frame].packet), len(want[frame].packet))
					diff := 0
					for diff < limit && got[frame].packet[diff] == want[frame].packet[diff] {
						diff++
					}
					gotDRED, gotOffset, gotOK, gotErr := findDREDPayload(got[frame].packet)
					wantDRED, wantOffset, wantOK, wantErr := findDREDPayload(want[frame].packet)
					t.Fatalf("frame %d packet mismatch at byte %d: Go len=%d C len=%d Go=%x C=%x; DRED Go=(%t,%d,%x,%v) C=(%t,%d,%x,%v)",
						frame, diff, len(got[frame].packet), len(want[frame].packet),
						got[frame].packet[:min(len(got[frame].packet), 32)],
						want[frame].packet[:min(len(want[frame].packet), 32)],
						gotOK, gotOffset, gotDRED, gotErr, wantOK, wantOffset, wantDRED, wantErr)
				}
				_, _, ok, err := findDREDPayload(got[frame].packet)
				if err != nil {
					t.Fatalf("frame %d findDREDPayload: %v", frame, err)
				}
				if ok {
					carriedDRED++
				}
			}
			if carriedDRED == 0 {
				t.Fatal("full low-delay sequence carries no DRED payloads")
			}
			t.Logf("matched %d/%d packets and final ranges; %d carry DRED", len(got), len(want), carriedDRED)
			assertDREDFullSequenceEncodeZeroAlloc(t, enc, channels, pcm)
		})
	}
}

func dredEncoderSequencePCM(t *testing.T, channels int) []float32 {
	t.Helper()
	pcm := make([]float32, dredEncoderSequenceFrames*dredEncoderSequenceFrameSize*channels)
	mono := make([]float32, dredEncoderSequenceFrameSize)
	for frame := 0; frame < dredEncoderSequenceFrames; frame++ {
		fillDREDQualitySpeechFrame(mono, frame)
		for sample, value := range mono {
			for channel := 0; channel < channels; channel++ {
				pcm[(frame*dredEncoderSequenceFrameSize+sample)*channels+channel] = value
			}
		}
	}
	return pcm
}

func encodeDREDFullSequenceGo(t *testing.T, channels int, pcm []float32) ([]libopusDREDFullEncoderFrame, *Encoder) {
	t.Helper()
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: ApplicationLowDelay,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	controls := []struct {
		name string
		set  func() error
	}{
		{"bitrate", func() error { return enc.SetBitrate(48000) }},
		{"complexity", func() error { return enc.SetComplexity(10) }},
		{"bandwidth", func() error { return enc.SetBandwidth(BandwidthFullband) }},
		{"max bandwidth", func() error { return enc.SetMaxBandwidth(BandwidthFullband) }},
		{"signal", func() error { return enc.SetSignal(SignalVoice) }},
		{"packet loss", func() error { return enc.SetPacketLoss(60) }},
		{"force channels", func() error { return enc.SetForceChannels(-1) }},
		{"LSB depth", func() error { enc.SetLSBDepth(24); return nil }},
		{"VBR", func() error { enc.SetVBR(true); return nil }},
		{"VBR constraint", func() error { enc.SetVBRConstraint(true); return nil }},
		{"DRED duration", func() error { return enc.SetDREDDuration(80) }},
	}
	for _, control := range controls {
		if err := control.set(); err != nil {
			t.Fatalf("set %s: %v", control.name, err)
		}
	}

	packet := make([]byte, maxPacketBytesPerStream)
	frames := make([]libopusDREDFullEncoderFrame, dredEncoderSequenceFrames)
	for frame := range frames {
		start := frame * dredEncoderSequenceFrameSize * channels
		end := start + dredEncoderSequenceFrameSize*channels
		n, err := enc.Encode(pcm[start:end], packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d): %v", frame, err)
		}
		if n <= 0 {
			t.Fatalf("Encode(frame=%d) returned %d bytes", frame, n)
		}
		frames[frame] = libopusDREDFullEncoderFrame{
			index:      frame,
			finalRange: enc.FinalRange(),
			packet:     append([]byte(nil), packet[:n]...),
		}
	}
	return frames, enc
}

func assertDREDFullSequenceEncodeZeroAlloc(t *testing.T, enc *Encoder, channels int, pcm []float32) {
	t.Helper()
	if enc.enc == nil || !enc.enc.DREDModelLoaded() || !enc.enc.DREDReady() {
		t.Fatal("active DRED encoder is not model-loaded and ready")
	}
	packet := make([]byte, maxPacketBytesPerStream)
	encodeSequence := func() {
		for frame := 0; frame < dredEncoderSequenceFrames; frame++ {
			start := frame * dredEncoderSequenceFrameSize * channels
			end := start + dredEncoderSequenceFrameSize*channels
			n, err := enc.Encode(pcm[start:end], packet)
			if err != nil {
				t.Fatalf("active DRED Encode(frame=%d): %v", frame, err)
			}
			if n <= 0 {
				t.Fatalf("active DRED Encode(frame=%d) returned %d bytes", frame, n)
			}
		}
	}
	encodeSequence()
	allocs := testing.AllocsPerRun(3, encodeSequence)
	if allocs != 0 {
		t.Fatalf("active public DRED Encode full sequence allocs/op=%.2f want 0", allocs)
	}
	t.Logf("active public DRED Encode: %d warmed frames/cycle, %.0f allocs/op", dredEncoderSequenceFrames, allocs)
}

func runLibopusDREDFullEncoderSequence(t *testing.T, channels int, pcm []float32) []libopusDREDFullEncoderFrame {
	t.Helper()
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatalf("resolve selected libopus reference: %v", err)
	}
	binPath, err := cachedLibopusDREDHelperPath(
		&libopusDREDFullEncoderSequenceHelper,
		"libopus_dred_encoder_sequence.c",
		"gopus_libopus_dred_encoder_sequence",
		true,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED full encoder sequence", err)
	}
	input := make([]byte, len(pcm)*4)
	for sample, value := range pcm {
		binary.LittleEndian.PutUint32(input[sample*4:], math.Float32bits(value))
	}
	pcmHash := sha256.Sum256(input)
	t.Logf("selected libopus=%s reference=%s paired neural models come from selected archive; PCM SHA-256=%s; packet capacity=%d",
		variant, libopustest.RefPath(), hex.EncodeToString(pcmHash[:]), maxPacketBytesPerStream)
	output, err := libopustest.RunHelperArgs(
		binPath,
		input,
		strconv.Itoa(channels),
		strconv.Itoa(dredEncoderSequenceFrames),
		strconv.Itoa(maxPacketBytesPerStream),
	)
	if err != nil {
		t.Fatalf("run libopus DRED full encoder sequence: %v", err)
	}
	return parseLibopusDREDFullEncoderSequence(t, output, channels)
}

func parseLibopusDREDFullEncoderSequence(t *testing.T, data []byte, channels int) []libopusDREDFullEncoderFrame {
	t.Helper()
	r := bytes.NewReader(data)
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil || string(header[:]) != "GDES" {
		t.Fatalf("libopus DRED encoder sequence magic=%q err=%v", header, err)
	}
	readU32 := func() uint32 {
		var value uint32
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			t.Fatalf("read libopus sequence header: %v", err)
		}
		return value
	}
	if got := readU32(); got != 1 {
		t.Fatalf("libopus sequence version=%d want 1", got)
	}
	if got := readU32(); got != 48000 {
		t.Fatalf("libopus sample rate=%d want 48000", got)
	}
	if got := readU32(); got != uint32(channels) {
		t.Fatalf("libopus channels=%d want %d", got, channels)
	}
	if got := readU32(); got != dredEncoderSequenceFrameSize {
		t.Fatalf("libopus frame size=%d want %d", got, dredEncoderSequenceFrameSize)
	}
	if got := readU32(); got != dredEncoderSequenceFrames {
		t.Fatalf("libopus frame count=%d want %d", got, dredEncoderSequenceFrames)
	}

	frames := make([]libopusDREDFullEncoderFrame, dredEncoderSequenceFrames)
	for frame := range frames {
		index := int(readU32())
		packetLen := int(readU32())
		finalRange := readU32()
		if packetLen <= 0 || packetLen > maxPacketBytesPerStream {
			t.Fatalf("frame %d libopus packet length=%d", frame, packetLen)
		}
		packet := make([]byte, packetLen)
		if _, err := io.ReadFull(r, packet); err != nil {
			t.Fatalf("read libopus packet frame %d: %v", frame, err)
		}
		frames[frame] = libopusDREDFullEncoderFrame{index: index, finalRange: finalRange, packet: packet}
	}
	if r.Len() != 0 {
		t.Fatalf("libopus sequence has %d trailing output bytes", r.Len())
	}
	return frames
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo CELT packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeCELT, BandwidthFullband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeCELT || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want celt stereo", toc)
	}

	// DRED payload offset and bytes are independent of the CELT primary-frame
	// length, so we still gate on them strictly.
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}

	delta := len(gotPacket) - len(packetInfo.packet)
	if delta != 0 {
		t.Fatalf("CELT FB stereo packet length=%d want %d (delta=%d bytes)",
			len(gotPacket), len(packetInfo.packet), delta)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus hybrid packet missing DRED payload")
	}
	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func assertDREDPacketExtensionFramingMatchesLibopus(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	_, gotFrames, gotPadding, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parse got DRED packet frames: %v", err)
	}
	_, wantFrames, wantPadding, _, err := parsePacketFramesAndPadding(wantPacket)
	if err != nil {
		t.Fatalf("parse libopus DRED packet frames: %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("primary frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	if !bytes.Equal(gotPadding, wantPadding) {
		t.Fatalf("extension padding mismatch\n got=%x\nwant=%x", gotPadding, wantPadding)
	}
}

func assertDREDPacketPrimaryFrameSizesMatchLibopus(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	_, gotFrames, gotPadding, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parse got DRED packet frames: %v", err)
	}
	_, wantFrames, wantPadding, _, err := parsePacketFramesAndPadding(wantPacket)
	if err != nil {
		t.Fatalf("parse libopus DRED packet frames: %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("primary frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if len(gotFrames[i]) != len(wantFrames[i]) {
			t.Fatalf("primary frame %d length=%d want %d", i, len(gotFrames[i]), len(wantFrames[i]))
		}
	}
	if !bytes.Equal(gotPadding, wantPadding) {
		t.Fatalf("extension padding mismatch\n got=%x\nwant=%x", gotPadding, wantPadding)
	}
}

var libopusDREDEncoderModelBlobHelper libopustest.HelperCache

func getLibopusDREDEncoderModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDEncoderModelBlobHelper, "libopus_dred_encoder_model_blob.c", "gopus_libopus_dred_encoder_model_blob", true)
}

func probeLibopusDREDEncoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDEncoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusPitchDNNModelBlob() ([]byte, error) {
	binPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusEncoderNeuralModelBlob() ([]byte, error) {
	pitchBlob, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		return nil, err
	}
	dredBlob, err := probeLibopusDREDEncoderModelBlob()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob, nil
}

func requireLibopusEncoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusEncoderNeuralModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "encoder neural model", err)
	}
	return blob
}

// assertCarriedDREDPayloadParity compares every carried DRED payload byte with
// the instruction-paired libopus DRED reference, then exercises the public
// decoder on that same carrier packet.
func assertCarriedDREDPayloadParity(t *testing.T, gotPacket []byte, gotPayload, wantPayload []byte, gotOffset int) {
	t.Helper()
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertCarriedDREDPayloadStructuralParity(t, gotPacket, gotPayload, wantPayload, gotOffset)
}

// assertCarriedDREDPayloadStructuralParity validates the exact payload's chunk
// structure and decodes it through the standalone and public DRED paths.
func assertCarriedDREDPayloadStructuralParity(t *testing.T, gotPacket []byte, gotPayload, wantPayload []byte, gotOffset int) {
	t.Helper()

	gotParsed, err := internaldred.ParsePayload(gotPayload, gotOffset)
	if err != nil {
		t.Fatalf("gopus DRED payload does not parse: %v\n got=%x", err, gotPayload)
	}
	wantParsed, err := internaldred.ParsePayload(wantPayload, gotOffset)
	if err != nil {
		t.Fatalf("libopus DRED payload does not parse: %v\nwant=%x", err, wantPayload)
	}
	if gotParsed.PayloadLatents != wantParsed.PayloadLatents {
		t.Fatalf("DRED chunk/latent count=%d want %d (got=%x want=%x)",
			gotParsed.PayloadLatents, wantParsed.PayloadLatents, gotPayload, wantPayload)
	}
	if gotParsed.PayloadLatents <= 0 {
		t.Fatalf("DRED payload carries no latents: %x", gotPayload)
	}
	if gotParsed.Header.QMax < gotParsed.Header.Q0 {
		t.Fatalf("DRED header QMax=%d < Q0=%d", gotParsed.Header.QMax, gotParsed.Header.Q0)
	}

	assertCarriedDREDPayloadRoundTrips(t, gotPacket)
}

// assertCarriedDREDPayloadRoundTrips drives the standalone DRED decoder + the
// public Decoder.DecodeDRED path over the carrier packet, asserting the emitted
// DRED extension parses, processes, and reconstructs the expected sample count.
func assertCarriedDREDPayloadRoundTrips(t *testing.T, gotPacket []byte) {
	t.Helper()

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}

	toc := ParseTOC(gotPacket[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}
	const decoderSampleRate = 48000

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	standalone := NewDREDDecoder()
	if err := standalone.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("standalone SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	maxDRED, parseRate := libopusDREDRequestForDecoder(libopusDREDPacket{
		sampleRate:     decoderSampleRate,
		maxDREDSamples: decoderSampleRate, // request up to 1 s; the parse clamps to the carried coverage
	}, decoderSampleRate)
	available, _, err := standalone.Parse(dred, gotPacket, maxDRED, parseRate, true)
	if err != nil {
		t.Fatalf("standalone Parse(carried payload) error: %v", err)
	}
	if dred.Empty() || dred.LatentCount() <= 0 {
		t.Fatalf("standalone Parse retained no DRED latents (available=%d)", available)
	}
	if err := standalone.Process(dred, dred); err != nil {
		t.Fatalf("standalone Process error: %v", err)
	}
	if !dred.Processed() {
		t.Fatal("standalone DRED did not reach processed state")
	}
	if available <= 0 {
		t.Fatalf("standalone Parse reported no available DRED samples: %d", available)
	}

	frameSamples := decoderSampleRate / 50 // one 20 ms frame
	pcm := make([]float32, frameSamples*channels)
	n, err := dec.DecodeDRED(dred, frameSamples, pcm, frameSamples)
	if err != nil {
		t.Fatalf("DecodeDRED(carried payload) error: %v", err)
	}
	if n != frameSamples {
		t.Fatalf("DecodeDRED=%d want %d", n, frameSamples)
	}
}

func encoderDREDBitrateForFrameSize(frameSize int) int {
	bitrate := 40000
	if frameSize > 0 && frameSize < 960 {
		bitrate = (40000 * 960) / frameSize
	}
	if bitrate > 320000 {
		bitrate = 320000
	}
	return bitrate
}

func encoderDREDVoicedSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func encoderDREDFrame(frameIdx, frameSize, sampleRate, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := encoderDREDVoicedSample(frameIdx, i, frameSize, sampleRate)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = s
		}
	}
	return pcm
}

func encoderModeToPublic(mode encpkg.Mode) (Mode, error) {
	switch mode {
	case encpkg.ModeSILK:
		return ModeSILK, nil
	case encpkg.ModeHybrid:
		return ModeHybrid, nil
	case encpkg.ModeCELT:
		return ModeCELT, nil
	default:
		return 0, fmt.Errorf("unsupported encoder mode %v", mode)
	}
}

func encodeUntilDREDPacket(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int) {
	packet, payload, offset, _ := encodeUntilDREDPacketWithFrameIndex(t, mode, bandwidth, frameSize, channels)
	return packet, payload, offset
}

func encodeUntilDREDPacketWithFrameIndex(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int, int) {
	return encodeUntilDREDPacketWithSettings(t, encoderDREDPacketSettings{
		mode:      mode,
		bandwidth: bandwidth,
		frameSize: frameSize,
		channels:  channels,
		bitrate:   encoderDREDBitrateForFrameSize(frameSize),
	})
}

type encoderDREDPacketSettings struct {
	mode         encpkg.Mode
	bandwidth    Bandwidth
	frameSize    int
	channels     int
	bitrate      int
	cbr          bool
	dredDuration int // 0 selects the default 80
}

func encodeUntilDREDPacketWithSettings(t *testing.T, settings encoderDREDPacketSettings) ([]byte, []byte, int, int) {
	return scanDREDEmissionWithSettings(t, settings, true)
}

// scanDREDEmissionWithSettings checks the same 640-frame window as the C emit
// oracle. requireEmission controls only whether absence is an expected result.
func scanDREDEmissionWithSettings(t *testing.T, settings encoderDREDPacketSettings, requireEmission bool) ([]byte, []byte, int, int) {
	t.Helper()
	if settings.frameSize <= 0 {
		settings.frameSize = 960
	}
	if settings.channels <= 0 {
		settings.channels = 1
	}
	if settings.bitrate <= 0 {
		settings.bitrate = encoderDREDBitrateForFrameSize(settings.frameSize)
	}

	cfg := EncoderConfig{
		SampleRate:  48000,
		Channels:    settings.channels,
		Application: ApplicationAudio,
	}
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(settings.frameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(settings.bandwidth); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(settings.bitrate); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if settings.cbr {
		if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
			t.Fatalf("SetBitrateMode(CBR) error: %v", err)
		}
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dredDuration := settings.dredDuration
	if dredDuration == 0 {
		dredDuration = 80
	}
	if err := enc.SetDREDDuration(dredDuration); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	enc.enc.SetMode(settings.mode)

	wantMode, err := encoderModeToPublic(settings.mode)
	if err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, maxPacketBytesPerStream)
	for frameIdx := 0; frameIdx < libopusDREDPacketMaxFramesToTry; frameIdx++ {
		pcm := encoderDREDFrame(frameIdx, settings.frameSize, cfg.SampleRate, cfg.Channels)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frameIdx, err)
		}
		gotPacket := append([]byte(nil), packet[:n]...)
		toc := ParseTOC(gotPacket[0])
		packetDuration, err := opusPacketDurationSamples(gotPacket)
		if err != nil {
			t.Fatalf("parse packet duration frame=%d: %v", frameIdx, err)
		}
		if toc.Mode != wantMode || toc.Bandwidth != settings.bandwidth || packetDuration != settings.frameSize {
			continue
		}
		payload, frameOffset, ok, err := findDREDPayload(gotPacket)
		if err != nil {
			t.Fatalf("findDREDPayload(frame=%d) error: %v", frameIdx, err)
		}
		if ok {
			return gotPacket, append([]byte(nil), payload...), frameOffset, frameIdx
		}
	}
	if !requireEmission {
		return nil, nil, 0, libopusDREDPacketMaxFramesToTry
	}
	t.Fatalf("no DRED packet emitted for mode=%v bandwidth=%v frameSize=%d", settings.mode, settings.bandwidth, settings.frameSize)
	return nil, nil, 0, 0
}

func TestEncoderDREDEmissionFrameIndexMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      encpkg.Mode
		public    Mode
		bandwidth Bandwidth
		channels  int
	}{
		{name: "silk", mode: encpkg.ModeSILK, public: ModeSILK, bandwidth: BandwidthWideband, channels: 1},
		{name: "hybrid", mode: encpkg.ModeHybrid, public: ModeHybrid, bandwidth: BandwidthFullband, channels: 1},
		{name: "celt", mode: encpkg.ModeCELT, public: ModeCELT, bandwidth: BandwidthFullband, channels: 1},
		{name: "stereo_celt", mode: encpkg.ModeCELT, public: ModeCELT, bandwidth: BandwidthFullband, channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: tc.public,
				Bandwidth: tc.bandwidth,
				Channels:  tc.channels,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "DRED packet", err)
			}
			_, _, _, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, tc.mode, tc.bandwidth, 960, tc.channels)
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
		})
	}
}

func assertSilkMonoPrimaryFrameByteExact(t *testing.T, frameSize int) {
	t.Helper()
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	gotPacket, _, _ := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 1)
	_, gotFrames, _, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(got): %v", err)
	}
	_, wantFrames, _, _, err := parsePacketFramesAndPadding(packetInfo.packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(want): %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if !bytes.Equal(gotFrames[i], wantFrames[i]) {
			t.Fatalf("frame %d DIVERGES\n got=%x\nwant=%x", i, gotFrames[i], wantFrames[i])
		}
	}
}

func TestEncoderSilkMono20msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 960)
}

func TestEncoderSilkMono40msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 1920)
}

func TestEncoderSilkMono60msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 2880)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msCBR(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Bitrate:   24000,
		CBR:       true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus CBR silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithSettings(t, encoderDREDPacketSettings{
		mode:      encpkg.ModeSILK,
		bandwidth: BandwidthWideband,
		frameSize: 960,
		channels:  1,
		bitrate:   24000,
		cbr:       true,
	})
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if gotFrameIndex != packetInfo.frameIndex {
		t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 2880,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "60 ms DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 60 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 2880, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWidebandLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{3840, 4800, 5760} {
		t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "long SILK DRED packet", err)
			}
			wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
			if err != nil {
				t.Fatalf("findDREDPayload(libopus) error: %v", err)
			}
			if !ok {
				t.Fatal("libopus long SILK packet missing DRED payload")
			}

			gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 1)
			if ParseTOC(gotPacket[0]).Mode != ModeSILK {
				t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
			}
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
			if len(gotPacket) != len(packetInfo.packet) {
				t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
			}
			if gotOffset != wantOffset {
				t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
			}
			if !bytes.Equal(gotPayload, wantPayload) {
				t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
			}
			assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
		})
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereoPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus CELT packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeCELT, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeCELT {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeCELT)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullbandLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		frameSize int
		channels  int
	}{
		{frameSize: 1920, channels: 1},
		{frameSize: 2880, channels: 1},
		{frameSize: 1920, channels: 2},
	} {
		tc := tc
		t.Run(fmt.Sprintf("%d_samples_%dch", tc.frameSize, tc.channels), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
				Channels:  tc.channels,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "CELT DRED packet", err)
			}
			wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
			if err != nil {
				t.Fatalf("findDREDPayload(libopus) error: %v", err)
			}
			if !ok {
				t.Fatal("libopus CELT packet missing DRED payload")
			}

			gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, encpkg.ModeCELT, BandwidthFullband, tc.frameSize, tc.channels)
			toc := ParseTOC(gotPacket[0])
			if toc.Mode != ModeCELT || toc.Stereo != (tc.channels == 2) {
				t.Fatalf("got packet toc=%+v want celt channels=%d", toc, tc.channels)
			}
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
			if gotOffset != wantOffset {
				t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
			}
			assertCarriedDREDPayloadParity(t, gotPacket, gotPayload, wantPayload, gotOffset)
		})
	}
}

func assertSilkStereoPrimaryFrameByteExact(t *testing.T, frameSize int) {
	t.Helper()
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	gotPacket, _, _ := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 2)
	_, gotFrames, _, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(got): %v", err)
	}
	_, wantFrames, _, _, err := parsePacketFramesAndPadding(packetInfo.packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(want): %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if !bytes.Equal(gotFrames[i], wantFrames[i]) {
			t.Fatalf("frame %d DIVERGES\n got=%x\nwant=%x", i, gotFrames[i], wantFrames[i])
		}
	}
}

func TestEncoderSilkStereo20msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkStereoPrimaryFrameByteExact(t, 960)
}

func TestEncoderSilkStereo40msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkStereoPrimaryFrameByteExact(t, 1920)
}

func TestEncoderSilkStereo60msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkStereoPrimaryFrameByteExact(t, 2880)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 40 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 2880,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 60 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 60 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 2880, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func encodeUntilMultistreamDREDPacket(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels, streams, coupledStreams int, mapping []byte) ([]byte, []byte, int) {
	t.Helper()

	enc, err := NewMultistreamEncoder(48000, channels, streams, coupledStreams, mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(encoderDREDBitrateForFrameSize(frameSize)); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if err := enc.SetDREDDuration(80); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	// Reach inside via the unexported multistream.Encoder field to force the
	// requested base mode on all stream encoders. This mirrors the
	// enc.enc.SetMode(mode) hop used by the single-stream parity tests.
	enc.enc.SetMode(mode)

	wantMode, err := encoderModeToPublic(mode)
	if err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, maxPacketBytesPerStream*streams)
	for frameIdx := 0; frameIdx < 640; frameIdx++ {
		pcm := encoderDREDFrame(frameIdx, frameSize, enc.SampleRate(), channels)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frameIdx, err)
		}
		if n == 0 {
			// DTX-suppressed packet; keep searching.
			continue
		}
		gotPacket := append([]byte(nil), packet[:n]...)
		toc := ParseTOC(gotPacket[0])
		packetDuration, err := opusPacketDurationSamples(gotPacket)
		if err != nil {
			t.Fatalf("parse packet duration frame=%d: %v", frameIdx, err)
		}
		if toc.Mode != wantMode || toc.Bandwidth != bandwidth || packetDuration != frameSize {
			continue
		}
		payload, frameOffset, ok, err := findDREDPayload(gotPacket)
		if err != nil {
			t.Fatalf("findDREDPayload(frame=%d) error: %v", frameIdx, err)
		}
		if ok {
			return gotPacket, append([]byte(nil), payload...), frameOffset
		}
	}
	t.Fatalf("no DRED packet emitted via multistream encoder for mode=%v bandwidth=%v frameSize=%d", mode, bandwidth, frameSize)
	return nil, nil, 0
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   960,
		ForceMode:   ModeSILK,
		Bandwidth:   BandwidthWideband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo silk packet missing DRED payload")
	}

	// 2-channel stereo via one coupled stream: streams=1, coupledStreams=1,
	// mapping={0,1} routes input ch0 -> left, ch1 -> right of stream 0.
	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeSILK, BandwidthWideband, 960, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   960,
		ForceMode:   ModeCELT,
		Bandwidth:   BandwidthFullband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo CELT packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeCELT, BandwidthFullband, 960, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeCELT || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want celt stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   960,
		ForceMode:   ModeHybrid,
		Bandwidth:   BandwidthFullband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo hybrid packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeHybrid, BandwidthFullband, 960, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   1920,
		ForceMode:   ModeSILK,
		Bandwidth:   BandwidthWideband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 40 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 40 ms silk packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeSILK, BandwidthWideband, 1920, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   2880,
		ForceMode:   ModeSILK,
		Bandwidth:   BandwidthWideband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 60 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 60 ms silk packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeSILK, BandwidthWideband, 2880, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   1920,
		ForceMode:   ModeHybrid,
		Bandwidth:   BandwidthFullband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 40 ms hybrid packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusCELTFullband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   1920,
		ForceMode:   ModeCELT,
		Bandwidth:   BandwidthFullband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 40 ms CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 40 ms CELT packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeCELT, BandwidthFullband, 1920, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeCELT || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want celt stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func publicEncoderDREDRuntimeLoadedForTest(t *testing.T, enc *Encoder) bool {
	t.Helper()
	if enc == nil || enc.enc == nil {
		return false
	}
	dred := reflect.ValueOf(enc.enc).Elem().FieldByName("dred")
	if !dred.IsValid() || dred.IsNil() {
		return false
	}
	runtime := dred.Elem().FieldByName("runtime")
	return runtime.IsValid() && !runtime.IsNil()
}

func TestPublicDREDEncoderEncodeWithDormantModelStaysZeroAlloc(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedSilk})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("core encoder did not retain DRED-capable model")
	}
	if enc.enc.DREDReady() {
		t.Fatal("core encoder reports DRED ready before duration is armed")
	}

	pcm := testSineFrame(960)
	packet := make([]byte, 4000)
	for i := 0; i < 5; i++ {
		if n, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		} else if n == 0 {
			t.Fatal("warmup Encode returned empty packet")
		}
	}
	if publicEncoderDREDRuntimeLoadedForTest(t, enc) {
		t.Fatal("public Encode woke DRED runtime before duration was armed")
	}

	allocs := testing.AllocsPerRun(200, func() {
		if n, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		} else if n == 0 {
			t.Fatal("Encode returned empty packet")
		}
	})
	if allocs != 0 {
		t.Fatalf("public Encode with dormant DRED model allocs/op = %.2f, want 0", allocs)
	}
	if publicEncoderDREDRuntimeLoadedForTest(t, enc) {
		t.Fatal("allocation guard woke DRED runtime before duration was armed")
	}
}

func TestPublicDREDDecoderDecodeWithCoreModelsStaysZeroAlloc(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5): %v", err)
	}
	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("decoder did not retain neural model readiness")
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("core decoder DNN blob armed standalone DRED payload scanning")
	}
	if dec.dredGoodPacketMarkerActive() {
		t.Fatal("core decoder DNN blob armed good-packet DRED marker work")
	}

	packet := testCELTPacket()
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}
	if state := dec.dredState(); state != nil {
		t.Fatalf("warmup Decode woke DRED sidecar: %+v", state)
	}
	// celt_decode_lost selects FRAME_PLC_NEURAL for a lost CELT frame once the
	// PLC model is loaded, so the neural concealment state wakes while the DRED
	// payload state stays asleep: no DRED payload was ever received.
	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("warmup Decode(nil): %v", err)
	}
	state := dec.dredState()
	if state == nil || state.decoderDREDNeuralState == nil {
		t.Fatalf("warmup Decode(nil) did not run neural concealment: %+v", state)
	}
	if state.decoderDREDPayloadState != nil {
		t.Fatalf("warmup Decode(nil) woke DRED payload state without a DRED payload: %+v", state)
	}
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup recovery Decode: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("Decode(nil): %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("public Decode and loss concealment with core DNN models allocs/op = %.2f, want 0", allocs)
	}
	if state := dec.dredState(); state == nil || state.decoderDREDPayloadState != nil {
		t.Fatalf("allocation guard woke DRED payload state: %+v", state)
	}
}
