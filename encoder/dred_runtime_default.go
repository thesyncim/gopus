//go:build !gopus_dred && !gopus_unsupported_controls
// +build !gopus_dred,!gopus_unsupported_controls

package encoder

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/types"
)

type dredEncoderExtras struct{}

type dredEmissionPlan struct {
	bitrate int
}

func (e *Encoder) SetDNNBlob(blob *dnnblob.Blob) {
	e.dnnBlob = blob
	e.dred = nil
}

func (e *Encoder) dredModelsLoaded() bool {
	return false
}

func (e *Encoder) resetDREDControls() {
	e.dred = nil
}

func (e *Encoder) clearDREDRuntime() {}

func (e *Encoder) processDREDLatents(_ []float64, _ int) int {
	return 0
}

func (e *Encoder) computeDREDEmissionPlan(_ int) (dredEmissionPlan, bool) {
	return dredEmissionPlan{}, false
}

func (e *Encoder) hybridDREDPrimaryBudget(_ int, _ int, _ dredEmissionPlan) int {
	return 0
}

func (e *Encoder) maybeBuildSingleFrameDREDPacket(_ []byte, _ Mode, _ types.Bandwidth, _ int, _ bool) ([]byte, bool, error) {
	return nil, false, nil
}

func (e *Encoder) maybeBuildMultiFrameDREDPacket(_ [][]byte, _ Mode, _ types.Bandwidth, _ int, _ int, _ bool, _ bool) ([]byte, bool, error) {
	return nil, false, nil
}
