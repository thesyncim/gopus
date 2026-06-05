//go:build gopus_dred || gopus_osce

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
	lastExtra := int32(0)
	n := EncodeExperimentalPayload(payload[:], maxChunks, q0, dQ, qmax, state[:], latents[:], latentsFill, dredOffset, latentOff, &lastExtra, activity[:])
	if n != ExperimentalHeaderBytes+len(want.Payload) {
		t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, ExperimentalHeaderBytes+len(want.Payload))
	}
	if !bytes.Equal(payload[ExperimentalHeaderBytes:n], want.Payload) {
		t.Fatalf("payload mismatch\ngot=%x\nwant=%x", payload[ExperimentalHeaderBytes:n], want.Payload)
	}
	if int(lastExtra) != want.LastExtraDREDOffset {
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
	lastExtra := int32(lastExtraIn)
	n := EncodeExperimentalPayload(payload[:], maxChunks, q0, dQ, qmax, state[:], latents[:], latentsFill, dredOffset, latentOff, &lastExtra, activity[:])
	if n != ExperimentalHeaderBytes+len(want.Payload) {
		t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, ExperimentalHeaderBytes+len(want.Payload))
	}
	if !bytes.Equal(payload[ExperimentalHeaderBytes:n], want.Payload) {
		t.Fatalf("payload mismatch\ngot=%x\nwant=%x", payload[ExperimentalHeaderBytes:n], want.Payload)
	}
	if int(lastExtra) != want.LastExtraDREDOffset {
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
	lastExtra := int32(0)
	n := EncodeExperimentalPayload(payload[:maxBytes+ExperimentalHeaderBytes], maxChunks, q0, dQ, qmax, state[:], latents[:], latentsFill, dredOffset, latentOff, &lastExtra, activity[:])
	if n != ExperimentalHeaderBytes+len(want.Payload) {
		t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, ExperimentalHeaderBytes+len(want.Payload))
	}
	if !bytes.Equal(payload[ExperimentalHeaderBytes:n], want.Payload) {
		t.Fatalf("payload mismatch\ngot=%x\nwant=%x", payload[ExperimentalHeaderBytes:n], want.Payload)
	}
	if int(lastExtra) != want.LastExtraDREDOffset {
		t.Fatalf("lastExtraDREDOffset=%d want %d", lastExtra, want.LastExtraDREDOffset)
	}
}

func TestEncodeExperimentalPayloadMatchesLibopusMatrix(t *testing.T) {
	for _, tc := range []struct {
		name        string
		q0          int
		dQ          int
		qmax        int
		maxChunks   int
		maxBytes    int
		latentsFill int
		dredOffset  int
		latentOff   int
		lastExtraIn int
		activeStart int
		activeEnd   int
		seed        int
	}{
		{name: "qmax_limited", q0: 5, dQ: 2, qmax: 10, maxChunks: 5, maxBytes: MaxDataSize, latentsFill: 10, dredOffset: 14, activeStart: 0, activeEnd: 64, seed: 3},
		{name: "zero_delta", q0: 12, dQ: 0, qmax: 12, maxChunks: 4, maxBytes: MaxDataSize, latentsFill: 8, dredOffset: 16, activeStart: 0, activeEnd: 48, seed: 5},
		{name: "extended_offset", q0: 6, dQ: 3, qmax: 15, maxChunks: 6, maxBytes: MaxDataSize, latentsFill: 10, dredOffset: 0, activeStart: 24, activeEnd: 80, seed: 7},
		{name: "high_q0", q0: 14, dQ: 1, qmax: 15, maxChunks: 4, maxBytes: MaxDataSize, latentsFill: 8, dredOffset: 12, activeStart: 0, activeEnd: 64, seed: 11},
		{name: "max_bytes_suppressed", q0: 6, dQ: 5, qmax: 15, maxChunks: 8, maxBytes: 1, latentsFill: 8, dredOffset: 12, activeStart: 0, activeEnd: 64, seed: 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state [MaxFrames * StateDim]float32
			var latents [MaxFrames * LatentDim]float32
			var activity [ActivityHistorySize]byte
			fillDREDEncodeParityHistory(state[:], latents[:], tc.seed)
			for i := tc.activeStart; i < tc.activeEnd && i < len(activity); i++ {
				activity[i] = 1
			}

			want, err := probeLibopusDREDEncodePayload(tc.q0, tc.dQ, tc.qmax, tc.maxChunks, tc.maxBytes, tc.latentsFill, tc.dredOffset, tc.latentOff, tc.lastExtraIn, state[:], latents[:], activity)
			if err != nil {
				t.Fatalf("probeLibopusDREDEncodePayload() error: %v", err)
			}

			payload := make([]byte, tc.maxBytes+ExperimentalHeaderBytes)
			lastExtra := int32(tc.lastExtraIn)
			n := EncodeExperimentalPayload(payload, int32(tc.maxChunks), int32(tc.q0), int32(tc.dQ), int32(tc.qmax), state[:], latents[:], int32(tc.latentsFill), int32(tc.dredOffset), int32(tc.latentOff), &lastExtra, activity[:])
			if n != expectedExperimentalPayloadLength(len(want.Payload)) {
				t.Fatalf("EncodeExperimentalPayload()=%d want %d", n, expectedExperimentalPayloadLength(len(want.Payload)))
			}
			gotPayload := []byte(nil)
			if n > 0 {
				gotPayload = payload[ExperimentalHeaderBytes:n]
			}
			if !bytes.Equal(gotPayload, want.Payload) {
				t.Fatalf("payload mismatch\ngot=%x\nwant=%x", gotPayload, want.Payload)
			}
			if int(lastExtra) != want.LastExtraDREDOffset {
				t.Fatalf("lastExtraDREDOffset=%d want %d", lastExtra, want.LastExtraDREDOffset)
			}
		})
	}
}

func fillDREDEncodeParityHistory(state, latents []float32, seed int) {
	for i := range state {
		state[i] = 0.01953125 * float32(((i+seed*3)%17)-8)
	}
	for i := range latents {
		latents[i] = 0.021484375 * float32(((i*3+seed)%19)-9)
	}
}

func expectedExperimentalPayloadLength(payloadLen int) int {
	if payloadLen == 0 {
		return 0
	}
	return ExperimentalHeaderBytes + payloadLen
}
