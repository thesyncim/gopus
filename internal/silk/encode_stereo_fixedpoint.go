//go:build gopus_fixed_point

package silk

// encode_stereo_fixedpoint.go resets the integer side-channel encode state when
// stereo side coding resumes after mid-only frames.

// resetStereoSideFixedState clears the integer side-channel encode state when
// stereo side coding resumes after one or more mid-only frames, mirroring
// libopus enc_API.c (silk/enc_API.c lines 453-463): sShape (harm/tilt smoothers
// and LastGainIndex), sNSQ, prev_NLSFq_Q15, sLP.In_LP_State, prevLag,
// sNSQ.lagPrev, prevSignalType, sNSQ.prev_gain_Q16 and first_frame_after_reset.
// frameCounter and VAD state are preserved, as in libopus.
func (e *Encoder) resetStereoSideFixedState() {
	st := e.fixed
	if st == nil || !st.initialized {
		return
	}
	st.nsq = NSQState{}
	st.nsq.prevGainQ16 = 1 << 16
	st.nsq.lagPrev = 100
	st.prevNLSFqQ15 = [maxLPCOrder]int16{}
	st.prevLag = 100
	st.lastGainIndex = 10
	st.prevSignalType = typeNoVoiceActivity
	st.harmShapeGainSmthQ16 = 0
	st.tiltSmthQ16 = 0
	st.firstFrameAfterReset = true
}
