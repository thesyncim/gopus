//go:build gopus_qext

package celt

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var libopusQEXTFinaliseHelper libopustest.HelperCache

func buildLibopusQEXTFinaliseHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext energy finalise",
		OutputBase:  "gopus_libopus_celt_qext_finalise",
		SourceFile:  "libopus_celt_qext_finalise_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTEnergyFinaliseFromBackupMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTFinaliseHelper.Path(buildLibopusQEXTFinaliseHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext energy finalise", err)
		return
	}
	for _, tc := range []struct {
		name     string
		channels int
		start    int
		end      int
		bitsLeft int
		withOld  bool
	}{
		{"mono_full_backup", 1, 0, MaxBands, 13, false},
		{"stereo_full_backup", 2, 0, MaxBands, 23, false},
		{"mono_narrow_backup", 1, 0, 13, 11, false},
		{"stereo_narrow_backup", 2, 0, 13, 17, false},
		{"stereo_hybrid_backup", 2, 17, MaxBands, 9, false},
		{"mono_full_history", 1, 0, MaxBands, 13, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const storage = 32
			var fineQuant, finePriority [MaxBands]int32
			for i := range fineQuant {
				fineQuant[i] = int32(1 + i%5)
				finePriority[i] = int32(i % 2)
			}
			old := make([]celtGLog, tc.channels*tc.end)
			residual := make([]celtGLog, len(old))
			for i := range old {
				old[i] = celtGLog(float32((i*7)%31-15) * 0.125)
				residual[i] = celtGLog(float32((i*11)%43-21) * (1.0 / 64.0))
			}
			oldBefore := append([]celtGLog(nil), old...)

			withOld := uint32(0)
			if tc.withOld {
				withOld = 1
			}
			payload := libopustest.NewOraclePayload("GQFI", uint32(tc.channels), uint32(tc.start), uint32(tc.end), uint32(tc.bitsLeft), storage, withOld)
			for _, v := range fineQuant {
				payload.I32(v)
			}
			for _, v := range finePriority {
				payload.I32(v)
			}
			for _, v := range old {
				payload.Float32(float32(v))
			}
			for _, v := range residual {
				payload.Float32(float32(v))
			}
			reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext energy finalise", "GQFO")
			if err != nil {
				t.Fatal(err)
			}
			refPacket := reader.Bytes(int(reader.U32()))
			refRange, refTell := reader.U32(), reader.U32()
			refOld := make([]uint32, len(old))
			refResidual := make([]uint32, len(residual))
			for i := range refOld {
				refOld[i] = math.Float32bits(reader.Float32())
			}
			for i := range refResidual {
				refResidual[i] = math.Float32bits(reader.Float32())
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			var enc rangecoding.Encoder
			enc.Init(make([]byte, storage))
			// Keep the C coder's full storage layout, including the gap before
			// raw end bits. Done otherwise compacts the returned Go slice.
			enc.Shrink(storage)
			var history []celtGLog
			if tc.withOld {
				history = old
			}
			encodeEnergyFinaliseResidual(&enc, history, residual, tc.start, tc.end, tc.channels,
				fineQuant[:], finePriority[:], tc.bitsLeft)
			gotRange, gotTell := enc.Range(), enc.TellFrac()
			gotPacket := enc.Done()
			if len(gotPacket) != len(refPacket) || !bytes.Equal(gotPacket, refPacket) {
				t.Errorf("packet: Go %x, C %x", gotPacket, refPacket)
			}
			if gotRange != refRange || uint32(gotTell) != refTell {
				t.Errorf("coder state: Go range %08x tell %d, C range %08x tell %d", gotRange, gotTell, refRange, refTell)
			}
			for i := range old {
				if got := math.Float32bits(float32(old[i])); got != refOld[i] {
					t.Errorf("history[%d]: Go %08x, C %08x", i, got, refOld[i])
				}
				if got := math.Float32bits(float32(residual[i])); got != refResidual[i] {
					t.Errorf("backup residual[%d]: Go %08x, C %08x", i, got, refResidual[i])
				}
				if !tc.withOld && old[i] != oldBefore[i] {
					t.Errorf("backup path modified history[%d]", i)
				}
			}
		})
	}
}

func TestQEXTReservedBytesWithoutExtraBandMode(t *testing.T) {
	// celt_encoder.c reserves extension bytes at narrow bandwidth, but
	// creates the extra-band mode only when end == mode->nbEBands.
	for _, tc := range []struct {
		name     string
		channels int
	}{{"mono", 1}, {"stereo", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			channels := tc.channels
			enc := NewEncoder(channels)
			enc.SetBandwidth(CELTNarrowband)
			enc.SetBitrate(256000)
			enc.SetQEXTEnabled(true)
			history := enc.ensureQEXTOldBandE(channels)
			for i := range history {
				history[i] = celtGLog(float32(i+1) * 0.125)
			}
			historyBefore := append([]celtGLog(nil), history...)
			const frameSize = 960
			pcm := make([]float32, frameSize*channels)
			for i := range frameSize {
				for c := range channels {
					pcm[i*channels+c] = float32(0.37 * math.Sin(2*math.Pi*(703+float64(c)*157)*float64(i)/48000))
				}
			}
			for range 4 {
				if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
					t.Fatal(err)
				}
				if len(enc.LastQEXTPayload()) == 0 {
					t.Fatal("narrowband frame did not reserve extension bytes")
				}
				for i, want := range historyBefore {
					if history[i] != want {
						t.Fatalf("narrowband frame modified extra-band history[%d]: got %08x want %08x", i,
							math.Float32bits(float32(history[i])), math.Float32bits(float32(want)))
					}
				}
			}
		})
	}
}
