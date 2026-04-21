package lpcnetplc

// Constants mirrored from libopus 1.6.1 dnn/lpcnet headers.
const (
	NumFeatures = 20
	MaxFEC      = 104
)

// State mirrors the low-cost LPCNet PLC state that the libopus DRED recovery
// path mutates before audio concealment. This intentionally covers only the
// queue/blend lifecycle needed by the current pure-Go DRED parity path.
type State struct {
	fec        [MaxFEC][NumFeatures]float32
	fecReadPos int
	fecFillPos int
	fecSkip    int
	blend      int
}

// Reset clears the retained queue state and resets blend to the libopus
// post-update default.
func (s *State) Reset() {
	*s = State{}
}

// FECClear mirrors lpcnet_plc_fec_clear().
func (s *State) FECClear() {
	if s == nil {
		return
	}
	s.fecReadPos = 0
	s.fecFillPos = 0
	s.fecSkip = 0
}

// FECAdd mirrors lpcnet_plc_fec_add(). A nil features slice records a skipped
// positive feature offset.
func (s *State) FECAdd(features []float32) {
	if s == nil {
		return
	}
	if features == nil {
		s.fecSkip++
		return
	}
	if s.fecFillPos >= MaxFEC || len(features) < NumFeatures {
		return
	}
	copy(s.fec[s.fecFillPos][:], features[:NumFeatures])
	s.fecFillPos++
}

// MarkUpdated mirrors lpcnet_plc_update()'s blend reset.
func (s *State) MarkUpdated() {
	if s == nil {
		return
	}
	s.blend = 0
}

// MarkConcealed mirrors lpcnet_plc_conceal()'s post-blend state.
func (s *State) MarkConcealed() {
	if s == nil {
		return
	}
	s.blend = 1
}

// Blend reports the retained libopus LPCNet PLC blend flag.
func (s *State) Blend() int {
	if s == nil {
		return 0
	}
	return s.blend
}

// FECFillPos reports how many concrete feature vectors are queued.
func (s *State) FECFillPos() int {
	if s == nil {
		return 0
	}
	return s.fecFillPos
}

// FECSkip reports how many positive feature offsets were recorded as missing.
func (s *State) FECSkip() int {
	if s == nil {
		return 0
	}
	return s.fecSkip
}

// FillQueuedFeatures copies one queued feature vector into dst and returns the
// number of floats written.
func (s *State) FillQueuedFeatures(slot int, dst []float32) int {
	if s == nil || slot < 0 || slot >= s.fecFillPos {
		return 0
	}
	n := NumFeatures
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst[:n], s.fec[slot][:n])
	return n
}
