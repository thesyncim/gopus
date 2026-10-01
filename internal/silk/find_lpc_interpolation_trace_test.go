//go:build gopus_silk_trace && linux && amd64 && !gopus_fixed_point

package silk_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const (
	silkLPCTraceInputMagic  = "GSLI"
	silkLPCTraceOutputMagic = "GSLO"
	silkLPCTraceMode        = uint32(6)
)

var silkLPCTraceHelper libopustest.HelperCache

type silkLPCTraceCandidate struct {
	index        int32
	nlsfQ15      [16]int16
	lpc          [16]float32
	energyFirst  float64
	energySecond float64
	residual     float32
}

type silkLPCTraceResult struct {
	order         int
	selectedIndex int32
	fullResidual  float32
	lastResidual  float32
	fullA         [16]float32
	lastA         [16]float32
	lastNLSFQ15   [16]int16
	candidates    []silkLPCTraceCandidate
}

func silkLPCTraceHelperPath() (string, error) {
	archive := libopustest.RefPath(".libs", "libopus.a")
	cflags := []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}
	if silkLPCTraceOracleUsesAVX2 {
		archive = libopustest.SIMDRefPath(".libs", "libopus.a")
		cflags = append(cflags, "-DGOPUS_LIBOPUS_REQUIRE_AVX2=1")
	}
	return silkLPCTraceHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "silk lpc interpolation trace",
		OutputBase:  "gopus_libopus_silk_lpc_trace",
		SourceFile:  "libopus_silk_lpc_info.c",
		CFlags:      cflags,
		RefIncludes: []string{"celt", "silk", "silk/float"},
		SIMDRef:     silkLPCTraceOracleUsesAVX2,
		Libs:        []string{archive, "-lm"},
	})
}

func runLibopusSILKLPCTrace(snapshots []silk.SILKNLSFInterpolationSnapshot) ([]silkLPCTraceResult, error) {
	binPath, err := silkLPCTraceHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(silkLPCTraceInputMagic, silkLPCTraceMode, uint32(len(snapshots)))
	for _, snapshot := range snapshots {
		if snapshot.Order != 10 && snapshot.Order != 16 {
			return nil, fmt.Errorf("order=%d", snapshot.Order)
		}
		if snapshot.NumSubframes != 4 || snapshot.SubframeLen <= snapshot.Order ||
			len(snapshot.Input) != snapshot.NumSubframes*snapshot.SubframeLen {
			return nil, fmt.Errorf("invalid input dimensions order=%d subframe=%d subframes=%d input=%d",
				snapshot.Order, snapshot.SubframeLen, snapshot.NumSubframes, len(snapshot.Input))
		}
		payload.U32(uint32(snapshot.SubframeLen - snapshot.Order))
		payload.U32(uint32(snapshot.NumSubframes))
		payload.U32(uint32(snapshot.Order))
		payload.U32(1) // useInterpolatedNLSFs
		payload.U32(0) // first_frame_after_reset
		payload.Float32(snapshot.MinInvGain)
		for i := range 16 {
			payload.I32(int32(snapshot.PrevNLSFQ15[i]))
		}
		payload.Float32s(snapshot.Input...)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run libopus interpolation trace helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderVersion("silk lpc interpolation trace", silkLPCTraceOutputMagic, data)
	if err != nil {
		return nil, err
	}
	if version != 3 {
		return nil, fmt.Errorf("helper version=%d want 3", version)
	}
	arch, implementation, presumed := reader.U32(), reader.U32(), reader.U32()
	if !libopustest.NativeX86SILKInnerProductMetadataValid(arch, implementation, presumed, silkLPCTraceOracleUsesAVX2) {
		return nil, fmt.Errorf("unexpected SILK inner-product dispatch arch=%d implementation=%d presumed=%d (want AVX2=%t)",
			arch, implementation, presumed, silkLPCTraceOracleUsesAVX2)
	}
	count := reader.Count(len(snapshots))
	out := make([]silkLPCTraceResult, count)
	for i := range out {
		result := &out[i]
		result.order = int(reader.U32())
		if result.order != 10 && result.order != 16 {
			return nil, fmt.Errorf("case %d helper order=%d", i, result.order)
		}
		result.selectedIndex = reader.I32()
		result.fullResidual = reader.Float32()
		result.lastResidual = reader.Float32()
		for j := range 16 {
			result.fullA[j] = reader.Float32()
		}
		for j := range 16 {
			result.lastA[j] = reader.Float32()
		}
		for j := range 16 {
			result.lastNLSFQ15[j] = int16(reader.I32())
		}
		candidateCount := int(reader.U32())
		if candidateCount < 0 || candidateCount > 4 {
			return nil, fmt.Errorf("case %d helper candidate count=%d", i, candidateCount)
		}
		result.candidates = make([]silkLPCTraceCandidate, candidateCount)
		for j := range candidateCount {
			candidate := &result.candidates[j]
			candidate.index = int32(reader.U32())
			for k := range 16 {
				candidate.nlsfQ15[k] = int16(reader.I32())
			}
			for k := range 16 {
				candidate.lpc[k] = reader.Float32()
			}
			candidate.energyFirst = reader.Float64()
			candidate.energySecond = reader.Float64()
			candidate.residual = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindLPCInterpolationTraceAgainstLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name           string
		channels       int
		bitrate        int
		bandwidth      types.Bandwidth
		traceFrame     int
		originalCIndex int32
	}{
		{name: "MB_mono_frame6", channels: 1, bitrate: 24000, bandwidth: types.BandwidthMediumband, traceFrame: 6, originalCIndex: 2},
		{name: "WB_stereo_frame13_channel0", channels: 2, bitrate: 48000, bandwidth: types.BandwidthWideband, traceFrame: 13, originalCIndex: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 960
			const frameCount = 50
			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantAMMultisineV1,
				48000,
				frameCount*frameSize*tc.channels,
				tc.channels,
			)
			if err != nil {
				t.Fatalf("generate trace input: %v", err)
			}
			for i, sample := range pcm {
				q := math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0
				pcm[i] = float32(q)
			}

			enc := encoder.NewEncoder(48000, tc.channels)
			enc.SetMode(encoder.ModeSILK)
			enc.SetRestrictedSilkApplication(true)
			enc.SetLowDelay(false)
			enc.SetBandwidth(tc.bandwidth)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(encoder.ModeCBR)
			enc.SetComplexity(10)

			channelByEncoder := make(map[*silk.Encoder]int, tc.channels)
			nextChannel := 0
			currentFrame := -1
			var snapshots []silk.SILKNLSFInterpolationSnapshot
			var encodeErr error
			silk.WithSILKNLSFInterpolationTraceHook(func(e *silk.Encoder, snapshot silk.SILKNLSFInterpolationSnapshot) {
				channel, ok := channelByEncoder[e]
				if !ok {
					channel = nextChannel
					channelByEncoder[e] = channel
					nextChannel++
				}
				if currentFrame != tc.traceFrame || channel != 0 {
					return
				}
				snapshot.Input = append([]float32(nil), snapshot.Input...)
				snapshots = append(snapshots, snapshot)
			}, func() {
				for frame := 0; frame < frameCount; frame++ {
					currentFrame = frame
					start := frame * frameSize * tc.channels
					end := start + frameSize*tc.channels
					if _, err := enc.Encode(pcm[start:end], frameSize); err != nil {
						encodeErr = fmt.Errorf("frame %d: %w", frame, err)
						return
					}
				}
			})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if len(snapshots) != 1 {
				t.Fatalf("captured interpolation traces=%d, want 1 at packet frame %d channel 0", len(snapshots), tc.traceFrame)
			}

			want, err := runLibopusSILKLPCTrace(snapshots)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk lpc interpolation trace", err)
			}
			got, ref := snapshots[0], want[0]
			if got.Order != ref.order {
				t.Fatalf("order=%d C=%d", got.Order, ref.order)
			}
			if len(got.Input) != got.NumSubframes*got.SubframeLen {
				t.Fatalf("snapshot input length=%d, want %d", len(got.Input), got.NumSubframes*got.SubframeLen)
			}
			if got.CandidateCount < 0 || got.CandidateCount > len(got.Candidates) {
				t.Fatalf("Go candidate count=%d outside [0,%d]", got.CandidateCount, len(got.Candidates))
			}
			t.Logf("frame=%d channel=0 order=%d subframe=%d minInvGain=%08x input=%d; interp selected Go=%d C(replay-Go-input)=%d C(original-encode)=%d; C candidates=%d",
				tc.traceFrame, got.Order, got.SubframeLen, math.Float32bits(got.MinInvGain), len(got.Input),
				got.SelectedIndex, ref.selectedIndex, tc.originalCIndex, len(ref.candidates))
			logSILKLPCTraceDifferences(t, got, ref)
		})
	}
}

func logSILKLPCTraceDifferences(t *testing.T, got silk.SILKNLSFInterpolationSnapshot, ref silkLPCTraceResult) {
	t.Helper()
	firstDifference := ""
	report := func(format string, args ...any) {
		if firstDifference == "" {
			firstDifference = fmt.Sprintf(format, args...)
		}
		t.Logf(format, args...)
	}
	if bits := math.Float32bits(got.FullResidualEnergy); bits != math.Float32bits(ref.fullResidual) {
		report("full Burg residual Go=%08x C=%08x", bits, math.Float32bits(ref.fullResidual))
	}
	for i := range got.Order {
		if bits := math.Float32bits(got.FullBurgCoefficients[i]); bits != math.Float32bits(ref.fullA[i]) {
			report("full Burg A[%d] Go=%08x C=%08x", i, bits, math.Float32bits(ref.fullA[i]))
			break
		}
	}
	if bits := math.Float32bits(got.LastResidualEnergy); bits != math.Float32bits(ref.lastResidual) {
		report("last-half Burg residual Go=%08x C=%08x", bits, math.Float32bits(ref.lastResidual))
	}
	for i := range got.Order {
		if bits := math.Float32bits(got.LastBurgCoefficients[i]); bits != math.Float32bits(ref.lastA[i]) {
			report("last-half Burg A[%d] Go=%08x C=%08x", i, bits, math.Float32bits(ref.lastA[i]))
			break
		}
	}
	for i := range got.Order {
		if got.LastNLSFQ15[i] != ref.lastNLSFQ15[i] {
			report("last-half NLSF[%d] Go=%d C=%d", i, got.LastNLSFQ15[i], ref.lastNLSFQ15[i])
			break
		}
	}
	common := min(got.CandidateCount, len(ref.candidates))
	for i := range common {
		g, c := got.Candidates[i], ref.candidates[i]
		if g.InterpIndex != c.index {
			report("candidate %d order Go=%d C=%d", i, g.InterpIndex, c.index)
			continue
		}
		for j := range got.Order {
			if g.NLSFQ15[j] != c.nlsfQ15[j] {
				report("candidate k=%d NLSF[%d] Go=%d C=%d", g.InterpIndex, j, g.NLSFQ15[j], c.nlsfQ15[j])
				break
			}
		}
		for j := range got.Order {
			goLPC := float32(g.LPCQ12[j]) / 4096.0
			if math.Float32bits(goLPC) != math.Float32bits(c.lpc[j]) {
				report("candidate k=%d LPC[%d] Go=%08x C=%08x", g.InterpIndex, j,
					math.Float32bits(goLPC), math.Float32bits(c.lpc[j]))
				break
			}
		}
		if math.Float64bits(g.EnergyFirst) != math.Float64bits(c.energyFirst) {
			report("candidate k=%d energy first Go=%016x C=%016x", g.InterpIndex,
				math.Float64bits(g.EnergyFirst), math.Float64bits(c.energyFirst))
		}
		if math.Float64bits(g.EnergySecond) != math.Float64bits(c.energySecond) {
			report("candidate k=%d energy second Go=%016x C=%016x", g.InterpIndex,
				math.Float64bits(g.EnergySecond), math.Float64bits(c.energySecond))
		}
		if math.Float32bits(g.ResidualEnergy) != math.Float32bits(c.residual) {
			report("candidate k=%d residual Go=%08x C=%08x", g.InterpIndex,
				math.Float32bits(g.ResidualEnergy), math.Float32bits(c.residual))
		}
	}
	if got.CandidateCount != len(ref.candidates) {
		report("candidate count Go=%d C=%d", got.CandidateCount, len(ref.candidates))
	}
	if got.SelectedIndex != ref.selectedIndex {
		report("selected interpolation index Go=%d C=%d", got.SelectedIndex, ref.selectedIndex)
	}
	if firstDifference == "" {
		t.Log("Go and the selected libopus build match at every captured FindLPC stage")
	} else {
		t.Logf("first Go/C FindLPC difference: %s", firstDifference)
	}
}
