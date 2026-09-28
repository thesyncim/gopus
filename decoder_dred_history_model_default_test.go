//go:build gopus_dred && !gopus_osce

package gopus

import "testing"

func dredHistoryDecoderModelBlob(t *testing.T) []byte {
	t.Helper()
	return requireLibopusDecoderNeuralModelBlob(t)
}

func prepareDREDHistoryOSCEModel(_ *testing.T, _ *Decoder) {}
