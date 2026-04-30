//go:build gopus_unsupported_controls || gopus_dred
// +build gopus_unsupported_controls gopus_dred

package multistream

import (
	"encoding/binary"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func appendEncoderTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	return append(dst, out...)
}

func appendEncoderTestBlobRecordWithPayload(dst []byte, name string, typ int32, payload []byte) []byte {
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

func encodeInt32s(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
}

func appendLinearLayerSpecBlob(dst []byte, spec lpcnetplc.LinearLayerSpec) []byte {
	if spec.Bias != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Subias != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Scale != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Weights != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Weights, dnnblob.TypeInt8, spec.NbInputs*spec.NbOutputs)
		return dst
	}
	if spec.FloatWeights != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*spec.NbInputs*spec.NbOutputs)
	}
	return dst
}

func appendConv2DLayerSpecBlob(dst []byte, spec lpcnetplc.Conv2DLayerSpec) []byte {
	if spec.Bias != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.OutChannels)
	}
	if spec.FloatWeights != "" {
		size := 4 * spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
		dst = appendEncoderTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, size)
	}
	return dst
}

func appendRDOVAEEncoderLayerRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Subias != "" {
		dst = appendEncoderTestBlobRecord(dst, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = appendEncoderTestBlobRecordWithPayload(dst, spec.WeightsIdx, dnnblob.TypeInt, encodeInt32s(idx))
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendEncoderTestBlobRecord(dst, spec.Weights, dnnblob.TypeInt8, size)
		dst = appendEncoderTestBlobRecord(dst, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendEncoderTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*size)
	}
	return dst
}

func makeLoadableDREDEncoderTestBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	var raw []byte
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		raw = appendLinearLayerSpecBlob(raw, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		raw = appendConv2DLayerSpecBlob(raw, spec)
	}
	for _, spec := range rdovae.EncoderLayerSpecs() {
		raw = appendRDOVAEEncoderLayerRecords(raw, spec)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone(loadable): %v", err)
	}
	return blob
}
