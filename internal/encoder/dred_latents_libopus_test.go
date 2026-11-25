//go:build gopus_dred || gopus_osce

package encoder

import (
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var (
	encoderLibopusLatentsTraceOnce sync.Once
	encoderLibopusLatentsTracePath string
	encoderLibopusLatentsTraceErr  error

	encoderLibopusPitchBlobOnce sync.Once
	encoderLibopusPitchBlob     []byte
	encoderLibopusPitchBlobErr  error

	encoderLibopusDREDBlobOnce sync.Once
	encoderLibopusDREDBlob     []byte
	encoderLibopusDREDBlobErr  error
)

type encoderLibopusDREDFrameTrace struct {
	frameIdx    int
	latentsFill int32
	dredOffset  int32
	latentOff   int32
	latents     [][rdovae.LatentDim]float32
}

func TestEncoderDREDInitialLatentsTraceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{960, 1920, 2880} {
			channels := channels
			frameSize := frameSize
			t.Run(fmt.Sprintf("%dch_%d", channels, frameSize), func(t *testing.T) {
				const (
					sampleRate = 48000
					extraDelay = sampleRate / 250
				)

				want := probeEncoderLibopusDREDLatentsTrace(t, channels, frameSize)
				if len(want) == 0 {
					t.Fatal("libopus DRED latents trace is empty")
				}
				blob := requireEncoderLibopusNeuralModelBlob(t)
				parsed, err := dnnblob.Clone(blob)
				if err != nil {
					t.Fatalf("Clone libopus encoder model blob: %v", err)
				}

				enc := NewEncoder(sampleRate, channels)
				enc.SetDNNBlob(parsed)
				if err := enc.SetDREDDuration(4); err != nil {
					t.Fatalf("SetDREDDuration error: %v", err)
				}

				got := make([]encoderLibopusDREDFrameTrace, len(want))
				for frameIdx := range want {
					frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
					emitted := enc.processDREDLatents(frame, extraDelay)
					if emitted == 0 {
						t.Fatalf("frame %d processDREDLatents emitted 0", frameIdx)
					}
					got[frameIdx] = snapshotEncoderDREDTrace(t, enc, frameIdx)
				}

				compareEncoderDREDTraces(t, got, want)
			})
		}
	}
}

func TestEncoderDREDLongCELTLatentsUseLibopusSubframeCadence(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			const (
				sampleRate = 48000
				frameSize  = 2880
				chunkSize  = 960
				extraDelay = sampleRate / 250
			)

			want := probeEncoderLibopusDREDLatentsTraceWithChunkSize(t, channels, frameSize, chunkSize)
			if len(want) == 0 {
				t.Fatal("libopus DRED latents trace is empty")
			}
			blob := requireEncoderLibopusNeuralModelBlob(t)
			parsed, err := dnnblob.Clone(blob)
			if err != nil {
				t.Fatalf("Clone libopus encoder model blob: %v", err)
			}

			enc := NewEncoder(sampleRate, channels)
			enc.SetDNNBlob(parsed)
			if err := enc.SetDREDDuration(4); err != nil {
				t.Fatalf("SetDREDDuration error: %v", err)
			}

			got := make([]encoderLibopusDREDFrameTrace, len(want))
			for frameIdx := range want {
				frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
				emitted := enc.processDREDLatentsForPacket(frame, frameSize, extraDelay, ModeCELT)
				if emitted != frameSize/chunkSize {
					t.Fatalf("frame %d processDREDLatentsForPacket emitted %d want %d", frameIdx, emitted, frameSize/chunkSize)
				}
				got[frameIdx] = snapshotEncoderDREDTrace(t, enc, frameIdx)
			}

			compareEncoderDREDTraces(t, got, want)
		})
	}
}

func TestEncoderDREDSetDNNBlobPreservesActiveRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	raw := requireEncoderLibopusNeuralModelBlob(t)
	newBlob := func() *dnnblob.Blob {
		t.Helper()
		blob, err := dnnblob.Clone(raw)
		if err != nil {
			t.Fatalf("Clone libopus encoder model blob: %v", err)
		}
		return blob
	}

	const (
		sampleRate  = 16000
		channels    = 1
		frameSize   = 320
		frameCount  = 7
		reloadFrame = 3
	)

	ref := NewEncoder(sampleRate, channels)
	ref.SetDNNBlob(newBlob())
	if err := ref.SetDREDDuration(4); err != nil {
		t.Fatalf("reference SetDREDDuration error: %v", err)
	}
	reloaded := NewEncoder(sampleRate, channels)
	reloaded.SetDNNBlob(newBlob())
	if err := reloaded.SetDREDDuration(4); err != nil {
		t.Fatalf("reloaded SetDREDDuration error: %v", err)
	}

	want := make([]encoderLibopusDREDFrameTrace, frameCount)
	got := make([]encoderLibopusDREDFrameTrace, frameCount)
	for frameIdx := 0; frameIdx < frameCount; frameIdx++ {
		frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
		if emitted := ref.processDREDLatents(frame, 0); emitted != 1 {
			t.Fatalf("reference frame %d emitted %d want 1", frameIdx, emitted)
		}
		want[frameIdx] = snapshotEncoderDREDTrace(t, ref, frameIdx)

		if frameIdx == reloadFrame {
			if reloaded.dred == nil || reloaded.dred.runtime == nil {
				t.Fatal("reloaded encoder has no active DRED runtime before reload")
			}
			runtimeBeforeReload := reloaded.dred.runtime
			reloaded.SetDNNBlob(newBlob())
			if reloaded.dred.runtime != runtimeBeforeReload {
				t.Fatal("SetDNNBlob replaced active DRED runtime")
			}
		}
		if emitted := reloaded.processDREDLatents(frame, 0); emitted != 1 {
			t.Fatalf("reloaded frame %d emitted %d want 1", frameIdx, emitted)
		}
		got[frameIdx] = snapshotEncoderDREDTrace(t, reloaded, frameIdx)
	}

	compareEncoderDREDTraces(t, got, want)
}

func snapshotEncoderDREDTrace(t *testing.T, enc *Encoder, frameIdx int) encoderLibopusDREDFrameTrace {
	t.Helper()
	if enc.dred == nil || enc.dred.runtime == nil {
		t.Fatal("DRED runtime is nil")
	}
	rt := enc.dred.runtime
	count := rt.latentsFill
	if count > 4 {
		count = 4
	}
	trace := encoderLibopusDREDFrameTrace{
		frameIdx:    frameIdx,
		latentsFill: rt.latentsFill,
		dredOffset:  rt.dredOffset,
		latentOff:   rt.latentOffset,
		latents:     make([][rdovae.LatentDim]float32, int(count)),
	}
	for pos := 0; pos < int(count); pos++ {
		copy(trace.latents[pos][:], rt.latentsBuffer[pos*rdovae.LatentDim:(pos+1)*rdovae.LatentDim])
	}
	return trace
}

// compareEncoderDREDTraces requires the same latent bits and state cadence
// from Go and the native C build with matching feature flags and CPU dispatch.
func compareEncoderDREDTraces(t *testing.T, got, want []encoderLibopusDREDFrameTrace) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("trace count=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].frameIdx != want[i].frameIdx {
			t.Fatalf("trace %d frameIdx=%d want %d", i, got[i].frameIdx, want[i].frameIdx)
		}
		if got[i].latentsFill != want[i].latentsFill {
			t.Fatalf("frame %d latentsFill=%d want %d", i, got[i].latentsFill, want[i].latentsFill)
		}
		if got[i].dredOffset != want[i].dredOffset {
			t.Fatalf("frame %d dredOffset=%d want %d", i, got[i].dredOffset, want[i].dredOffset)
		}
		if got[i].latentOff != want[i].latentOff {
			t.Fatalf("frame %d latentOff=%d want %d", i, got[i].latentOff, want[i].latentOff)
		}
		if len(got[i].latents) != len(want[i].latents) {
			t.Fatalf("frame %d latent rows=%d want %d", i, len(got[i].latents), len(want[i].latents))
		}
	}
	for i := range want {
		for pos := range want[i].latents {
			for k := range rdovae.LatentDim {
				g := math.Float32bits(got[i].latents[pos][k])
				w := math.Float32bits(want[i].latents[pos][k])
				if g != w {
					t.Fatalf("frame %d row %d latent %d: Go=%08x C=%08x", i, pos, k, g, w)
				}
			}
		}
	}
}

func encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels int) []opusRes {
	pcm := make([]opusRes, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := opusRes(encoderLibopusDREDTraceSample(frameIdx, i, frameSize, sampleRate))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func encoderLibopusDREDTraceSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
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

func requireEncoderLibopusNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	pitchBlob := probeEncoderLibopusPitchDNNBlob(t)
	dredBlob := probeEncoderLibopusDREDEncoderBlob(t)
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob
}

func probeEncoderLibopusPitchDNNBlob(t *testing.T) []byte {
	t.Helper()
	encoderLibopusPitchBlobOnce.Do(func() {
		binPath, err := buildEncoderLibopusDREDHelper("libopus_pitchdnn_model_blob.c", "gopus_encoder_libopus_pitchdnn_model_blob", true)
		if err != nil {
			encoderLibopusPitchBlobErr = err
			return
		}
		encoderLibopusPitchBlob, encoderLibopusPitchBlobErr = runEncoderLibopusModelBlobHelper(binPath)
	})
	if encoderLibopusPitchBlobErr != nil {
		libopustest.HelperUnavailable(t, "pitch model blob", encoderLibopusPitchBlobErr)
	}
	return encoderLibopusPitchBlob
}

func probeEncoderLibopusDREDEncoderBlob(t *testing.T) []byte {
	t.Helper()
	encoderLibopusDREDBlobOnce.Do(func() {
		binPath, err := buildEncoderLibopusDREDHelper("libopus_dred_encoder_model_blob.c", "gopus_encoder_libopus_dred_encoder_model_blob", true)
		if err != nil {
			encoderLibopusDREDBlobErr = err
			return
		}
		encoderLibopusDREDBlob, encoderLibopusDREDBlobErr = runEncoderLibopusModelBlobHelper(binPath)
	})
	if encoderLibopusDREDBlobErr != nil {
		libopustest.HelperUnavailable(t, "DRED encoder model blob", encoderLibopusDREDBlobErr)
	}
	return encoderLibopusDREDBlob
}

func probeEncoderLibopusDREDLatentsTrace(t *testing.T, channels, frameSize int) []encoderLibopusDREDFrameTrace {
	return probeEncoderLibopusDREDLatentsTraceWithChunkSize(t, channels, frameSize, frameSize)
}

func probeEncoderLibopusDREDLatentsTraceWithChunkSize(t *testing.T, channels, frameSize, chunkSize int) []encoderLibopusDREDFrameTrace {
	t.Helper()
	binPath, err := getEncoderLibopusDREDLatentsTracePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED latents trace", err)
	}
	data, err := libopustest.RunHelperArgs(binPath, nil, fmt.Sprintf("%d", channels), fmt.Sprintf("%d", frameSize), fmt.Sprintf("%d", chunkSize))
	if err != nil {
		t.Fatalf("run libopus DRED latents trace helper: %v", err)
	}
	return parseEncoderLibopusDREDLatentsTrace(t, data)
}

func parseEncoderLibopusDREDLatentsTrace(t *testing.T, data []byte) []encoderLibopusDREDFrameTrace {
	t.Helper()
	var traces []encoderLibopusDREDFrameTrace
	for offset := 0; offset < len(data); {
		if len(data)-offset < 24 || string(data[offset:offset+4]) != "GDLT" {
			t.Fatalf("unexpected libopus trace output at offset %d", offset)
		}
		trace := encoderLibopusDREDFrameTrace{
			frameIdx:    int(binary.LittleEndian.Uint32(data[offset+4 : offset+8])),
			latentsFill: int32(binary.LittleEndian.Uint32(data[offset+8 : offset+12])),
			dredOffset:  int32(binary.LittleEndian.Uint32(data[offset+12 : offset+16])),
			latentOff:   int32(binary.LittleEndian.Uint32(data[offset+16 : offset+20])),
		}
		positionCount := int(binary.LittleEndian.Uint32(data[offset+20 : offset+24]))
		pos := offset + 24
		trace.latents = make([][rdovae.LatentDim]float32, positionCount)
		for i := 0; i < positionCount; i++ {
			if len(data)-pos < 4*rdovae.LatentDim {
				t.Fatalf("truncated libopus trace at offset %d", pos)
			}
			for k := 0; k < rdovae.LatentDim; k++ {
				trace.latents[i][k] = math.Float32frombits(binary.LittleEndian.Uint32(data[pos+4*k : pos+4*(k+1)]))
			}
			pos += 4 * rdovae.LatentDim
		}
		traces = append(traces, trace)
		offset = pos
	}
	return traces
}

func getEncoderLibopusDREDLatentsTracePath() (string, error) {
	encoderLibopusLatentsTraceOnce.Do(func() {
		encoderLibopusLatentsTracePath, encoderLibopusLatentsTraceErr = buildEncoderLibopusDREDHelper("libopus_dred_latents_trace.c", "gopus_encoder_libopus_dred_latents_trace", true)
	})
	if encoderLibopusLatentsTraceErr != nil {
		return "", encoderLibopusLatentsTraceErr
	}
	return encoderLibopusLatentsTracePath, nil
}

func runEncoderLibopusModelBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
}

func buildEncoderLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, includeInternal)
}

func TestDREDBitsTableMatchesLibopusReference(t *testing.T) {
	want := readLibopusDREDBitsTable(t)
	if len(want) != len(dredBitsTable) {
		t.Fatalf("dredBitsTable len=%d want %d", len(dredBitsTable), len(want))
	}
	for i, got := range dredBitsTable {
		if got != want[i] {
			t.Fatalf("dredBitsTable[%d]=%g want %g", i, got, want[i])
		}
	}
}

func TestComputeDREDEmissionPlanMatchesLibopusFormula(t *testing.T) {
	libopusBitsTable := readLibopusDREDBitsTable(t)

	rates := []struct {
		name       string
		sampleRate int
		frameSize  int
	}{
		{name: "8k_20ms", sampleRate: 8000, frameSize: 160},
		{name: "12k_20ms", sampleRate: 12000, frameSize: 240},
		{name: "16k_20ms", sampleRate: 16000, frameSize: 320},
		{name: "24k_20ms", sampleRate: 24000, frameSize: 480},
		{name: "48k_20ms", sampleRate: 48000, frameSize: 960},
		{name: "16k_60ms", sampleRate: 16000, frameSize: 960},
		{name: "48k_60ms", sampleRate: 48000, frameSize: 2880},
	}
	settings := []struct {
		name     string
		bitrate  int
		loss     int
		duration int
		fec      bool
	}{
		{name: "no_fec_low_loss", bitrate: 24000, loss: 3, duration: 8},
		{name: "no_fec_mid_loss", bitrate: 64000, loss: 10, duration: 8},
		{name: "no_fec_high_loss", bitrate: 96000, loss: 60, duration: 20},
		{name: "fec_mid_loss", bitrate: 64000, loss: 10, duration: 8, fec: true},
		{name: "fec_high_loss", bitrate: 96000, loss: 60, duration: 20, fec: true},
	}

	for _, rate := range rates {
		for _, setting := range settings {
			t.Run(rate.name+"/"+setting.name, func(t *testing.T) {
				enc := newDREDPlanTestEncoder(rate.sampleRate, setting.bitrate, setting.loss, setting.duration)
				enc.fecEnabled = setting.fec

				got, gotOK := enc.computeDREDEmissionPlan(rate.frameSize)
				want, wantOK := libopusDREDPlan(rate.sampleRate, rate.frameSize, setting.bitrate, setting.loss, setting.duration, setting.fec, libopusBitsTable)
				if gotOK != wantOK {
					t.Fatalf("enabled=%v want %v; got plan %+v want %+v", gotOK, wantOK, got, want)
				}
				if gotOK && got != want {
					t.Fatalf("plan=%+v want %+v", got, want)
				}
			})
		}
	}
}

func TestComputeDREDEmissionPlanUsesFECControlFlag(t *testing.T) {
	withoutLBRR := newDREDPlanTestEncoder(48000, 64000, 10, 8)
	withoutLBRR.fecEnabled = true
	withoutLBRR.lbrrCoded = false
	got, ok := withoutLBRR.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED with FEC enabled")
	}

	withLBRR := newDREDPlanTestEncoder(48000, 64000, 10, 8)
	withLBRR.fecEnabled = true
	withLBRR.lbrrCoded = true
	want, ok := withLBRR.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED with LBRR coded")
	}
	if got != want {
		t.Fatalf("plan with FEC enabled differs by LBRR state: got %+v want %+v", got, want)
	}

	noFEC := newDREDPlanTestEncoder(48000, 64000, 10, 8)
	noFEC.fecEnabled = false
	noFECPlan, ok := noFEC.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED without FEC")
	}
	if got == noFECPlan {
		t.Fatalf("FEC-enabled plan matched no-FEC plan: %+v", got)
	}
}

func TestDREDMaxChunksOnlyCapsVBR(t *testing.T) {
	enc := newDREDPlanTestEncoder(48000, 20000, 20, 80)
	plan, ok := enc.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED")
	}
	if plan.targetChunks != 2 {
		t.Fatalf("targetChunks=%d want 2", plan.targetChunks)
	}

	if got := maxDREDChunks(int(enc.dred.duration), int(plan.targetChunks), true); got != 2 {
		t.Fatalf("VBR maxDREDChunks()=%d want 2", got)
	}
	wantCBRChunks := int((enc.dred.duration + 5) / 4)
	if wantCBRChunks > internaldred.NumRedundancyFrames/2 {
		wantCBRChunks = internaldred.NumRedundancyFrames / 2
	}
	if got := maxDREDChunks(int(enc.dred.duration), int(plan.targetChunks), false); got != wantCBRChunks {
		t.Fatalf("CBR maxDREDChunks()=%d want %d", got, wantCBRChunks)
	}
}

func newDREDPlanTestEncoder(sampleRate, bitrate, packetLoss, duration int) *Encoder {
	return &Encoder{
		sampleRate: int32(sampleRate),
		bitrate:    int32(bitrate),
		packetLoss: int32(packetLoss),
		encoderDREDFields: encoderDREDFields{
			dred: &dredEncoderExtras{
				duration: int32(duration),
				models: dredEncoderModels{
					encoder: &rdovae.EncoderModel{},
					pitch:   &lpcnetplc.PitchDNNModel{},
				},
			},
		},
	}
}

func libopusDREDPlan(sampleRate, frameSize, bitrate, packetLoss, duration int, fec bool, bitsTable []float32) (dredEmissionPlan, bool) {
	if sampleRate <= 0 || frameSize <= 0 || bitrate <= 0 || duration <= 0 {
		return dredEmissionPlan{}, false
	}
	if packetLoss < 0 {
		packetLoss = 0
	}
	if packetLoss > 100 {
		packetLoss = 100
	}

	var dredFrac float32
	bitrateOffset := 12000
	if fec {
		dredFrac = libopusMinFloat32(0.7, 3.0*float32(packetLoss)/100.0)
		bitrateOffset = 20000
	} else if packetLoss > 5 {
		dredFrac = libopusMinFloat32(0.8, 0.55+float32(packetLoss)/100.0)
	} else {
		dredFrac = 12.0 * float32(packetLoss) / 100.0
	}
	dredFrac = dredFrac / (dredFrac + (1-dredFrac)*(float32(frameSize*50)/float32(sampleRate)))

	rateBudget := bitrate - bitrateOffset
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
	if bitrate-bitrateOffset > 36000 {
		dQ = 3
	}
	qmax := 15
	targetDREDBitrate := int(dredFrac * float32(bitrate-bitrateOffset))
	if targetDREDBitrate < 0 {
		targetDREDBitrate = 0
	}
	targetBits := libopusDREDBitrateToBits(targetDREDBitrate, sampleRate, frameSize)
	maxBits, targetChunks := libopusEstimateDREDBits(q0, dQ, qmax, duration, targetBits, bitsTable)
	if targetChunks < 2 {
		return dredEmissionPlan{}, false
	}
	dredBitrate := libopusDREDBitsToBitrate(maxBits, sampleRate, frameSize)
	if targetDREDBitrate < dredBitrate {
		dredBitrate = targetDREDBitrate
	}
	if dredBitrate <= 0 {
		return dredEmissionPlan{}, false
	}
	return dredEmissionPlan{
		q0:           int32(q0),
		dQ:           int32(dQ),
		qmax:         int32(qmax),
		targetChunks: int32(targetChunks),
		bitrate:      int32(dredBitrate),
	}, true
}

func libopusEstimateDREDBits(q0, dQ, qmax, duration, targetBits int, bitsTable []float32) (int, int) {
	bitsUsed := float32(8 * (3 + internaldred.ExperimentalHeaderBytes))
	bitsUsed += 50.0 + bitsTable[q0]
	dredChunks := (duration + 5) / 4
	if dredChunks > internaldred.NumRedundancyFrames/2 {
		dredChunks = internaldred.NumRedundancyFrames / 2
	}
	targetChunks := 0
	for i := 0; i < dredChunks; i++ {
		q := libopusComputeDREDQuantizer(q0, dQ, qmax, i)
		bitsUsed += bitsTable[q]
		if bitsUsed < float32(targetBits) {
			targetChunks = i + 1
		}
	}
	return int(math.Floor(float64(float32(0.5) + bitsUsed))), targetChunks
}

func libopusComputeDREDQuantizer(q0, dQ, qmax, i int) int {
	dQTable := [...]int{0, 2, 3, 4, 6, 8, 12, 16}
	quant := q0 + (dQTable[dQ]*i+8)/16
	if quant > qmax {
		return qmax
	}
	return quant
}

func libopusDREDBitrateToBits(bitrate, sampleRate, frameSize int) int {
	if bitrate <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitrate * 6 / unitsPerFrame
}

func libopusDREDBitsToBitrate(bitCount, sampleRate, frameSize int) int {
	if bitCount <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitCount * unitsPerFrame / 6
}

func libopusMinFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func readLibopusDREDBitsTable(t *testing.T) []float32 {
	t.Helper()

	data := libopustest.ReadPinnedSourceFileOrSkip(t, "opus_encoder.c", "src", "opus_encoder.c")

	re := regexp.MustCompile(`(?s)static\s+const\s+float\s+dred_bits_table\[16\]\s*=\s*\{([^}]*)\}`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatal("libopus dred_bits_table not found")
	}
	fields := strings.Split(string(m[1]), ",")
	values := make([]float32, 0, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(field)
		value = strings.TrimSuffix(value, "f")
		parsed, err := strconv.ParseFloat(value, 32)
		if err != nil {
			t.Fatalf("parse libopus dred_bits_table value %q: %v", field, err)
		}
		values = append(values, float32(parsed))
	}
	return values
}

func TestEncoderSetDREDDurationGrowsScratchPacketWhenArmed(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if got := len(enc.scratchPacket); got != defaultScratchPacketBytes {
		t.Fatalf("fresh scratchPacket len=%d want %d", got, defaultScratchPacketBytes)
	}

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	if got := len(enc.scratchPacket); got != extensionScratchPacketBytes {
		t.Fatalf("armed DRED scratchPacket len=%d want %d", got, extensionScratchPacketBytes)
	}
}

func TestDREDEncoderFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	checkFieldsHaveType(t, reflect.TypeOf(dredEmissionPlan{}), int32Type,
		"q0",
		"dQ",
		"qmax",
		"targetChunks",
		"bitrate",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dredEncoderRuntime{}), int32Type,
		"latentsFill",
		"dredOffset",
		"latentOffset",
		"lastExtraDREDOffset",
		"emitted",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dredEncoderPacketSnapshot{}), int32Type,
		"latentsFill",
		"dredOffset",
		"latentOffset",
		"lastExtraDREDOffset",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dredEncoderExtras{}), int32Type,
		"duration",
	)
}
