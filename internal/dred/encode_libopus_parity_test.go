//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package dred

import (
	"bytes"
	"testing"
)

func TestEncodeExperimentalPayloadMatchesLibopus(t *testing.T) {
	var state [MaxFrames * StateDim]float32
	var latents [MaxFrames * LatentDim]float32
	var activity [ActivityHistorySize]byte

	for i := range state {
		state[i] = 0.035 * float32((i%13)-6)
	}
	for i := range latents {
		latents[i] = 0.0275 * float32((i%11)-5)
	}
	for i := 0; i < 48; i++ {
		activity[i] = 1
	}

	const (
		q0          = 6
		dQ          = 3
		qmax        = 15
		maxChunks   = 4
		latentsFill = 6
		dredOffset  = 12
		latentOff   = 0
	)

	want, err := probeLibopusDREDEncodePayload(q0, dQ, qmax, maxChunks, MaxDataSize, latentsFill, dredOffset, latentOff, 0, state[:], latents[:], activity)
	if err != nil {
		t.Fatalf("probeLibopusDREDEncodePayload() error: %v", err)
	}

	var payload [MaxDataSize]byte
	lastExtra := 0
	n := EncodeExperimentalPayload(payload[:], maxChunks, q0, dQ, qmax, state[:], latents[:], latentsFill, dredOffset, latentOff, &lastExtra, activity[:])
	if n != ExperimentalHeaderBytes+len(want.Payload) {
		t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, ExperimentalHeaderBytes+len(want.Payload))
	}
	if !bytes.Equal(payload[ExperimentalHeaderBytes:n], want.Payload) {
		t.Fatalf("payload mismatch\ngot=%x\nwant=%x", payload[ExperimentalHeaderBytes:n], want.Payload)
	}
	if lastExtra != want.LastExtraDREDOffset {
		t.Fatalf("lastExtraDREDOffset=%d want %d", lastExtra, want.LastExtraDREDOffset)
	}
}

func TestEncodeExperimentalPayloadMatchesLibopusDelayedOffset(t *testing.T) {
	var state [MaxFrames * StateDim]float32
	var latents [MaxFrames * LatentDim]float32
	var activity [ActivityHistorySize]byte

	for i := range state {
		state[i] = 0.025 * float32((i%9)-4)
	}
	for i := range latents {
		latents[i] = 0.03125 * float32((i%7)-3)
	}
	for i := 0; i < 16; i++ {
		activity[i] = 1
	}
	for i := 16; i < 32; i++ {
		activity[i] = 0
	}
	for i := 32; i < 48; i++ {
		activity[i] = 1
	}

	const (
		q0          = 6
		dQ          = 5
		qmax        = 15
		maxChunks   = 4
		latentsFill = 8
		dredOffset  = 20
		latentOff   = 0
		lastExtraIn = 2
	)

	want, err := probeLibopusDREDEncodePayload(q0, dQ, qmax, maxChunks, MaxDataSize, latentsFill, dredOffset, latentOff, lastExtraIn, state[:], latents[:], activity)
	if err != nil {
		t.Fatalf("probeLibopusDREDEncodePayload() error: %v", err)
	}

	var payload [MaxDataSize]byte
	lastExtra := lastExtraIn
	n := EncodeExperimentalPayload(payload[:], maxChunks, q0, dQ, qmax, state[:], latents[:], latentsFill, dredOffset, latentOff, &lastExtra, activity[:])
	if n != ExperimentalHeaderBytes+len(want.Payload) {
		t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, ExperimentalHeaderBytes+len(want.Payload))
	}
	if !bytes.Equal(payload[ExperimentalHeaderBytes:n], want.Payload) {
		t.Fatalf("payload mismatch\ngot=%x\nwant=%x", payload[ExperimentalHeaderBytes:n], want.Payload)
	}
	if lastExtra != want.LastExtraDREDOffset {
		t.Fatalf("lastExtraDREDOffset=%d want %d", lastExtra, want.LastExtraDREDOffset)
	}
}

func TestEncodeExperimentalPayloadMatchesLibopusLargeLaplaceContinuation(t *testing.T) {
	var state [MaxFrames * StateDim]float32
	var latents [MaxFrames * LatentDim]float32
	var activity [ActivityHistorySize]byte

	// Exercises ec_laplace_encode_p0() values that use the final ICDF symbol as
	// a continuation marker. Clamping that symbol corrupts real DRED payloads.
	state[5] = 41.417286
	state[15] = 51.2
	latents[2] = -49.05866
	for i := 0; i < 16; i++ {
		activity[i] = 1
	}

	const (
		q0          = 6
		dQ          = 5
		qmax        = 15
		maxChunks   = 14
		maxBytes    = 46
		latentsFill = 2
		dredOffset  = 10
		latentOff   = 0
	)

	want, err := probeLibopusDREDEncodePayload(q0, dQ, qmax, maxChunks, maxBytes, latentsFill, dredOffset, latentOff, 0, state[:], latents[:], activity)
	if err != nil {
		t.Fatalf("probeLibopusDREDEncodePayload() error: %v", err)
	}

	var payload [MaxDataSize]byte
	lastExtra := 0
	n := EncodeExperimentalPayload(payload[:maxBytes+ExperimentalHeaderBytes], maxChunks, q0, dQ, qmax, state[:], latents[:], latentsFill, dredOffset, latentOff, &lastExtra, activity[:])
	if n != ExperimentalHeaderBytes+len(want.Payload) {
		t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, ExperimentalHeaderBytes+len(want.Payload))
	}
	if !bytes.Equal(payload[ExperimentalHeaderBytes:n], want.Payload) {
		t.Fatalf("payload mismatch\ngot=%x\nwant=%x", payload[ExperimentalHeaderBytes:n], want.Payload)
	}
	if lastExtra != want.LastExtraDREDOffset {
		t.Fatalf("lastExtraDREDOffset=%d want %d", lastExtra, want.LastExtraDREDOffset)
	}
}
