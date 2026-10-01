//go:build (gopus_dred || gopus_osce) && !gopus_fixed_point

package multistream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const msNeuralNoSidecarFrameSize = 320

type msNeuralNoSidecarStep struct {
	ret         int
	finalRange  uint32
	blend       int
	analysisPos int
	predictPos  int
	pcmBits     []uint32
}

type msNeuralNoSidecarSequence struct {
	model  []byte
	packet []byte
	steps  [2][6]msNeuralNoSidecarStep
}

var (
	msNeuralNoSidecarHelper libopustest.HelperCache
	msNeuralPitchModel      libopustest.HelperCache
	msNeuralPLCModel        libopustest.HelperCache
	msNeuralFARGANModel     libopustest.HelperCache
)

func msNeuralModelBlobForTest(t *testing.T) []byte {
	t.Helper()
	var blob []byte
	for _, item := range []struct {
		cache      *libopustest.HelperCache
		sourceFile string
		outputBase string
	}{
		{&msNeuralPitchModel, "libopus_pitchdnn_model_blob.c", "gopus_ms_pitchdnn_model_blob"},
		{&msNeuralPLCModel, "libopus_plc_model_blob.c", "gopus_ms_plc_model_blob"},
		{&msNeuralFARGANModel, "libopus_fargan_model_blob.c", "gopus_ms_fargan_model_blob"},
	} {
		path, err := item.cache.Path(func() (string, error) {
			return libopustest.BuildDREDHelper("", item.sourceFile, item.outputBase, true)
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "multistream neural model blob", err)
		}
		part, err := libopustest.RunHelper(path, nil)
		if err != nil {
			t.Fatalf("read selected neural model %s: %v", item.sourceFile, err)
		}
		blob = append(blob, part...)
	}
	return blob
}

func msRawSILKHistoryWasReset(d *Decoder) bool {
	for i := range d.rawSILKHistory {
		if d.rawSILKHistoryPos[i] != 0 || d.rawSILKHistoryFill[i] != 0 || d.pcmHistorySynced[i] || d.directRawCapture[i] {
			return false
		}
		for _, sample := range d.rawSILKHistory[i] {
			if sample != 0 {
				return false
			}
		}
	}
	return true
}

func selectedMSNeuralNoSidecarSequence(t *testing.T) msNeuralNoSidecarSequence {
	t.Helper()
	libopustest.RequireOracle(t)
	bin, err := msNeuralNoSidecarHelper.Path(func() (string, error) {
		return buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			Label:       "multistream main-model neural PLC without DRED sidecar",
			SourceFile:  "libopus_multistream_neural_nosidecar_sequence.c",
			OutputBase:  "gopus_libopus_multistream_neural_nosidecar_sequence",
			RefIncludes: []string{".", "celt", "silk", "src"},
			Libs:        []string{"-lm"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream neural PLC sequence", err)
	}
	model := msNeuralModelBlobForTest(t)
	request := make([]byte, 4+len(model))
	binary.LittleEndian.PutUint32(request[:4], uint32(len(model)))
	copy(request[4:], model)
	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewReader(request)
	wire, err := cmd.Output()
	if err != nil {
		t.Fatalf("run selected C multistream sequence helper: %v", err)
	}
	r := bytes.NewReader(wire)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "MNSQ" {
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
	arch, features, modelSource := readU32(), readU32(), readU32()
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
	if features != wantFeatures || (modelSource != 1 && modelSource != 2) {
		t.Fatalf("C feature/model identity=%03b/%d Go feature identity=%03b", features, modelSource, wantFeatures)
	}
	t.Logf("C opus_select_arch=%d features=%03b model_source=%d helper=%s", arch, features, modelSource, bin)
	packetLen := int(readU32())
	if packetLen <= 0 || packetLen > 1275 {
		t.Fatalf("C packet length=%d", packetLen)
	}
	result := msNeuralNoSidecarSequence{model: model, packet: make([]byte, packetLen)}
	if _, err := io.ReadFull(r, result.packet); err != nil {
		t.Fatal(err)
	}
	toc := parseStreamTOC(result.packet[0])
	if toc.mode != streamModeSILK || toc.bandwidth != 2 {
		t.Fatalf("C packet TOC=%+v, want SILK WB", toc)
	}
	for complexityIdx, complexity := range []int{0, 5} {
		if got := readU32(); int(got) != complexity {
			t.Fatalf("C complexity=%d want %d", got, complexity)
		}
		loaded := readU32()
		wantLoaded := uint32(1)
		if modelSource == 1 {
			wantLoaded = 0
		}
		if loaded != wantLoaded {
			t.Fatalf("C main PLC model loaded=%d at complexity=%d, want initial loaded=%d", loaded, complexity, wantLoaded)
		}
		for stepIdx := range 6 {
			step := &result.steps[complexityIdx][stepIdx]
			step.ret = int(int32(readU32()))
			step.finalRange = readU32()
			step.blend = int(readU32())
			step.analysisPos = int(readU32())
			step.predictPos = int(readU32())
			if step.ret != msNeuralNoSidecarFrameSize {
				t.Fatalf("C complexity=%d step=%d returned %d samples", complexity, stepIdx, step.ret)
			}
			step.pcmBits = make([]uint32, step.ret)
			for i := range step.pcmBits {
				step.pcmBits[i] = readU32()
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("C protocol trailing bytes=%d", r.Len())
	}
	return result
}

func TestMultistreamMainModelNeuralPLCWithoutDREDMatchesLibopus(t *testing.T) {
	want := selectedMSNeuralNoSidecarSequence(t)
	for complexityIdx, complexity := range []int{0, 5} {
		t.Run(fmt.Sprintf("complexity_%d", complexity), func(t *testing.T) {
			dec, err := NewDecoder(16000, 1, 1, 0, []byte{0})
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetComplexity(complexity); err != nil {
				t.Fatal(err)
			}
			blob, err := dnnblob.Clone(want.model)
			if err != nil {
				t.Fatalf("clone selected neural model: %v", err)
			}
			for stepIdx := 0; stepIdx < 6; stepIdx++ {
				switch stepIdx {
				case 1:
					// The initial good frame updates libopus's raw LPCNet history
					// before the external model is installed. Loading the model
					// preserves that history for the following loss.
					dec.SetDNNBlob(blob)
				case 2:
					// Rebinding the same model keeps the live LPCNet runtime state,
					// matching libopus's model-load control path.
					dec.SetDNNBlob(blob)
					if complexity == 0 {
						// First let classical PLC update the raw history, then enable
						// deep PLC for this loss.
						if err := dec.SetComplexity(5); err != nil {
							t.Fatal(err)
						}
					}
				}
				packet := want.packet
				if stepIdx == 1 ||
					(complexity == 0 && (stepIdx == 2 || stepIdx == 5)) ||
					(complexity == 5 && stepIdx == 4) {
					packet = nil
				}
				got, err := dec.Decode(packet, msNeuralNoSidecarFrameSize)
				if err != nil {
					t.Fatalf("step %d: %v", stepIdx, err)
				}
				ref := want.steps[complexityIdx][stepIdx]
				if len(got) != ref.ret || dec.FinalRange() != ref.finalRange {
					t.Fatalf("step %d returned/range=%d/%08x C=%d/%08x", stepIdx, len(got), dec.FinalRange(), ref.ret, ref.finalRange)
				}
				for i, sample := range got {
					if bits := math.Float32bits(sample); bits != ref.pcmBits[i] {
						t.Fatalf("step %d PCM[%d]=%08x C=%08x complexity=%d C PLC blend/analysis/predict=%d/%d/%d", stepIdx, i, bits, ref.pcmBits[i], complexity, ref.blend, ref.analysisPos, ref.predictPos)
					}
				}
				if dec.dred != nil && len(dec.dred.dredCache) != 0 {
					t.Fatalf("step %d activated multistream DRED payload sidecar without a payload", stepIdx)
				}
				if dec.dred != nil {
					for stream, recovery := range dec.dred.dredRecovery {
						if recovery != 0 {
							t.Fatalf("step %d advanced DRED sidecar cursor for main-model-only stream %d: %d want 0", stepIdx, stream, recovery)
						}
					}
				}
				if stepIdx >= 1 {
					if dec.dred == nil || len(dec.dred.dredPLC) != 1 {
						t.Fatalf("step %d missing main-model PLC runtime", stepIdx)
					}
					state := dec.dred.dredPLC[0].Snapshot()
					if state.Blend != ref.blend || state.AnalysisPos != ref.analysisPos || state.PredictPos != ref.predictPos {
						t.Fatalf("step %d PLC blend/analysis/predict=%d/%d/%d C=%d/%d/%d", stepIdx, state.Blend, state.AnalysisPos, state.PredictPos, ref.blend, ref.analysisPos, ref.predictPos)
					}
				}
			}
			dec.Reset()
			if err := dec.SetComplexity(complexity); err != nil {
				t.Fatal(err)
			}
			if !msRawSILKHistoryWasReset(dec) {
				t.Fatal("Reset retained raw SILK history or a live direct-capture flag")
			}
			if complexity == 5 && dec.dred != nil {
				if state := dec.dred.dredPLC[0].Snapshot(); state.Blend != 0 || state.AnalysisPos != 15*160 || state.PredictPos != 15*160 {
					t.Fatalf("Reset PLC blend/analysis/predict=%d/%d/%d want 0/%d/%d", state.Blend, state.AnalysisPos, state.PredictPos, 15*160, 15*160)
				}
			}
			for stepIdx := 0; stepIdx < 2; stepIdx++ {
				packet := want.packet
				if stepIdx == 1 {
					packet = nil
				}
				got, err := dec.Decode(packet, msNeuralNoSidecarFrameSize)
				if err != nil {
					t.Fatalf("post-reset step %d: %v", stepIdx, err)
				}
				ref := want.steps[complexityIdx][stepIdx]
				if len(got) != ref.ret || dec.FinalRange() != ref.finalRange {
					t.Fatalf("post-reset step %d returned/range=%d/%08x C=%d/%08x", stepIdx, len(got), dec.FinalRange(), ref.ret, ref.finalRange)
				}
				for i, sample := range got {
					if bits := math.Float32bits(sample); bits != ref.pcmBits[i] {
						t.Fatalf("post-reset step %d PCM[%d]=%08x C=%08x complexity=%d", stepIdx, i, bits, ref.pcmBits[i], complexity)
					}
				}
				if dec.dred != nil {
					for stream, recovery := range dec.dred.dredRecovery {
						if recovery != 0 {
							t.Fatalf("post-reset step %d advanced DRED sidecar cursor for main-model-only stream %d: %d want 0", stepIdx, stream, recovery)
						}
					}
				}
			}
		})
	}
}

func TestMultistreamMainModelNeuralPLCWarmZeroAllocs(t *testing.T) {
	want := selectedMSNeuralNoSidecarSequence(t)
	dec, err := NewDecoder(16000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetComplexity(5); err != nil {
		t.Fatal(err)
	}
	blob, err := dnnblob.Clone(want.model)
	if err != nil {
		t.Fatal(err)
	}
	dec.SetDNNBlob(blob)
	output := make([]float32, msNeuralNoSidecarFrameSize)
	sequence := func() bool {
		for step := 0; step < 6; step++ {
			packet := want.packet
			if step == 1 || step == 4 {
				packet = nil
			}
			if _, err := dec.DecodeIntoFloat32(packet, output, msNeuralNoSidecarFrameSize); err != nil {
				return false
			}
		}
		return true
	}
	for range 3 {
		if !sequence() {
			t.Fatal("warm main-model neural PLC sequence failed")
		}
	}
	failed := false
	allocs := testing.AllocsPerRun(20, func() {
		if !sequence() {
			failed = true
		}
	})
	if failed {
		t.Fatal("main-model neural PLC sequence failed during allocation measurement")
	}
	if allocs != 0 {
		t.Fatalf("warm main-model neural PLC DecodeIntoFloat32 allocs/op=%.2f, want 0", allocs)
	}
}
