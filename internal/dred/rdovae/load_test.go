package rdovae

import (
	"encoding/binary"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestLoadDecoder(t *testing.T) {
	blob, err := dnnblob.Clone(buildDecoderTestBlob())
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	model, err := LoadDecoder(blob)
	if err != nil {
		t.Fatalf("LoadDecoder error: %v", err)
	}
	if model.Dense1.NbInputs != 26 || model.Dense1.NbOutputs != 96 {
		t.Fatalf("Dense1 dims=(%d,%d) want (26,96)", model.Dense1.NbInputs, model.Dense1.NbOutputs)
	}
	if model.GLU[0].Bias.Len() != 64 || model.GLU[0].Weights.Len() != 64*64 || model.GLU[0].Scale.Len() != 64 {
		t.Fatalf("GLU[0] shapes = bias:%d weights:%d scale:%d", model.GLU[0].Bias.Len(), model.GLU[0].Weights.Len(), model.GLU[0].Scale.Len())
	}
	if model.Output.WeightsIdx.Len() == 0 || model.Output.Weights.Len() != SparseBlockSize*(80/8) {
		t.Fatalf("Output sparse shapes idx:%d weights:%d", model.Output.WeightsIdx.Len(), model.Output.Weights.Len())
	}
}

func TestLoadDecoderRejectsWrongSparseShape(t *testing.T) {
	var raw []byte
	for _, spec := range DecoderLayerSpecs() {
		if spec.Name == "dec_output" {
			spec.WeightsIdx = ""
			raw = appendLinearLayerRecords(raw, spec)
			raw = append(raw, makeTestBlobRecord("dec_output_weights_idx", dnnblob.TypeInt, encodeInt32s([]int32{1, 1}))...)
			continue
		}
		raw = appendLinearLayerRecords(raw, spec)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if _, err := LoadDecoder(blob); err == nil {
		t.Fatal("LoadDecoder error=nil want non-nil")
	}
}

func buildDecoderTestBlob() []byte {
	var raw []byte
	for _, spec := range DecoderLayerSpecs() {
		raw = appendLinearLayerRecords(raw, spec)
	}
	return raw
}

func appendLinearLayerRecords(dst []byte, spec LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = append(dst, makeTestBlobRecord(spec.Bias, dnnblob.TypeFloat, encodeFloat32s(spec.NbOutputs))...)
	}
	if spec.Subias != "" {
		dst = append(dst, makeTestBlobRecord(spec.Subias, dnnblob.TypeFloat, encodeFloat32s(spec.NbOutputs))...)
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = append(dst, makeTestBlobRecord(spec.WeightsIdx, dnnblob.TypeInt, encodeInt32s(idx))...)
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = SparseBlockSize * totalBlocks
		}
		dst = append(dst, makeTestBlobRecord(spec.Weights, dnnblob.TypeInt8, make([]byte, size))...)
		dst = append(dst, makeTestBlobRecord(spec.Scale, dnnblob.TypeFloat, encodeFloat32s(spec.NbOutputs))...)
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = SparseBlockSize * totalBlocks
		}
		dst = append(dst, makeTestBlobRecord(spec.FloatWeights, dnnblob.TypeFloat, encodeFloat32s(size))...)
	}
	return dst
}

func makeTestBlobRecord(name string, typ int32, payload []byte) []byte {
	const headerSize = 64
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[4:8], 0)
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	copy(out[headerSize:], payload)
	return out
}

func encodeFloat32s(n int) []byte {
	return make([]byte, 4*n)
}

func encodeInt32s(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}
