//go:build (gopus_dred || gopus_osce) && !gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var neuralNoSidecarRates = [...]int{8000, 12000, 16000, 24000, 48000}

var neuralNoSidecarSteps = [...]string{"received", "loss", "recovery", "loss_again", "recovery_again"}

type neuralNoSidecarStep struct {
	ret         int
	finalRange  uint32
	blend       int
	analysisPos int
	predictPos  int
	pcmBits     []uint32
}

type neuralNoSidecarRateSequence struct {
	sampleRate int
	frameSize  int
	steps      [2][len(neuralNoSidecarSteps)]neuralNoSidecarStep
}

type neuralNoSidecarSequence struct {
	model       []byte
	packet      []byte
	arch        uint32
	rtcdEnabled bool
	presumeNEON bool
	features    uint32
	modelSource uint32
	rates       [len(neuralNoSidecarRates)]neuralNoSidecarRateSequence
}

// selectedNeuralNoSidecarSequence pairs the helper and archive with the
// current Go DRED/OSCE/QEXT and instruction build. C produces one SILK WB
// packet, then decodes received/loss/recovery/loss/recovery at every supported
// API rate with the main LPCNet model loaded and no DRED sidecar. libopus source:
// opus_decoder.c:443 and silk/PLC.c:400.
func selectedNeuralNoSidecarSequence(t *testing.T) neuralNoSidecarSequence {
	t.Helper()
	libopustest.RequireOracle(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	ensure := libopustest.EnsureDREDBuild
	if extsupport.OSCERuntime {
		ensure = libopustest.EnsureOSCEBuild
	}
	_, buildDir, err := ensure(root)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(buildDir, ".libs", "libopus.a")
	bin, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		SourceFile:  "libopus_decoder_neural_nosidecar_sequence.c",
		OutputBase:  "gopus_decoder_neural_nosidecar_sequence",
		RefIncludes: []string{".", "celt", "silk", "src"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("selected C archive %s: %v", archive, err)
	}
	t.Logf("selected C archive=%s Go target=%s/%s variant=%s DRED=%t OSCE=%t QEXT=%t helper=%s",
		archive, runtime.GOOS, runtime.GOARCH, variant,
		extsupport.DRED, extsupport.OSCERuntime, extsupport.QEXT, bin)

	model, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		t.Fatal(err)
	}
	if len(model) == 0 {
		t.Fatal("empty pinned neural model")
	}
	var request bytes.Buffer
	if err := binary.Write(&request, binary.LittleEndian, uint32(len(model))); err != nil {
		t.Fatal(err)
	}
	request.Write(model)
	cmd := exec.Command(bin)
	cmd.Stdin = &request
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	wire, err := cmd.Output()
	if err != nil {
		t.Fatalf("selected C helper: %v stderr=%s", err, stderr.String())
	}
	r := bytes.NewReader(wire)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "DNCP" {
		t.Fatalf("C protocol magic=%q err=%v", magic, err)
	}
	readU32 := func() uint32 {
		t.Helper()
		var value uint32
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	result := neuralNoSidecarSequence{model: model}
	result.arch = readU32()
	result.rtcdEnabled = readU32() != 0
	result.presumeNEON = readU32() != 0
	result.features, result.modelSource = readU32(), readU32()
	var wantFeatures uint32
	if extsupport.DRED {
		wantFeatures |= 1
	}
	if extsupport.OSCERuntime {
		wantFeatures |= 2
	}
	if extsupport.QEXT {
		wantFeatures |= 4
	}
	if result.features != wantFeatures || (result.modelSource != 1 && result.modelSource != 2) {
		t.Fatalf("C feature/model identity=%03b/%d Go feature identity=%03b",
			result.features, result.modelSource, wantFeatures)
	}
	if variant == libopustooling.LibopusReferenceSIMD {
		switch runtime.GOARCH {
		case "amd64":
			if !result.rtcdEnabled || result.arch == 0 || result.presumeNEON {
				t.Fatalf("C SIMD identity: RTCD=%t arch=%d presumeNEON=%t",
					result.rtcdEnabled, result.arch, result.presumeNEON)
			}
		case "arm64":
			if result.rtcdEnabled || result.arch != 0 || !result.presumeNEON {
				t.Fatalf("C NEON identity: RTCD=%t arch=%d presumeNEON=%t",
					result.rtcdEnabled, result.arch, result.presumeNEON)
			}
		default:
			t.Fatalf("no selected SIMD oracle identity contract for %s", runtime.GOARCH)
		}
	} else if result.rtcdEnabled || result.arch != 0 || result.presumeNEON {
		t.Fatalf("C scalar identity: RTCD=%t arch=%d presumeNEON=%t",
			result.rtcdEnabled, result.arch, result.presumeNEON)
	}
	t.Logf("C opus_select_arch=%d RTCD=%t presumeNEON=%t variant=%s features=%03b model_source=%d",
		result.arch, result.rtcdEnabled, result.presumeNEON, variant, result.features, result.modelSource)
	packetLen := int(readU32())
	if packetLen <= 0 || packetLen > 1275 {
		t.Fatalf("C packet length=%d", packetLen)
	}
	result.packet = make([]byte, packetLen)
	if _, err := io.ReadFull(r, result.packet); err != nil {
		t.Fatal(err)
	}
	toc := ParseTOC(result.packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
		t.Fatalf("C packet TOC=%+v, want SILK WB", toc)
	}
	for _, sampleRate := range neuralNoSidecarRates {
		if n, err := packetSamplesAtRate(result.packet, sampleRate); err != nil || n != sampleRate/50 {
			t.Fatalf("C packet samples at %d Hz=%d err=%v, want 20 ms", sampleRate, n, err)
		}
	}
	if _, _, found, err := findDREDPayload(result.packet); err != nil || found {
		t.Fatalf("C packet DRED sidecar found=%v err=%v", found, err)
	}
	rateCount := int(readU32())
	if rateCount != len(neuralNoSidecarRates) {
		t.Fatalf("C API-rate count=%d want=%d", rateCount, len(neuralNoSidecarRates))
	}
	for rateIdx, sampleRate := range neuralNoSidecarRates {
		sequence := &result.rates[rateIdx]
		sequence.sampleRate = int(readU32())
		if sequence.sampleRate != sampleRate {
			t.Fatalf("C API rate[%d]=%d want=%d", rateIdx, sequence.sampleRate, sampleRate)
		}
		sequence.frameSize = sequence.sampleRate / 50
		for complexityIdx, complexity := range []int{0, 5} {
			if got := readU32(); int(got) != complexity {
				t.Fatalf("C complexity=%d want=%d at %d Hz", got, complexity, sampleRate)
			}
			if loaded := readU32(); loaded != 1 {
				t.Fatalf("C main LPCNet loaded=%d at complexity=%d rate=%d", loaded, complexity, sampleRate)
			}
			for stepIdx := range neuralNoSidecarSteps {
				step := &sequence.steps[complexityIdx][stepIdx]
				step.ret = int(int32(readU32()))
				step.finalRange = readU32()
				step.blend = int(readU32())
				step.analysisPos = int(readU32())
				step.predictPos = int(readU32())
				if step.ret != sequence.frameSize {
					t.Fatalf("C %d Hz complexity=%d %s ret=%d want=%d", sampleRate, complexity,
						neuralNoSidecarSteps[stepIdx], step.ret, sequence.frameSize)
				}
				step.pcmBits = make([]uint32, step.ret)
				for i := range step.pcmBits {
					step.pcmBits[i] = readU32()
				}
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("C protocol trailing bytes=%d", r.Len())
	}
	return result
}

func newNeuralNoSidecarDecoder(t *testing.T, model []byte, sampleRate, complexity int) *Decoder {
	t.Helper()
	dec, err := NewDecoder(DecoderConfig{SampleRate: sampleRate, Channels: 1})
	if err != nil {
		t.Fatalf("NewDecoder(%d Hz): %v", sampleRate, err)
	}
	if err := dec.SetComplexity(complexity); err != nil {
		t.Fatalf("SetComplexity(%d): %v", complexity, err)
	}
	if err := dec.SetDNNBlob(model); err != nil {
		t.Fatalf("SetDNNBlob(%d Hz, %d bytes): %v", sampleRate, len(model), err)
	}
	if !dec.dredNeuralConcealmentAvailable() || dec.dredPayloadState() != nil {
		t.Fatalf("main LPCNet model or sidecar state invalid at complexity=%d rate=%d", complexity, sampleRate)
	}
	return dec
}

func decodeNeuralNoSidecarStep(dec *Decoder, packet []byte, pcm []float32) bool {
	n, err := dec.Decode(packet, pcm)
	return err == nil && n == len(pcm)
}

func neuralNoSidecarPacketForStep(packet []byte, step int) []byte {
	if step == 1 || step == 3 {
		return nil
	}
	return packet
}

func primeNeuralNoSidecarSequence(dec *Decoder, packet []byte, pcm []float32, steps int) bool {
	for step := 0; step < steps; step++ {
		if !decodeNeuralNoSidecarStep(dec, neuralNoSidecarPacketForStep(packet, step), pcm) {
			return false
		}
	}
	return true
}

func TestDecoderNoSidecarDeepPLCComplexityMatchesLibopus(t *testing.T) {
	want := selectedNeuralNoSidecarSequence(t)
	for rateIdx, sampleRate := range neuralNoSidecarRates {
		sequence := want.rates[rateIdx]
		t.Run(fmt.Sprintf("rate_%d", sampleRate), func(t *testing.T) {
			for complexityIdx, complexity := range []int{0, 5} {
				t.Run(fmt.Sprintf("complexity_%d", complexity), func(t *testing.T) {
					dec := newNeuralNoSidecarDecoder(t, want.model, sampleRate, complexity)
					pcm := make([]float32, sequence.frameSize)
					for stepIdx, label := range neuralNoSidecarSteps {
						src := neuralNoSidecarPacketForStep(want.packet, stepIdx)
						n, err := dec.Decode(src, pcm)
						if err != nil {
							t.Fatalf("%s: %v", label, err)
						}
						ref := sequence.steps[complexityIdx][stepIdx]
						if n != ref.ret || dec.FinalRange() != ref.finalRange {
							t.Fatalf("%s n/range=%d/%08x C=%d/%08x", label, n, dec.FinalRange(), ref.ret, ref.finalRange)
						}
						for i := 0; i < n; i++ {
							if got := math.Float32bits(pcm[i]); got != ref.pcmBits[i] {
								t.Fatalf("%s PCM[%d]=%08x C=%08x; C PLC blend=%d analysis=%d predict=%d",
									label, i, got, ref.pcmBits[i], ref.blend, ref.analysisPos, ref.predictPos)
							}
						}
						if dec.dredPayloadState() != nil {
							t.Fatalf("%s activated DRED sidecar without a payload", label)
						}
						if complexity == 5 && stepIdx >= 1 {
							r := dec.dredRecoveryState()
							if r == nil {
								t.Fatalf("%s has no neural PLC state", label)
							}
							state := r.dredPLC.Snapshot()
							if state.Blend != ref.blend || state.AnalysisPos != ref.analysisPos || state.PredictPos != ref.predictPos {
								t.Fatalf("%s PLC blend/analysis/predict=%d/%d/%d C=%d/%d/%d",
									label, state.Blend, state.AnalysisPos, state.PredictPos,
									ref.blend, ref.analysisPos, ref.predictPos)
							}
						}
					}
				})
			}
		})
	}
}

func TestDecoderNoSidecarDeepPLCWarmZeroAllocs(t *testing.T) {
	want := selectedNeuralNoSidecarSequence(t)
	for _, sampleRate := range []int{16000, 48000} {
		sequence := want.rates[2]
		if sampleRate == 48000 {
			sequence = want.rates[4]
		}
		for _, complexity := range []int{0, 5} {
			t.Run(fmt.Sprintf("rate_%d/complexity_%d", sampleRate, complexity), func(t *testing.T) {
				dec := newNeuralNoSidecarDecoder(t, want.model, sampleRate, complexity)
				pcm := make([]float32, sequence.frameSize)
				for range 3 {
					if !primeNeuralNoSidecarSequence(dec, want.packet, pcm, len(neuralNoSidecarSteps)) {
						t.Fatal("warm no-sidecar sequence failed")
					}
				}
				sequenceAllocs := testing.AllocsPerRun(20, func() {
					for step := range neuralNoSidecarSteps {
						if !decodeNeuralNoSidecarStep(dec, neuralNoSidecarPacketForStep(want.packet, step), pcm) {
							t.Error("measured no-sidecar sequence failed")
						}
					}
				})

				stepAllocs := [len(neuralNoSidecarSteps)]float64{}
				for stepIdx := range neuralNoSidecarSteps {
					stepDec := newNeuralNoSidecarDecoder(t, want.model, sampleRate, complexity)
					warmDec := newNeuralNoSidecarDecoder(t, want.model, sampleRate, complexity)
					stepPCM := make([]float32, sequence.frameSize)
					warmPCM := make([]float32, sequence.frameSize)
					for range 3 {
						if !primeNeuralNoSidecarSequence(stepDec, want.packet, stepPCM, len(neuralNoSidecarSteps)) ||
							!primeNeuralNoSidecarSequence(warmDec, want.packet, warmPCM, len(neuralNoSidecarSteps)) {
							t.Fatal("no-sidecar allocation probe warm sequence failed")
						}
					}
					if !primeNeuralNoSidecarSequence(stepDec, want.packet, stepPCM, stepIdx) ||
						!primeNeuralNoSidecarSequence(warmDec, want.packet, warmPCM, stepIdx) {
						t.Fatalf("%s allocation probe prefix failed", neuralNoSidecarSteps[stepIdx])
					}
					warmup := true
					failed := false
					stepAllocs[stepIdx] = testing.AllocsPerRun(1, func() {
						decoder, output := stepDec, stepPCM
						if warmup {
							decoder, output = warmDec, warmPCM
							warmup = false
						}
						packet := neuralNoSidecarPacketForStep(want.packet, stepIdx)
						if !decodeNeuralNoSidecarStep(decoder, packet, output) {
							failed = true
						}
					})
					if failed {
						t.Fatalf("%s no-sidecar allocation probe decode failed", neuralNoSidecarSteps[stepIdx])
					}
				}
				if sequenceAllocs != 0 || stepAllocs != [len(neuralNoSidecarSteps)]float64{} {
					t.Fatalf("warm no-sidecar allocations sequence=%g steps=%v; want 0", sequenceAllocs, stepAllocs)
				}
			})
		}
	}
}
