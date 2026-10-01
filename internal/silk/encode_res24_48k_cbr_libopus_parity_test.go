//go:build gopus_fixed_point

package silk

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
)

var silkRes24API48kHelper libopustest.HelperCache

func buildSILKRes24API48kHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "fixed SILK RES24 48 kHz API",
		OutputBase:  "gopus_silk_fixed_api_res24_48k",
		SourceFile:  "libopus_silk_fixed_api_res24_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3"},
		RefIncludes: []string{"celt", "silk", "silk/fixed"},
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
	}
	if extsupport.QEXT {
		cfg.FixedRef = false
		cfg.FixedQEXTRef = true
		cfg.Libs = []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"}
	}
	return libopustest.BuildCHelper(cfg)
}

func TestSILKFixedRes24API48kCBRSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := silkRes24API48kHelper.Path(buildSILKRes24API48kHelper)
	if err != nil {
		t.Fatal(err)
	}

	const (
		apiRate    = 48000
		frameSize  = 480
		frameMs    = 10
		frames     = 3
		complexity = 10
	)
	for _, tc := range []struct {
		name         string
		internalRate int
		bitrate      int
		maxBits      int
	}{
		{name: "narrowband", internalRate: 8000, bitrate: 15200, maxBits: 19 * 8},
		{name: "wideband", internalRate: 16000, bitrate: 31200, maxBits: 39 * 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantAMMultisineV1,
				apiRate, frameSize*frames, 1)
			if err != nil {
				t.Fatal(err)
			}
			raw := make([]int32, len(pcm))
			for i, sample := range pcm {
				q := float32(math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0)
				raw[i] = opusmath.Float32ToInt24(q)
			}
			filtered, _, err := libopustest.ProbeFixedDCReject(raw, [4]int32{}, apiRate, 1, 3)
			if err != nil {
				t.Fatal(err)
			}

			input := libopustest.NewOraclePayload("GSRQ",
				uint32(apiRate), uint32(tc.internalRate), uint32(1), uint32(frameMs),
				uint32(tc.bitrate), uint32(tc.maxBits), uint32(complexity), uint32(1), uint32(frames))
			input.I32s(filtered...)
			reader, err := libopustest.RunOracle(helper, input.Bytes(), "fixed SILK RES24 48 kHz API", "GSRP")
			if err != nil {
				t.Fatal(err)
			}
			if reader.Count(frames) != frames {
				t.Fatalf("unexpected C helper header")
			}
			selectedCArch := int32(reader.U32())
			t.Logf("selected C arch=%d", selectedCArch)

			enc := NewPacketEncoder(1)
			ctl := EncControl{
				NChannelsAPI: 1, NChannelsInternal: 1, APISampleRate: apiRate,
				MaxInternalSampleRate: 16000, MinInternalSampleRate: 8000,
				DesiredInternalSampleRate: int32(tc.internalRate), PayloadSizeMs: frameMs,
				BitRate: int32(tc.bitrate), Complexity: complexity, UseCBR: true,
				MaxBits: int32(tc.maxBits),
			}
			frame := make([]int32, frameSize)
			for f := range frames {
				copy(frame, filtered[f*frameSize:(f+1)*frameSize])
				cStatus := int32(reader.U32())
				cBytes := int(reader.U32())
				cRange, cTell := reader.U32(), reader.U32()
				cPacket := reader.Bytes(cBytes)
				cState := make([]int32, 17)
				for i := range cState {
					cState[i] = reader.I32()
				}
				cXBufCount := int(reader.U32())
				cXBuf := make([]int16, cXBufCount)
				for i := range cXBuf {
					cXBuf[i] = int16(reader.I32())
				}
				cPitchMeta := make([]int32, 6)
				for i := range cPitchMeta {
					cPitchMeta[i] = reader.I32()
				}
				if cStatus != 0 {
					t.Fatalf("frame %d selected C status=%d", f, cStatus)
				}

				storage := make([]byte, 1275)
				var re rangecoding.Encoder
				re.Init(storage)
				goBytes, err := enc.EncodeResQ8(&ctl, frame, frameSize, &re, 0, 1)
				if err != nil {
					t.Fatalf("frame %d EncodeResQ8: %v", f, err)
				}
				goRange, goTell := re.Range(), uint32(re.Tell())
				goPacket := re.Done()
				st := enc.state[0]
				fixed := st.fixed
				goState := []int32{
					fixed.frameCounter, fixed.prevSignalType, int32(st.lastQuantOffsetType), int32(st.lastSeed),
					st.speechActivityQ8, st.inputTiltQ15, fixed.sumLogGainQ7,
					fixed.prevSignalType, fixed.prevLag,
					fixed.ecPrevSignalType, int32(fixed.ecPrevLagIndex),
					int32(fixed.lastGainIndex), fixed.harmShapeGainSmthQ16, fixed.tiltSmthQ16,
					fixed.ltpCorrQ15, enc.nBitsExceeded, enc.nBitsUsedLBRR,
				}
				stateNames := [...]string{
					"frameCounter", "indices.signalType", "indices.quantOffsetType", "indices.Seed",
					"speech_activity_Q8", "input_tilt_Q15", "sum_log_gain_Q7", "prevSignalType",
					"prevLag", "ec_prevSignalType", "ec_prevLagIndex", "LastGainIndex",
					"HarmShapeGain_smth_Q16", "Tilt_smth_Q16", "LTPCorr_Q15", "nBitsExceeded",
					"nBitsUsedLBRR",
				}
				for i := range goState {
					if goState[i] != cState[i] {
						t.Fatalf("frame %d state %s Go=%d C=%d", f, stateNames[i], goState[i], cState[i])
					}
				}
				firstXBufDiff := -1
				for i := 0; i < min(len(fixed.xBuf), len(cXBuf)); i++ {
					if fixed.xBuf[i] != cXBuf[i] {
						firstXBufDiff = i
						break
					}
				}
				if len(fixed.xBuf) != len(cXBuf) || firstXBufDiff >= 0 {
					goValue, cValue := int16(0), int16(0)
					if firstXBufDiff >= 0 {
						goValue, cValue = fixed.xBuf[firstXBufDiff], cXBuf[firstXBufDiff]
					}
					t.Fatalf("frame %d x_buf differs Go=%d samples C=%d samples first=%d Go=%d C=%d",
						f, len(fixed.xBuf), len(cXBuf), firstXBufDiff, goValue, cValue)
				}
				pitchWin := int32((10 + 4) * tc.internalRate / 1000)
				goFirstReset := int32(0)
				if fixed.firstFrameAfterReset {
					goFirstReset = 1
				}
				goPitchMeta := []int32{
					st.pitchEstimationThresholdQ16, st.pitchEstimationComplexity, pitchWin,
					goFirstReset, 2,
				}
				pitchMetaNames := [...]string{
					"pitchEstimationThreshold_Q16", "pitchEstimationComplexity", "pitch_LPC_win_length",
					"first_frame_after_reset", "nb_subfr",
				}
				cPitchMetaIndices := [...]int{0, 1, 2, 3, 5}
				for i, cIndex := range cPitchMetaIndices {
					if cPitchMeta[cIndex] != goPitchMeta[i] {
						t.Fatalf("frame %d pitch metadata %s C=%d Go=%d", f, pitchMetaNames[i], cPitchMeta[cIndex], goPitchMeta[i])
					}
				}
				if cPitchMeta[4] != selectedCArch {
					t.Fatalf("frame %d selected C arch metadata=%d header=%d", f, cPitchMeta[4], selectedCArch)
				}
				if goBytes < 0 || int(goBytes) != cBytes || goRange != cRange || goTell != cTell ||
					!bytes.Equal(goPacket[:goBytes], cPacket) {
					first := 0
					for first < min(len(goPacket[:goBytes]), len(cPacket)) && goPacket[first] == cPacket[first] {
						first++
					}
					t.Fatalf("frame %d SILK API mismatch bytes=%d/%d range=%08x/%08x tell=%d/%d firstByte=%d Go=%x C=%x",
						f, goBytes, cBytes, goRange, cRange, goTell, cTell, first,
						goPacket[:goBytes], cPacket)
				}
				// opus_encode_frame_native carries switchReady into the next
				// silk_Encode call as opusCanSwitch.
				ctl.OpusCanSwitch = ctl.SwitchReady
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
