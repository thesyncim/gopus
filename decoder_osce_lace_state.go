//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// decoderOSCELACEState carries decoder-side OSCE LACE/NoLACE runtime
// bookkeeping under the explicit quarantine build. The `osceLACEModel`
// field follows the same pattern as the OSCE BWE binding: it is non-nil
// once `SetDNNBlob` has successfully bound an OSCE LACE-capable weights
// blob.
//
// libopus keeps a single shared OSCEModel inside `OpusDecoder` (see
// `dnn/osce.c`: `osce_init`) carrying both LACE and NoLACE postfilter
// weights; the per-channel postfilter state (LACEState / NoLACEState)
// lives in the silk decoder structs. Phase 1 only wires the typed model
// pointer; the per-channel runtime state and forward pass arrive in
// Phase 2.
type decoderOSCELACEState struct {
	osceLACEModel *osceLACE.Model
}
