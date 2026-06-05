package dred

import (
	"math"
	"reflect"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnmath"
)

func TestUpdateActivityHistory(t *testing.T) {
	var history [ActivityHistorySize]byte
	for i := range len(history) {
		history[i] = byte(i % 2)
	}

	UpdateActivityHistory(&history, 480, 48000, true)

	wantPrefix := []byte{1, 1, 1, 1}
	if got := history[:4]; !reflect.DeepEqual(got, wantPrefix) {
		t.Fatalf("history prefix=%v want %v", got, wantPrefix)
	}
	if history[4] != 0 || history[5] != 1 {
		t.Fatalf("history rollover mismatch at [4:6]=%v want [0 1]", history[4:6])
	}
}

func TestEncodeExperimentalPayloadHasExpectedHeader(t *testing.T) {
	var state [MaxFrames * StateDim]float32
	var latents [MaxFrames * LatentDim]float32
	var activity [ActivityHistorySize]byte

	for i := range StateDim {
		state[i] = 0.03 * float32((i%7)-3)
	}
	for i := range 4 * LatentDim {
		latents[i] = 0.04 * float32((i%9)-4)
	}
	for i := range 16 {
		activity[i] = 1
	}

	var payload [MaxDataSize]byte
	lastExtra := int32(0)
	n := EncodeExperimentalPayload(payload[:], 2, 6, 3, 15, state[:], latents[:], 4, 12, 0, &lastExtra, activity[:])
	if n <= ExperimentalHeaderBytes {
		t.Fatalf("EncodeExperimentalPayload()=%d want > %d", n, ExperimentalHeaderBytes)
	}
	if payload[0] != 'D' || payload[1] != ExperimentalVersion {
		t.Fatalf("experimental prefix=%q,%d want D,%d", payload[0], payload[1], ExperimentalVersion)
	}

	parsed, err := ParsePayload(payload[ExperimentalHeaderBytes:n], 0)
	if err != nil {
		t.Fatalf("ParsePayload() error: %v", err)
	}
	if parsed.Header.Q0 != 6 || parsed.Header.DQ != 3 || parsed.Header.QMax != 15 {
		t.Fatalf("parsed header=(q0=%d dq=%d qmax=%d) want (6 3 15)", parsed.Header.Q0, parsed.Header.DQ, parsed.Header.QMax)
	}
	if parsed.PayloadLatents == 0 {
		t.Fatal("EncodeExperimentalPayload() produced no decodable latent chunks")
	}
}

func TestQuantizeDREDLatentsUsesLibopusVectorTail(t *testing.T) {
	x := float32(-0.75)
	scale := uint8(255)
	dzone := uint8(255)
	if runtime.GOARCH == "arm64" {
		found := false
		for step := -20000; step <= 20000; step++ {
			candidate := float32(step) * (1.0 / 1024.0)
			vectorQ := quantizeDREDLatentWithVectorTanh(candidate, scale, dzone)
			scalarQ := quantizeDREDLatentWithScalarTanh(candidate, scale, dzone)
			if vectorQ != scalarQ {
				x = candidate
				found = true
				break
			}
		}
		if !found {
			t.Fatal("test case no longer distinguishes vector tail activation from scalar activation")
		}
	}

	var scratch dredLatentEncodeScratch
	q := []int32{0}
	quantizeDREDLatents(q, []float32{x}, []uint8{scale}, []uint8{dzone}, []uint8{1}, []uint8{1}, &scratch)
	if want := int32(quantizeDREDLatentWithVectorTanh(x, scale, dzone)); q[0] != want {
		t.Fatalf("quantized latent=%d want vector-tail %d", q[0], want)
	}
}

func quantizeDREDLatentWithVectorTanh(x float32, scale, dzone uint8) int {
	delta := float32(dzone) * (1.0 / 256.0)
	xq := x * float32(scale) * (1.0 / 256.0)
	in := []float32{xq / (delta + 0.1)}
	out := []float32{0}
	dnnmath.TanhVectorApprox(out, in, len(in))
	xq -= delta * out[0]
	return int(math.Floor(float64(float32(0.5) + xq)))
}

func quantizeDREDLatentWithScalarTanh(x float32, scale, dzone uint8) int {
	delta := float32(dzone) * (1.0 / 256.0)
	xq := x * float32(scale) * (1.0 / 256.0)
	xq -= delta * dnnmath.TanhApprox(xq/(delta+0.1))
	return int(math.Floor(float64(float32(0.5) + xq)))
}

func TestEncodeExperimentalPayloadDoesNotAllocate(t *testing.T) {
	var state [MaxFrames * StateDim]float32
	var latents [MaxFrames * LatentDim]float32
	var activity [ActivityHistorySize]byte
	var payload [MaxDataSize]byte
	lastExtra := int32(0)

	for i := range StateDim {
		state[i] = 0.02 * float32((i%5)-2)
	}
	for i := range 4 * LatentDim {
		latents[i] = 0.01 * float32((i%11)-5)
	}
	for i := range 24 {
		activity[i] = 1
	}

	if n := EncodeExperimentalPayload(payload[:], 2, 6, 3, 15, state[:], latents[:], 4, 12, 0, &lastExtra, activity[:]); n == 0 {
		t.Fatal("warm EncodeExperimentalPayload() returned 0")
	}

	lastExtra = 0
	allocs := testing.AllocsPerRun(1000, func() {
		if n := EncodeExperimentalPayload(payload[:], 2, 6, 3, 15, state[:], latents[:], 4, 12, 0, &lastExtra, activity[:]); n == 0 {
			t.Fatal("EncodeExperimentalPayload() returned 0")
		}
		lastExtra = 0
	})
	if allocs != 0 {
		t.Fatalf("EncodeExperimentalPayload allocs=%v want 0", allocs)
	}
}
