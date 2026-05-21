package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKResamplerInputMagic  = "GSRI"
	libopusSILKResamplerOutputMagic = "GSRO"
)

var libopusSILKResamplerHelper libopustest.HelperCache

type libopusSILKResamplerRecord struct {
	fsIn   int
	fsOut  int
	forEnc bool
	frames [][]int16
}

func buildLibopusSILKResamplerHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk resampler",
		OutputBase:   "gopus_libopus_silk_resampler",
		SourceFile:   "libopus_silk_resampler_info.c",
		ProbeRelPath: "silk/SigProc_FIX.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources: []string{
			"silk/resampler.c",
			"silk/resampler_private_IIR_FIR.c",
			"silk/resampler_private_up2_HQ.c",
			"silk/resampler_private_down_FIR.c",
			"silk/resampler_private_AR2.c",
			"silk/resampler_rom.c",
		},
	})
}

func getLibopusSILKResamplerHelperPath() (string, error) {
	return libopusSILKResamplerHelper.Path(buildLibopusSILKResamplerHelper)
}

func probeLibopusSILKResampler(records []libopusSILKResamplerRecord) ([][]int16, error) {
	binPath, err := getLibopusSILKResamplerHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKResamplerInputMagic, uint32(len(records)))
	for _, record := range records {
		if len(record.frames) == 0 {
			return nil, fmt.Errorf("empty resampler record")
		}
		frameSamples := len(record.frames[0])
		payload.U32(uint32(record.fsIn))
		payload.U32(uint32(record.fsOut))
		if record.forEnc {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(frameSamples))
		payload.U32(uint32(len(record.frames)))
		for _, frame := range record.frames {
			if len(frame) != frameSamples {
				return nil, fmt.Errorf("mixed frame lengths in %d->%d record", record.fsIn, record.fsOut)
			}
			for _, sample := range frame {
				payload.I16(sample)
			}
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk resampler", libopusSILKResamplerOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	out := make([][]int16, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]int16, n)
		for j := range out[i] {
			out[i][j] = reader.I16()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKEncoderDownsamplingResamplerMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKResamplerRecord{
		{fsIn: 24000, fsOut: 8000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x0f1e2d3c)},
		{fsIn: 24000, fsOut: 12000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x10293847)},
		{fsIn: 24000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(240, 6, 0x55667788)},
		{fsIn: 48000, fsOut: 8000, forEnc: true, frames: makeSILKResamplerFrames(480, 6, 0x89abcdef)},
		{fsIn: 48000, fsOut: 12000, forEnc: true, frames: makeSILKResamplerFrames(480, 6, 0xdecafbad)},
		{fsIn: 48000, fsOut: 16000, forEnc: true, frames: makeSILKResamplerFrames(480, 6, 0xc001d00d)},
	}
	want, err := probeLibopusSILKResampler(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk encoder resampler", err)
	}

	for recIdx, record := range records {
		t.Run(fmt.Sprintf("%d_to_%d_%dms", record.fsIn, record.fsOut, len(record.frames[0])*1000/record.fsIn), func(t *testing.T) {
			resampler := NewDownsamplingResampler(record.fsIn, record.fsOut)
			frameOut := len(record.frames[0]) * record.fsOut / record.fsIn
			out := make([]int16, frameOut)
			wantOffset := 0
			for frameIdx, frame := range record.frames {
				clear(out)
				resampler.processWithDelay(out, frame)
				for i, got := range out {
					wantSample := want[recIdx][wantOffset+i]
					if got != wantSample {
						t.Fatalf("frame %d sample %d got %d want %d", frameIdx, i, got, wantSample)
					}
				}
				wantOffset += frameOut
			}
			if wantOffset != len(want[recIdx]) {
				t.Fatalf("checked %d output samples want %d", wantOffset, len(want[recIdx]))
			}
		})
	}
}

func makeSILKResamplerFrames(frameSamples, frameCount int, seed uint32) [][]int16 {
	frames := make([][]int16, frameCount)
	for frame := range frames {
		samples := make([]int16, frameSamples)
		for i := range samples {
			seed = 1664525*seed + 1013904223
			v := int32(seed>>16) - 32768
			v = v/2 + int32((frame*37+i*11)%2048) - 1024
			if i%31 == 0 {
				v = int32((frame+i)%5-2) * 12000
			}
			if v > 30000 {
				v = 30000
			} else if v < -30000 {
				v = -30000
			}
			samples[i] = int16(v)
		}
		frames[frame] = samples
	}
	return frames
}

func TestSILKDecoderResamplerProcessInt16IntoMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	records := []libopusSILKResamplerRecord{
		{fsIn: 8000, fsOut: 48000, frames: makeSILKResamplerFrames(80, 8, 0x13579bdf)},
		{fsIn: 8000, fsOut: 48000, frames: makeSILKResamplerFrames(160, 5, 0x2468ace0)},
		{fsIn: 8000, fsOut: 8000, frames: makeSILKResamplerFrames(160, 5, 0x0f1e2d3c)},
		{fsIn: 12000, fsOut: 48000, frames: makeSILKResamplerFrames(120, 8, 0x10203040)},
		{fsIn: 12000, fsOut: 8000, frames: makeSILKResamplerFrames(240, 5, 0x45670123)},
		{fsIn: 12000, fsOut: 12000, frames: makeSILKResamplerFrames(240, 5, 0x76543210)},
		{fsIn: 16000, fsOut: 48000, frames: makeSILKResamplerFrames(160, 8, 0x55667788)},
		{fsIn: 16000, fsOut: 48000, frames: makeSILKResamplerFrames(320, 5, 0xa5a55a5a)},
		{fsIn: 16000, fsOut: 8000, frames: makeSILKResamplerFrames(320, 5, 0x31415926)},
		{fsIn: 16000, fsOut: 12000, frames: makeSILKResamplerFrames(320, 5, 0x27182818)},
		{fsIn: 16000, fsOut: 16000, frames: makeSILKResamplerFrames(320, 5, 0xabcdef01)},
		{fsIn: 16000, fsOut: 24000, frames: makeSILKResamplerFrames(320, 5, 0x1234fedc)},
	}
	want, err := probeLibopusSILKResampler(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk resampler", err)
	}

	for recIdx, record := range records {
		t.Run(fmt.Sprintf("%d_to_%d_%dms", record.fsIn, record.fsOut, len(record.frames[0])*1000/record.fsIn), func(t *testing.T) {
			resampler := NewLibopusResampler(record.fsIn, record.fsOut)
			frameOut := len(record.frames[0]) * record.fsOut / record.fsIn
			out := make([]float32, frameOut)
			wantOffset := 0
			for frameIdx, frame := range record.frames {
				n := resampler.ProcessInt16Into(frame, out)
				if n != frameOut {
					t.Fatalf("frame %d output samples=%d want %d", frameIdx, n, frameOut)
				}
				for i := 0; i < n; i++ {
					wantSample := want[recIdx][wantOffset+i]
					wantFloat := float32(wantSample) * (1.0 / 32768.0)
					if math.Float32bits(out[i]) != math.Float32bits(wantFloat) {
						t.Fatalf("frame %d sample %d got %08x(%0.10g) want %08x(%0.10g) from int16 %d",
							frameIdx, i,
							math.Float32bits(out[i]), out[i],
							math.Float32bits(wantFloat), wantFloat,
							wantSample)
					}
				}
				wantOffset += n
			}
			if wantOffset != len(want[recIdx]) {
				t.Fatalf("checked %d output samples want %d", wantOffset, len(want[recIdx]))
			}
		})
	}
}
