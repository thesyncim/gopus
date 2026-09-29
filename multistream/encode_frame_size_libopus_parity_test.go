package multistream

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	internalencoder "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamFrameSizeOracleHelper libopustest.HelperCache

type nativeFrameSizeRateCase struct {
	sampleRate int
	legal      []int
	invalid    []int
}

type nativeFrameSizeModeCase struct {
	restrictedSilk bool
	rates          []nativeFrameSizeRateCase
}

func TestValidNativeFrameSizesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	modes := []nativeFrameSizeModeCase{
		{rates: buildNativeFrameSizeCases(rates, false)},
		{restrictedSilk: true, rates: buildNativeFrameSizeCases(rates, true)},
	}

	for _, sampleFormat := range []int{0, 1} {
		formatName := "float32"
		if sampleFormat == 1 {
			formatName = "int16"
		}
		t.Run(formatName, func(t *testing.T) {
			binPath, err := multistreamFrameSizeOracleHelper.Path(func() (string, error) {
				return buildMultistreamReferenceHelper(libopustest.CHelperConfig{
					Label:       "multistream frame-size validation",
					OutputBase:  "gopus_libopus_multistream_frame_size",
					SourceFile:  "libopus_multistream_frame_size.c",
					CFlags:      []string{"-O3", "-DNDEBUG"},
					RefIncludes: []string{"celt", "src"},
					Libs:        []string{"-lm"},
				})
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream frame-size reference", err)
				return
			}

			payload := libopustest.NewOraclePayload("MFSI", uint32(sampleFormat), uint32(len(modes)))
			for _, mode := range modes {
				payload.U32(boolToU32(mode.restrictedSilk))
				payload.U32(uint32(len(mode.rates)))
				for _, rate := range mode.rates {
					payload.U32(uint32(rate.sampleRate))
					payload.U32(uint32(len(rate.legal)))
					payload.I32s(int32s(rate.legal)...)
					payload.U32(uint32(len(rate.invalid)))
					payload.I32s(int32s(rate.invalid)...)
				}
			}

			reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(),
				"multistream frame-size validation", "MFSO", 1)
			if err != nil {
				t.Fatalf("live C frame-size helper: %v", err)
			}
			if got := reader.Count(len(modes)); got != len(modes) {
				t.Fatalf("oracle mode count=%d want %d", got, len(modes))
			}

			for _, mode := range modes {
				t.Run(fmt.Sprintf("restricted_silk_%t", mode.restrictedSilk), func(t *testing.T) {
					if got := reader.U32(); got != boolToU32(mode.restrictedSilk) {
						t.Fatalf("oracle restricted SILK mode=%d want %t", got, mode.restrictedSilk)
					}
					if got := reader.Count(len(mode.rates)); got != len(mode.rates) {
						t.Fatalf("oracle rate count=%d want %d", got, len(mode.rates))
					}
					for _, rate := range mode.rates {
						t.Run(fmt.Sprintf("rate_%d", rate.sampleRate), func(t *testing.T) {
							if got := int(reader.U32()); got != rate.sampleRate {
								t.Fatalf("oracle sample rate=%d want %d", got, rate.sampleRate)
							}
							if got := reader.Count(len(rate.legal)); got != len(rate.legal) {
								t.Fatalf("oracle legal duration count=%d want %d", got, len(rate.legal))
							}
							for _, frameSize := range rate.legal {
								got := reader.I32()
								cRange := reader.U32()
								if got <= 0 {
									t.Fatalf("libopus rejected legal frame size %d at %d Hz: %d", frameSize, rate.sampleRate, got)
								}
								if !validNativeFrameSize(rate.sampleRate, frameSize, mode.restrictedSilk) {
									t.Fatalf("Go validator rejected legal frame size %d at %d Hz", frameSize, rate.sampleRate)
								}
								enc := newFrameSizeParityEncoder(t, rate.sampleRate, mode.restrictedSilk)
								pcm := frameSizeProbeInput(sampleFormat, frameSize, 9)
								out := make([]byte, 4000)
								n, encodeErr := encodeFrameSizeProbe(enc, sampleFormat, pcm, frameSize, out)
								if encodeErr != nil || n != int(got) {
									t.Fatalf("Go legal frame %d at %d Hz returned n=%d C=%d err=%v", frameSize, rate.sampleRate, n, got, encodeErr)
								}
								if gotRange := enc.GetFinalRange(); gotRange != cRange {
									t.Fatalf("frame %d final range Go/C=%08x/%08x", frameSize, gotRange, cRange)
								}
							}

							if got := reader.Count(len(rate.invalid)); got != len(rate.invalid) {
								t.Fatalf("oracle invalid duration count=%d want %d", got, len(rate.invalid))
							}
							for _, frameSize := range rate.invalid {
								t.Run(fmt.Sprintf("invalid_frame_%d", frameSize), func(t *testing.T) {
									badResult := reader.I32()
									cRangeBefore := reader.U32()
									cRangeAfter := reader.U32()
									cProbeRecovery := reader.I32()
									cProbeRecoveryRange := reader.U32()
									cControlRecovery := reader.I32()
									cControlRecoveryRange := reader.U32()
									cRecoveryMatches := reader.U32()
									cPacketLen := int(reader.U32())
									cPacket := append([]byte(nil), reader.Bytes(cPacketLen)...)
									if badResult != -1 {
										t.Fatalf("libopus frame size %d returned %d, want OPUS_BAD_ARG (-1)", frameSize, badResult)
									}
									if cRangeBefore != cRangeAfter || cRecoveryMatches != 1 ||
										cProbeRecovery < 0 || cControlRecovery < 0 ||
										cProbeRecoveryRange != cControlRecoveryRange || int32(cPacketLen) != cControlRecovery {
										t.Fatalf("libopus invalid call changed state: ranges %08x/%08x recovery=(%d,%08x)/(%d,%08x) match=%d packet=%d",
											cRangeBefore, cRangeAfter, cProbeRecovery, cProbeRecoveryRange,
											cControlRecovery, cControlRecoveryRange, cRecoveryMatches, cPacketLen)
									}

									probe := newFrameSizeParityEncoder(t, rate.sampleRate, mode.restrictedSilk)
									control := newFrameSizeParityEncoder(t, rate.sampleRate, mode.restrictedSilk)
									primeSize := rate.sampleRate / 50
									primePCM := frameSizeProbeInput(sampleFormat, primeSize, 3)
									primePacket := make([]byte, 4000)
									primeN, primeErr := encodeFrameSizeProbe(probe, sampleFormat, primePCM, primeSize, primePacket)
									if primeErr != nil || primeN <= 0 {
										t.Fatalf("Go priming encode n=%d err=%v", primeN, primeErr)
									}
									if gotRange := probe.GetFinalRange(); gotRange != cRangeBefore {
										t.Fatalf("primed final range Go/C=%08x/%08x", gotRange, cRangeBefore)
									}
									controlPrime := make([]byte, 4000)
									if n, err := encodeFrameSizeProbe(control, sampleFormat, primePCM, primeSize, controlPrime); err != nil || n != primeN || !bytes.Equal(controlPrime[:n], primePacket[:primeN]) {
										t.Fatalf("Go control priming packet n=%d/%d err=%v", n, primeN, err)
									}

									badInput := frameSizeProbeInput(sampleFormat, maxInt(frameSize, 0), 11)
									badOut := []byte{0xa5}
									badN, badErr := encodeFrameSizeProbe(probe, sampleFormat, badInput, frameSize, badOut)
									if badN != 0 || !errors.Is(badErr, ErrInvalidInput) || badOut[0] != 0xa5 {
										t.Fatalf("Go invalid frame %d returned n=%d err=%v output=%x", frameSize, badN, badErr, badOut)
									}
									if gotRange := probe.GetFinalRange(); gotRange != cRangeBefore {
										t.Fatalf("invalid Go call changed final range %08x to %08x", cRangeBefore, gotRange)
									}

									recoveryPCM := frameSizeProbeInput(sampleFormat, primeSize, 29)
									recoveryPacket := make([]byte, 4000)
									recoveryN, recoveryErr := encodeFrameSizeProbe(probe, sampleFormat, recoveryPCM, primeSize, recoveryPacket)
									if recoveryErr != nil || recoveryN <= 0 {
										t.Fatalf("Go recovery encode n=%d err=%v", recoveryN, recoveryErr)
									}
									controlRecoveryPacket := make([]byte, 4000)
									controlN, controlErr := encodeFrameSizeProbe(control, sampleFormat, recoveryPCM, primeSize, controlRecoveryPacket)
									if controlErr != nil || controlN != recoveryN || !bytes.Equal(controlRecoveryPacket[:controlN], recoveryPacket[:recoveryN]) {
										t.Fatalf("Go invalid/recovery changed stream: probe n=%d err=%v control n=%d err=%v", recoveryN, recoveryErr, controlN, controlErr)
									}
									if !bytes.Equal(recoveryPacket[:recoveryN], cPacket) ||
										probe.GetFinalRange() != cProbeRecoveryRange ||
										control.GetFinalRange() != cControlRecoveryRange {
										t.Fatalf("recovery differs from live C: packet Go/C=%x/%x range Go/C=%08x/%08x control=%08x",
											recoveryPacket[:recoveryN], cPacket, probe.GetFinalRange(),
											cProbeRecoveryRange, control.GetFinalRange())
									}
								})
							}
						})
					}
				})
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeFrameSizeValidatorCoversAllRatesAndDurations(t *testing.T) {
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000, 96000} {
		legal := nativeFrameSizes(sampleRate)
		for _, frameSize := range legal {
			if !validNativeFrameSize(sampleRate, frameSize, false) {
				t.Errorf("normal application rejects %d samples at %d Hz", frameSize, sampleRate)
			}
			wantRestricted := frameSize >= sampleRate/100
			if got := validNativeFrameSize(sampleRate, frameSize, true); got != wantRestricted {
				t.Errorf("restricted SILK frame %d at %d Hz valid=%t want %t", frameSize, sampleRate, got, wantRestricted)
			}
		}
		for _, frameSize := range []int{-1, 0, sampleRate/400 - 1, sampleRate/400 + 1, 6*sampleRate/50 + 1, int(^uint(0) >> 1)} {
			if validNativeFrameSize(sampleRate, frameSize, false) {
				t.Errorf("normal application accepts invalid frame %d at %d Hz", frameSize, sampleRate)
			}
			if validNativeFrameSize(sampleRate, frameSize, true) {
				t.Errorf("restricted SILK application accepts invalid frame %d at %d Hz", frameSize, sampleRate)
			}
		}
	}

	maxInt := int(^uint(0) >> 1)
	if got, ok := checkedInterleavedSampleCount(maxInt/2+1, 2); ok {
		t.Fatalf("overflowing interleaved sample count returned %d", got)
	}
	if got, ok := checkedInterleavedSampleCount(960, 2); !ok || got != 1920 {
		t.Fatalf("checked sample count=%d,%t want 1920,true", got, ok)
	}
}

func TestMultistreamInvalidFrameSizesReturnBeforeBudgetAndInt16Conversion(t *testing.T) {
	enc, err := NewEncoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	maxInt := int(^uint(0) >> 1)
	for _, frameSize := range []int{119, 121, 4801, maxInt / 2, maxInt/2 + 1, maxInt} {
		out := []byte{0xa5}
		if n, encodeErr := enc.Encode(nil, frameSize, out); n != 0 || !errors.Is(encodeErr, ErrInvalidInput) || out[0] != 0xa5 {
			t.Fatalf("float frame %d returned n=%d err=%v output=%x", frameSize, n, encodeErr, out)
		}
		short := []int16{0}
		if n, encodeErr := enc.EncodeInt16WithAnalysis(short, frameSize, short, out); n != 0 || !errors.Is(encodeErr, ErrInvalidInput) || out[0] != 0xa5 {
			t.Fatalf("int16 frame %d returned n=%d err=%v output=%x", frameSize, n, encodeErr, out)
		}
		if len(enc.int16Scratch) != 0 || len(enc.int16CodedScratch) != 0 {
			t.Fatalf("invalid int16 frame %d allocated conversion scratch: analysis=%d coded=%d",
				frameSize, len(enc.int16Scratch), len(enc.int16CodedScratch))
		}
	}
	enc.SetRestrictedSilkApplication(true)
	for _, frameSize := range []int{120, 240} {
		if n, encodeErr := enc.Encode(nil, frameSize, []byte{0xa5}); n != 0 || !errors.Is(encodeErr, ErrInvalidInput) {
			t.Fatalf("restricted-SILK invalid frame %d returned n=%d err=%v", frameSize, n, encodeErr)
		}
	}
}

func buildNativeFrameSizeCases(sampleRates []int, restrictedSilk bool) []nativeFrameSizeRateCase {
	cases := make([]nativeFrameSizeRateCase, len(sampleRates))
	for i, sampleRate := range sampleRates {
		legal := nativeFrameSizes(sampleRate)
		if restrictedSilk {
			filtered := legal[:0]
			for _, frameSize := range legal {
				if frameSize >= sampleRate/100 {
					filtered = append(filtered, frameSize)
				}
			}
			legal = filtered
		}
		invalid := []int{-1, 0, sampleRate/400 - 1, sampleRate/400 + 1, 6*sampleRate/50 + 1}
		if restrictedSilk {
			invalid = append(invalid, sampleRate/400, sampleRate/200)
		}
		if sampleRate == 48000 {
			invalid = append(invalid, 119, 121, 4801)
		}
		cases[i] = nativeFrameSizeRateCase{
			sampleRate: sampleRate,
			legal:      append([]int(nil), legal...),
			invalid:    uniqueIntValues(invalid),
		}
	}
	return cases
}

func nativeFrameSizes(sampleRate int) []int {
	return []int{
		sampleRate / 400,
		sampleRate / 200,
		sampleRate / 100,
		sampleRate / 50,
		sampleRate / 25,
		3 * sampleRate / 50,
		4 * sampleRate / 50,
		5 * sampleRate / 50,
		6 * sampleRate / 50,
	}
}

func uniqueIntValues(values []int) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		found := false
		for _, existing := range result {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			result = append(result, value)
		}
	}
	return result
}

func int32s(values []int) []int32 {
	result := make([]int32, len(values))
	for i, value := range values {
		result[i] = int32(value)
	}
	return result
}

func frameSizeProbeInput(sampleFormat, frameSize, seed int) any {
	if frameSize <= 0 {
		if sampleFormat == 1 {
			return []int16(nil)
		}
		return []float32(nil)
	}
	if sampleFormat == 1 {
		pcm := make([]int16, frameSize)
		for i := range pcm {
			value := (i*97+seed*31)%2001 - 1000
			pcm[i] = int16(value * 16)
		}
		return pcm
	}
	pcm := make([]float32, frameSize)
	for i := range pcm {
		value := (i*97+seed*31)%2001 - 1000
		pcm[i] = float32(value) * float32(0.0001)
	}
	return pcm
}

func newFrameSizeParityEncoder(t *testing.T, sampleRate int, restrictedSilk bool) *Encoder {
	t.Helper()
	enc, err := NewEncoder(sampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatalf("NewEncoder(%d): %v", sampleRate, err)
	}
	enc.SetBitrate(64000)
	if restrictedSilk {
		enc.SetRestrictedSilkApplication(true)
		// Match the restricted-SILK application's mode directly instead of
		// relying on the default mode heuristic for these duration checks.
		enc.SetMode(internalencoder.ModeSILK)
	}
	return enc
}

func encodeFrameSizeProbe(enc *Encoder, sampleFormat int, pcm any, frameSize int, out []byte) (int, error) {
	if sampleFormat == 1 {
		return enc.EncodeInt16(pcm.([]int16), frameSize, out)
	}
	return enc.Encode(pcm.([]float32), frameSize, out)
}

func boolToU32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}
