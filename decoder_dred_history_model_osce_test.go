//go:build gopus_osce

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func dredHistoryDecoderModelBlob(t *testing.T) []byte {
	t.Helper()
	core := requireLibopusDecoderNeuralModelBlob(t)
	osce := requireLibopusOSCELACEModelBlob(t)
	bwe := requireLibopusOSCEBWEModelBlob(t)
	merged := make([]byte, 0, len(core)+len(osce)+len(bwe))
	merged = append(merged, core...)
	merged = append(merged, osce...)
	merged = append(merged, bwe...)
	return merged
}

func prepareDREDHistoryOSCEModel(t *testing.T, dec *Decoder) {
	t.Helper()
	blob, err := dnnblob.Clone(requireLibopusOSCELACEModelBlob(t))
	if err != nil {
		t.Fatalf("clone OSCE model blob: %v", err)
	}
	if err := dec.bindOSCELACEModel(blob, blob.SupportsOSCE()); err != nil {
		t.Fatalf("bind OSCE model before core model load: %v", err)
	}
	dec.setOSCEModelState(blob.DecoderModels())
}
