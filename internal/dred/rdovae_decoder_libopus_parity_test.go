//go:build gopus_dred || gopus_extra_controls

package dred

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDREDRDOVAEDecInputMagic  = "GRDI"
	libopusDREDRDOVAEDecOutputMagic = "GRDO"
)

var (
	libopusDREDDecoderModelBlobHelper libopustest.HelperCache
	libopusDREDRDOVAEDecHelper        libopustest.HelperCache
)

func getLibopusDREDDecoderModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDDecoderModelBlobHelper, "libopus_dred_model_blob.c", "gopus_libopus_dred_model_blob")
}

func getLibopusDREDRDOVAEDecHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDRDOVAEDecHelper, "libopus_dred_rdovae_dec_info.c", "gopus_libopus_dred_rdovae_dec")
}

func probeLibopusDREDDecoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDDecoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run dred decoder model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDREDRDOVAEDec(state, latents []float32, nbLatents int) ([]float32, error) {
	binPath, err := getLibopusDREDRDOVAEDecHelperPath()
	if err != nil {
		return nil, err
	}
	if nbLatents <= 0 {
		return nil, fmt.Errorf("latent count must be positive")
	}
	if len(state) != rdovae.StateDim {
		return nil, fmt.Errorf("state length=%d want %d", len(state), rdovae.StateDim)
	}
	if len(latents) != nbLatents*(rdovae.LatentDim+1) {
		return nil, fmt.Errorf("latents length=%d want %d", len(latents), nbLatents*(rdovae.LatentDim+1))
	}

	payload := libopustest.NewOraclePayload(libopusDREDRDOVAEDecInputMagic, uint32(nbLatents))
	for _, v := range state {
		payload.Float32(v)
	}
	for _, v := range latents {
		payload.Float32(v)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run rdovae decoder helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("rdovae decoder", libopusDREDRDOVAEDecOutputMagic, data)
	if err != nil {
		return nil, err
	}
	reader.Count(nbLatents)
	features := make([]float32, nbLatents*rdovae.OutputOutSize)
	reader.ExpectRemaining(len(features) * 4)
	for i := range features {
		features[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return features, nil
}

func TestRDOVAEDecoderMatchesLibopusOnRealModel(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusDREDDecoderModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred decoder model blob", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real dred decoder blob) error: %v", err)
	}
	model, err := rdovae.LoadDecoder(blob)
	if err != nil {
		t.Fatalf("LoadDecoder(real model) error: %v", err)
	}

	cases := []struct {
		name    string
		state   func() []float32
		latents func(int) []float32
	}{
		{"trig", makeDREDRDOVAEDecoderState, makeDREDRDOVAEDecoderLatents},
		{"zero", makeDREDRDOVAEZeroDecoderState, makeDREDRDOVAEZeroDecoderLatents},
		{"impulse", makeDREDRDOVAEImpulseDecoderState, makeDREDRDOVAEImpulseDecoderLatents},
		{"alternating", makeDREDRDOVAEAlternatingDecoderState, makeDREDRDOVAEAlternatingDecoderLatents},
	}
	for _, tc := range cases {
		for _, nbLatents := range []int{1, 2, 4, 8, 16} {
			t.Run(fmt.Sprintf("%s_latents_%d", tc.name, nbLatents), func(t *testing.T) {
				state := tc.state()
				latents := tc.latents(nbLatents)
				want, err := probeLibopusDREDRDOVAEDec(state, latents, nbLatents)
				if err != nil {
					libopustest.HelperUnavailable(t, "rdovae decoder", err)
				}

				got := make([]float32, len(want))
				var processor rdovae.Processor
				if n := model.DecodeAllWithProcessor(&processor, got, state, latents, nbLatents); n != len(got) {
					t.Fatalf("DecodeAllWithProcessor count=%d want %d", n, len(got))
				}
				assertDREDFloat32Close(t, got, want, 0, "rdovae decoder features")
			})
		}
	}
}

func makeDREDRDOVAEDecoderState() []float32 {
	state := make([]float32, rdovae.StateDim)
	for i := range state {
		x := float64(i)
		state[i] = float32(0.25*math.Sin(0.17*x) - 0.19*math.Cos(0.31*x))
	}
	return state
}

func makeDREDRDOVAEDecoderLatents(nbLatents int) []float32 {
	latents := make([]float32, nbLatents*(rdovae.LatentDim+1))
	for frame := 0; frame < nbLatents; frame++ {
		base := frame * (rdovae.LatentDim + 1)
		for i := 0; i < rdovae.LatentDim; i++ {
			x := float64(base + i)
			latents[base+i] = float32(0.37*math.Sin(0.11*x) + 0.23*math.Cos(0.07*x+0.13*float64(frame)))
		}
		latents[base+rdovae.LatentDim] = float32(float64((frame%16))*0.125 - 1.0)
	}
	return latents
}

func makeDREDRDOVAEZeroDecoderState() []float32 {
	return make([]float32, rdovae.StateDim)
}

func makeDREDRDOVAEZeroDecoderLatents(nbLatents int) []float32 {
	return make([]float32, nbLatents*(rdovae.LatentDim+1))
}

func makeDREDRDOVAEImpulseDecoderState() []float32 {
	state := make([]float32, rdovae.StateDim)
	for i := 0; i < len(state); i += 17 {
		if (i/17)%2 == 0 {
			state[i] = 0.875
		} else {
			state[i] = -0.625
		}
	}
	return state
}

func makeDREDRDOVAEImpulseDecoderLatents(nbLatents int) []float32 {
	latents := make([]float32, nbLatents*(rdovae.LatentDim+1))
	for frame := 0; frame < nbLatents; frame++ {
		base := frame * (rdovae.LatentDim + 1)
		latents[base+(frame%rdovae.LatentDim)] = 0.75
		latents[base+((frame*7+3)%rdovae.LatentDim)] = -0.5
		latents[base+rdovae.LatentDim] = float32(frame%5-2) * 0.25
	}
	return latents
}

func makeDREDRDOVAEAlternatingDecoderState() []float32 {
	state := make([]float32, rdovae.StateDim)
	for i := range state {
		state[i] = float32(i%9-4) * 0.11
	}
	return state
}

func makeDREDRDOVAEAlternatingDecoderLatents(nbLatents int) []float32 {
	latents := make([]float32, nbLatents*(rdovae.LatentDim+1))
	for i := range latents {
		latents[i] = float32(i%13-6) * 0.07
	}
	return latents
}
