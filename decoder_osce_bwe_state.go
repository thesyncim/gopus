//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// decoderOSCEBWEState carries decoder-side OSCE BWE runtime bookkeeping under
// the explicit quarantine build. The `osceBWEModel` field follows the same
// pattern as the FARGAN / Predictor bindings: it is non-nil once
// `SetDNNBlob` has successfully bound an OSCE BWE-capable weights blob.
type decoderOSCEBWEState struct {
	osceBWEModel *osceBWE.Model
	osceBWERuntime osceBWE.State
}
