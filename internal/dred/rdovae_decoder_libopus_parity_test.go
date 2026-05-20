//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package dred

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
)

const (
	libopusDREDRDOVAEDecInputMagic  = "GRDI"
	libopusDREDRDOVAEDecOutputMagic = "GRDO"
)

var (
	libopusDREDDecoderModelBlobHelperOnce sync.Once
	libopusDREDDecoderModelBlobHelperPath string
	libopusDREDDecoderModelBlobHelperErr  error

	libopusDREDRDOVAEDecHelperOnce sync.Once
	libopusDREDRDOVAEDecHelperPath string
	libopusDREDRDOVAEDecHelperErr  error
)

func getLibopusDREDDecoderModelBlobHelperPath() (string, error) {
	libopusDREDDecoderModelBlobHelperOnce.Do(func() {
		libopusDREDDecoderModelBlobHelperPath, libopusDREDDecoderModelBlobHelperErr = buildLibopusDREDHelper("libopus_dred_model_blob.c", "gopus_libopus_dred_model_blob")
	})
	if libopusDREDDecoderModelBlobHelperErr != nil {
		return "", libopusDREDDecoderModelBlobHelperErr
	}
	return libopusDREDDecoderModelBlobHelperPath, nil
}

func getLibopusDREDRDOVAEDecHelperPath() (string, error) {
	libopusDREDRDOVAEDecHelperOnce.Do(func() {
		libopusDREDRDOVAEDecHelperPath, libopusDREDRDOVAEDecHelperErr = buildLibopusDREDHelper("libopus_dred_rdovae_dec_info.c", "gopus_libopus_dred_rdovae_dec")
	})
	if libopusDREDRDOVAEDecHelperErr != nil {
		return "", libopusDREDRDOVAEDecHelperErr
	}
	return libopusDREDRDOVAEDecHelperPath, nil
}

func probeLibopusDREDDecoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDDecoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run dred decoder model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
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

	var payload bytes.Buffer
	payload.WriteString(libopusDREDRDOVAEDecInputMagic)
	for _, v := range []uint32{1, uint32(nbLatents)} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, fmt.Errorf("encode rdovae decoder helper header: %w", err)
		}
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeBits(state); err != nil {
		return nil, fmt.Errorf("encode rdovae decoder state: %w", err)
	}
	if err := writeBits(latents); err != nil {
		return nil, fmt.Errorf("encode rdovae decoder latents: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run rdovae decoder helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusDREDRDOVAEDecOutputMagic {
		return nil, fmt.Errorf("unexpected rdovae decoder helper output")
	}
	gotLatents := int(binary.LittleEndian.Uint32(data[8:12]))
	if gotLatents != nbLatents {
		return nil, fmt.Errorf("rdovae decoder helper latent count=%d want %d", gotLatents, nbLatents)
	}
	features := make([]float32, nbLatents*rdovae.OutputOutSize)
	offset := 12
	for i := range features {
		if len(data) < offset+4 {
			return nil, fmt.Errorf("truncated rdovae decoder helper output")
		}
		features[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
	}
	return features, nil
}

func TestRDOVAEDecoderMatchesLibopusOnRealModel(t *testing.T) {
	raw, err := probeLibopusDREDDecoderModelBlob()
	if err != nil {
		t.Skipf("dred decoder model blob helper unavailable: %v", err)
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
					t.Skipf("rdovae decoder helper unavailable: %v", err)
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
