package encoder

import internaldred "github.com/thesyncim/gopus/internal/dred"

func (e *Encoder) currentDREDActivity(pcm []float64) bool {
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
