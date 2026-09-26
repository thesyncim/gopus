//go:build gopus_qext

package celt

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var qextFineEnergyHelper libopustest.HelperCache

func buildQEXTFineEnergyHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext fine energy",
		OutputBase:  "gopus_libopus_celt_qext_fine_energy",
		SourceFile:  "libopus_celt_qext_fine_energy_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTFineEnergyStridedStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	path, err := qextFineEnergyHelper.Path(buildQEXTFineEnergyHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext fine energy", err)
		return
	}
	for _, tc := range []struct {
		fs, end, physical, coded int
	}{
		{48000, 2, 2, 1},
		{48000, 2, 2, 2},
		{96000, nbQEXTBands, 2, 2},
	} {
		name := fmt.Sprintf("fs%d-physical%d-coded%d-end%d", tc.fs, tc.physical, tc.coded, tc.end)
		t.Run(name, func(t *testing.T) {
			const storage = 64
			oldC := make([]float32, nbQEXTBands*tc.coded)
			errorC := make([]float32, len(oldC))
			for i := range oldC {
				oldC[i] = float32(i%nbQEXTBands)*0.125 - 1.5 + float32(i/nbQEXTBands)*0.375
				errorC[i] = float32((i*13)%17-8) * 0.045
			}
			var fineBits [nbQEXTBands]uint32
			for band := range fineBits {
				if band%3 != 0 {
					fineBits[band] = uint32(1 + band%4)
				}
			}

			payload := libopustest.NewOraclePayload("GQFI")
			payload.U32(uint32(tc.coded))
			payload.U32(uint32(tc.fs))
			payload.U32(uint32(tc.end))
			payload.U32(storage)
			payload.Float32s(oldC...)
			payload.Float32s(errorC...)
			payload.U32s(fineBits[:]...)
			reader, err := libopustest.RunOracle(path, payload.Bytes(), "celt qext fine energy", "GQFO")
			if err != nil {
				t.Fatal(err)
			}
			wantError := reader.U32()
			wantRange := reader.U32()
			wantPacketLen := int(reader.U32())
			wantOld := make([]float32, len(oldC))
			wantResidual := make([]float32, len(oldC))
			for i := range wantOld {
				wantOld[i] = reader.Float32()
			}
			for i := range wantResidual {
				wantResidual[i] = reader.Float32()
			}
			wantPacket := append([]byte(nil), reader.Bytes(wantPacketLen)...)
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			enc := NewEncoder(tc.physical)
			enc.SetStreamChannels(tc.coded)
			state := enc.ensureQEXTOldBandE(tc.physical)
			for i := range state {
				state[i] = -99
			}
			for ch := range tc.coded {
				for band := range nbQEXTBands {
					state[ch*MaxBands+band] = celtGLog(oldC[ch*nbQEXTBands+band])
				}
			}
			residual := make([]celtGLog, tc.end*tc.coded)
			for ch := range tc.coded {
				for band := range tc.end {
					residual[ch*tc.end+band] = celtGLog(errorC[ch*nbQEXTBands+band])
				}
			}
			bits := make([]int32, nbQEXTBands)
			for i, v := range fineBits {
				bits[i] = int32(v)
			}
			buffer := make([]byte, storage)
			var re rangecoding.Encoder
			re.Init(buffer)
			enc.encodeFineEnergyFromErrorWithEncoder(&re, state[:MaxBands*tc.coded], tc.end, MaxBands, bits[:tc.end], residual)
			gotRange := re.Range()
			gotPacket := re.Done()
			if uint32(re.Error()) != wantError || gotRange != wantRange || !bytes.Equal(gotPacket, wantPacket) {
				t.Fatalf("coder: error %d/%d range %08x/%08x packet %x/%x", re.Error(), wantError, gotRange, wantRange, gotPacket, wantPacket)
			}
			for ch := range tc.coded {
				for band := range nbQEXTBands {
					idxC := ch*nbQEXTBands + band
					idxGo := ch*MaxBands + band
					if math.Float32bits(float32(state[idxGo])) != math.Float32bits(wantOld[idxC]) {
						t.Fatalf("oldBandE ch%d band%d: Go %08x C %08x", ch, band, math.Float32bits(float32(state[idxGo])), math.Float32bits(wantOld[idxC]))
					}
					if band < tc.end && math.Float32bits(float32(residual[ch*tc.end+band])) != math.Float32bits(wantResidual[idxC]) {
						t.Fatalf("error ch%d band%d: Go %08x C %08x", ch, band, math.Float32bits(float32(residual[ch*tc.end+band])), math.Float32bits(wantResidual[idxC]))
					}
				}
			}
			for ch := range tc.physical {
				for band := nbQEXTBands; band < MaxBands; band++ {
					if state[ch*MaxBands+band] != -99 {
						t.Fatalf("untouched Go history ch%d band%d changed", ch, band)
					}
				}
			}
			if tc.coded == 1 {
				for band := range MaxBands {
					if state[MaxBands+band] != -99 {
						t.Fatalf("uncoded physical channel band%d changed", band)
					}
				}
			}
		})
	}
}
