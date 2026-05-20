//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package gopus

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDREDControlModelBlobsMatchPinnedLibopusDigests(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name        string
		probe       func() ([]byte, error)
		validate    func(*dnnblob.Blob) error
		wantRecords int
		wantBytes   int
		wantSHA256  string
	}{
		{
			name:        "encoder",
			probe:       probeLibopusEncoderNeuralModelBlob,
			validate:    func(blob *dnnblob.Blob) error { return blob.ValidateEncoderControl() },
			wantRecords: 133,
			wantBytes:   317696,
			wantSHA256:  "164bfeab6f7d20932b006e9cdcaa1d26f2fd2f02d5e33cafee4fbb580be4c0b2",
		},
		{
			name:        "decoder",
			probe:       probeLibopusDecoderNeuralModelBlob,
			validate:    func(blob *dnnblob.Blob) error { return blob.ValidateDecoderControl(false) },
			wantRecords: 114,
			wantBytes:   1432768,
			wantSHA256:  "42d935199c9d86519ccd7be3bc8df0090553319d3e23b2e93cdfdf7a06c9ab95",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.probe()
			if err != nil {
				libopustest.HelperUnavailable(t, tc.name+" control model", err)
			}
			blob, err := dnnblob.Clone(raw)
			if err != nil {
				t.Fatalf("Clone(%s control model blob) error: %v", tc.name, err)
			}
			if err := tc.validate(blob); err != nil {
				t.Fatalf("Validate(%s control model blob) error: %v", tc.name, err)
			}
			if len(blob.Records) != tc.wantRecords {
				t.Fatalf("%s DRED control model records=%d want %d", tc.name, len(blob.Records), tc.wantRecords)
			}
			if len(raw) != tc.wantBytes {
				t.Fatalf("%s DRED control model bytes=%d want %d", tc.name, len(raw), tc.wantBytes)
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])
			if got != tc.wantSHA256 {
				t.Fatalf("%s DRED control model blob sha256=%s want %s", tc.name, got, tc.wantSHA256)
			}
		})
	}
}
