package bwe

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// State carries the persistent BBWENet runtime state libopus keeps inside
// `BBWENetState` (dnn/bbwenet.h). Phase 1 only retains the model binding;
// Phase 2 will populate the conv/gru/tdshape working buffers below from the
// libopus reference implementation.
type State struct {
	model *Model
}

// SetModel binds (or clears) the BBWENet model on the runtime state. Passing
// a nil blob clears the binding so subsequent Loaded() calls return false.
func (s *State) SetModel(blob *dnnblob.Blob) error {
	if s == nil {
		return errInvalidBWEModel
	}
	if blob == nil {
		s.model = nil
		return nil
	}
	model, err := LoadModel(blob)
	if err != nil {
		s.model = nil
		return err
	}
	s.model = model
	return nil
}

// Model returns the bound BBWENet model, or nil when the runtime has not yet
// been loaded with a valid weights blob.
func (s *State) Model() *Model {
	if s == nil {
		return nil
	}
	return s.model
}

// Loaded reports whether the BBWENet runtime has a valid model binding.
func (s *State) Loaded() bool {
	return s != nil && s.model != nil
}

// Reset clears any per-stream working state. The model binding survives so
// libopus-style reset semantics are preserved (USE_WEIGHTS_FILE lifetime).
func (s *State) Reset() {
	// Phase 1: nothing to clear. Phase 2 will zero the conv/gru/tdshape
	// scratch state here.
}

// errBWERuntimeNotImplemented is returned by Process while the BBWENet
// forward pass remains a Phase 1 stub.
var errBWERuntimeNotImplemented = errors.New("osce/bwe: forward pass not implemented")

// Process runs the BBWENet upsampler from a 16 kHz lowband input into a
// 32 kHz wideband output stored in place of the input. Phase 1 leaves this as
// a no-op that reports `errBWERuntimeNotImplemented` so callers can detect the
// missing runtime; Phase 2 will replace this with the libopus
// `osce_bwe_process_frame`-equivalent pipeline.
func (s *State) Process(samples []float32) error {
	if s == nil || s.model == nil {
		return errBWERuntimeNotImplemented
	}
	_ = samples
	return errBWERuntimeNotImplemented
}
