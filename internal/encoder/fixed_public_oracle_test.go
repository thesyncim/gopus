//go:build gopus_fixed_point

package encoder

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// probePublicFixedMixedRecords pairs public encoder calls with the C archive
// built for the active fixed-point feature set. These comparisons leave QEXT
// runtime disabled on both encoders.
func probePublicFixedMixedRecords(p libopustest.OpusEncodeFixedParams, frames []libopustest.OpusEncodeFixedMixedFrame) ([]libopustest.OpusEncodeFixedRecord, error) {
	if extsupport.QEXT {
		return libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(p, frames)
	}
	return libopustest.ProbeOpusEncodeFixedMixedRecords(p, frames)
}

// probePublicFixedCELTQ8 uses the same selected C feature set as the public
// fixed CELT encoder, even when its QEXT packet extension is runtime disabled.
func probePublicFixedCELTQ8(p libopustest.CELTFixedQ8Params) ([]libopustest.CELTFixedQ8Record, error) {
	if extsupport.QEXT {
		return libopustest.ProbeCELTFixedQEXTQ8(p)
	}
	return libopustest.ProbeCELTFixedRawQ8(p)
}
