package lpcnetplc

import (
	"encoding/binary"
	"sort"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

type analysisTestBlobSpec struct {
	typ  int32
	size int
}

func appendAnalysisTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
	header := make([]byte, 64)
	copy(header[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(header[4:8], 0)
	binary.LittleEndian.PutUint32(header[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(header[12:16], uint32(payloadSize))
	blockSize := ((payloadSize + 63) / 64) * 64
	binary.LittleEndian.PutUint32(header[16:20], uint32(blockSize))
	copy(header[20:], append([]byte(name), 0))
	out := append(dst, header...)
	out = append(out, make([]byte, blockSize)...)
	return out
}

func addAnalysisLinearLayerSpec(specs map[string]analysisTestBlobSpec, spec LinearLayerSpec) {
	if spec.Bias != "" {
		specs[spec.Bias] = analysisTestBlobSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Subias != "" {
		specs[spec.Subias] = analysisTestBlobSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Scale != "" {
		specs[spec.Scale] = analysisTestBlobSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Weights != "" {
		specs[spec.Weights] = analysisTestBlobSpec{typ: dnnblob.TypeInt8, size: spec.NbInputs * spec.NbOutputs}
	} else if spec.FloatWeights != "" {
		specs[spec.FloatWeights] = analysisTestBlobSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbInputs * spec.NbOutputs}
	}
}

func addAnalysisConv2DLayerSpec(specs map[string]analysisTestBlobSpec, spec Conv2DLayerSpec) {
	if spec.Bias != "" {
		specs[spec.Bias] = analysisTestBlobSpec{typ: dnnblob.TypeFloat, size: 4 * spec.OutChannels}
	}
	if spec.FloatWeights != "" {
		size := 4 * spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
		specs[spec.FloatWeights] = analysisTestBlobSpec{typ: dnnblob.TypeFloat, size: size}
	}
}

func makePitchDNNTestBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	specs := make(map[string]analysisTestBlobSpec)
	for _, spec := range PitchDNNLinearLayerSpecs() {
		addAnalysisLinearLayerSpec(specs, spec)
	}
	for _, spec := range PitchDNNConv2DLayerSpecs() {
		addAnalysisConv2DLayerSpec(specs, spec)
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	var raw []byte
	for _, name := range names {
		spec := specs[name]
		raw = appendAnalysisTestBlobRecord(raw, name, spec.typ, spec.size)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(pitchdnn test blob) error: %v", err)
	}
	return blob
}

func TestPitchDNNDoesNotAllocate(t *testing.T) {
	blob := makePitchDNNTestBlob(t)
	var pitch PitchDNN
	if err := pitch.SetModel(blob); err != nil {
		t.Fatalf("PitchDNN.SetModel error: %v", err)
	}
	var ifFeatures [pitchIFFeatures]float32
	var xcorr [pitchXcorrFeatures]float32
	for i := range ifFeatures {
		ifFeatures[i] = float32((i%17)-8) / 9
	}
	for i := range xcorr {
		xcorr[i] = float32((i%19)-9) / 11
	}
	allocs := testing.AllocsPerRun(100, func() {
		local := pitch
		_ = local.Compute(ifFeatures[:], xcorr[:])
	})
	if allocs != 0 {
		t.Fatalf("PitchDNN.Compute allocs=%v want 0", allocs)
	}
}

func TestAnalysisComputeSingleFrameFeaturesFloatDoesNotAllocate(t *testing.T) {
	blob := makePitchDNNTestBlob(t)
	var analysis Analysis
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("Analysis.SetModel error: %v", err)
	}
	var frame [FrameSize]float32
	var features [NumTotalFeatures]float32
	for i := range frame {
		frame[i] = float32((i%23)-11) / 13
	}
	allocs := testing.AllocsPerRun(100, func() {
		local := analysis
		if n := local.ComputeSingleFrameFeaturesFloat(features[:], frame[:]); n != NumTotalFeatures {
			t.Fatalf("ComputeSingleFrameFeaturesFloat()=%d want %d", n, NumTotalFeatures)
		}
	})
	if allocs != 0 {
		t.Fatalf("Analysis.ComputeSingleFrameFeaturesFloat allocs=%v want 0", allocs)
	}
}

func TestAnalysisBurgCepstralAnalysisDoesNotAllocate(t *testing.T) {
	var analysis Analysis
	var frame [FrameSize]float32
	var ceps [2 * NumBands]float32
	for i := range frame {
		frame[i] = float32((i%27)-13) / 15
	}
	allocs := testing.AllocsPerRun(100, func() {
		local := analysis
		if n := local.BurgCepstralAnalysis(ceps[:], frame[:]); n != 2*NumBands {
			t.Fatalf("BurgCepstralAnalysis()=%d want %d", n, 2*NumBands)
		}
	})
	if allocs != 0 {
		t.Fatalf("Analysis.BurgCepstralAnalysis allocs=%v want 0", allocs)
	}
}
