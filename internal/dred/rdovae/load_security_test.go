package rdovae

import (
	"encoding/binary"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func buildSparseIdxTestBlob(t *testing.T, layerName string, nbInputs, nbOutputs int, idxVals []int32) *dnnblob.Blob {
	t.Helper()
	var raw []byte
	appendRec := func(name string, typ int32, payload []byte) {
		hdr := make([]byte, 64)
		binary.LittleEndian.PutUint32(hdr[8:12], uint32(typ))
		binary.LittleEndian.PutUint32(hdr[12:16], uint32(len(payload)))
		binary.LittleEndian.PutUint32(hdr[16:20], uint32(len(payload)))
		copy(hdr[20:63], name)
		raw = append(raw, hdr...)
		raw = append(raw, payload...)
	}
	idxBytes := make([]byte, len(idxVals)*4)
	for i, v := range idxVals {
		binary.LittleEndian.PutUint32(idxBytes[i*4:], uint32(v))
	}
	appendRec(layerName+"_weights_idx", dnnblob.TypeInt, idxBytes)
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	return blob
}

func TestLoadSparseIndexRejectsNegativePosition(t *testing.T) {
	// 8 outputs => 1 group, 1 block with negative 4-byte-aligned pos = -4.
	blob := buildSparseIdxTestBlob(t, "test_layer", 64, 8, []int32{1, -4})
	if _, _, err := loadSparseIndexRecord(blob, "test_layer_weights_idx", 64, 8); err == nil {
		t.Fatal("loadSparseIndexRecord accepted negative sparse index pos=-4")
	}
}

func TestLoadSparseIndexRejectsExtraGroups(t *testing.T) {
	// 8 outputs => 1 group, but supply 2 groups.
	blob := buildSparseIdxTestBlob(t, "test_layer", 64, 8, []int32{1, 0, 1, 0})
	if _, _, err := loadSparseIndexRecord(blob, "test_layer_weights_idx", 64, 8); err == nil {
		t.Fatal("loadSparseIndexRecord accepted extra output groups")
	}
}
