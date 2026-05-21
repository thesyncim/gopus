//go:build gopus_dred || gopus_extra_controls

package dred

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDREDRDOVAEModelBlobsMatchPinnedLibopusDigests(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name        string
		probe       func() ([]byte, error)
		supports    func(*dnnblob.Blob) bool
		wantRecords int
		wantBytes   int
		wantSHA256  string
	}{
		{
			name:        "encoder",
			probe:       probeLibopusDREDEncoderModelBlob,
			supports:    func(blob *dnnblob.Blob) bool { return blob.SupportsDREDEncoder() },
			wantRecords: 105,
			wantBytes:   241152,
			wantSHA256:  "43658976486611a9570a2da4e11c9f57e47be7311b3bdd00e6c6a9deca41ff42",
		},
		{
			name:        "decoder",
			probe:       probeLibopusDREDDecoderModelBlob,
			supports:    func(blob *dnnblob.Blob) bool { return blob.ValidateDREDDecoderControl() == nil },
			wantRecords: 124,
			wantBytes:   324288,
			wantSHA256:  "757cbffc527776ac2b506fb747fec2caeb3997f4627dd490ea9375fc5cdba9ff",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.probe()
			if err != nil {
				libopustest.HelperUnavailable(t, "dred "+tc.name+" model blob", err)
			}
			blob, err := dnnblob.Clone(raw)
			if err != nil {
				t.Fatalf("Clone(%s model blob) error: %v", tc.name, err)
			}
			if !tc.supports(blob) {
				t.Fatalf("%s DRED RDOVAE model blob did not expose the required records", tc.name)
			}
			if len(blob.Records) != tc.wantRecords {
				t.Fatalf("%s DRED RDOVAE model records=%d want %d", tc.name, len(blob.Records), tc.wantRecords)
			}
			if len(raw) != tc.wantBytes {
				t.Fatalf("%s DRED RDOVAE model bytes=%d want %d", tc.name, len(raw), tc.wantBytes)
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])
			if got != tc.wantSHA256 {
				t.Fatalf("%s DRED RDOVAE model blob sha256=%s want %s", tc.name, got, tc.wantSHA256)
			}
		})
	}
}
