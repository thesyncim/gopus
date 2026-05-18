//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/extsupport"
)

func (e *Encoder) currentDREDActivity(pcm []float64) bool {
	if !extsupport.DREDRuntime {
		return false
	}
	if len(pcm) == 0 {
		return true
	}
	if isDigitalSilence(pcm, e.lsbDepth) {
		return false
	}
	if e.lastAnalysisValid {
		active := e.lastAnalysisInfo.VADProb >= dtxActivityThreshold
		if !active {
			frameEnergy := computeFrameEnergy(pcm)
			peak := 0.0
			if e.dtx != nil {
				peak = e.dtx.peakSignalEnergy
			}
			active = peak < pseudoSNRThreshold*frameEnergy
		}
		return active
	}
	frameEnergy := computeFrameEnergy(pcm)
	peak := 0.0
	if e.dtx != nil {
		peak = e.dtx.peakSignalEnergy
	}
	return peak < pseudoSNRThreshold*0.5*frameEnergy
}

func (e *Encoder) buildDREDExperimentalPayload(dst []byte, maxChunks, q0, dQ, qmax int) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil || runtime.latentsFill <= 0 {
		return 0
	}
	return internaldred.EncodeExperimentalPayload(
		dst,
		maxChunks,
		q0,
		dQ,
		qmax,
		runtime.stateBuffer[:],
		runtime.latentsBuffer[:],
		runtime.latentsFill,
		runtime.dredOffset,
		runtime.latentOffset,
		&runtime.lastExtraDREDOffset,
		runtime.activity[:],
	)
}

func (e *Encoder) buildDREDExperimentalPayloadForPacket(dst []byte, maxChunks, q0, dQ, qmax int) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil {
		return 0
	}
	snapshot := &runtime.packetSnapshot
	if !snapshot.valid {
		return e.buildDREDExperimentalPayload(dst, maxChunks, q0, dQ, qmax)
	}
	lastExtra := snapshot.lastExtraDREDOffset
	n := internaldred.EncodeExperimentalPayload(
		dst,
		maxChunks,
		q0,
		dQ,
		qmax,
		snapshot.stateBuffer[:],
		snapshot.latentsBuffer[:],
		snapshot.latentsFill,
		snapshot.dredOffset,
		snapshot.latentOffset,
		&lastExtra,
		snapshot.activity[:],
	)
	runtime.lastExtraDREDOffset = lastExtra
	return n
}
