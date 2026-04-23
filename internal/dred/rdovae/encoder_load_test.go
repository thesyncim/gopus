package rdovae

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestLoadEncoder(t *testing.T) {
	blob, err := dnnblob.Clone(buildEncoderTestBlob())
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	model, err := LoadEncoder(blob)
	if err != nil {
		t.Fatalf("LoadEncoder error: %v", err)
	}
	if model.Dense1.NbInputs != 40 || model.Dense1.NbOutputs != 64 {
		t.Fatalf("Dense1 dims=(%d,%d) want (40,64)", model.Dense1.NbInputs, model.Dense1.NbOutputs)
	}
	if model.GDense1.WeightsIdx.Len() == 0 || model.GDense1.Weights.Len() != SparseBlockSize*(128/8) {
		t.Fatalf("GDense1 sparse shapes idx:%d weights:%d", model.GDense1.WeightsIdx.Len(), model.GDense1.Weights.Len())
	}
	if model.GRUInput[0].Bias.Len() != 96 || model.GRUInput[0].Weights.Len() != SparseBlockSize*(96/8) || model.GRUInput[0].Scale.Len() != 96 {
		t.Fatalf("GRUInput[0] shapes = bias:%d weights:%d scale:%d", model.GRUInput[0].Bias.Len(), model.GRUInput[0].Weights.Len(), model.GRUInput[0].Scale.Len())
	}
}

func TestLoadEncoderRejectsWrongSparseShape(t *testing.T) {
	var raw []byte
	for _, spec := range EncoderLayerSpecs() {
		if spec.Name == "gdense1" {
			spec.WeightsIdx = ""
			raw = appendLinearLayerRecords(raw, spec)
			raw = append(raw, makeTestBlobRecord("gdense1_weights_idx", dnnblob.TypeInt, encodeInt32s([]int32{1, 1}))...)
			continue
		}
		raw = appendLinearLayerRecords(raw, spec)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if _, err := LoadEncoder(blob); err == nil {
		t.Fatal("LoadEncoder error=nil want non-nil")
	}
}

func buildEncoderTestBlob() []byte {
	var raw []byte
	for _, spec := range EncoderLayerSpecs() {
		raw = appendLinearLayerRecords(raw, spec)
	}
	return raw
}
