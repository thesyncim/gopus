package libopustest

import (
	"fmt"
	"math"
)

// CELTFixedQ8Analysis is celt/celt.h AnalysisInfo on the selected fixed build.
// Every field is transmitted, including fields that the current CELT frame
// does not consume, so the oracle has the same state as CELT_SET_ANALYSIS.
type CELTFixedQ8Analysis struct {
	Valid                                        bool
	Tonality, TonalitySlope, Noisiness, Activity float32
	MusicProb, MusicProbMin, MusicProbMax        float32
	Bandwidth                                    int32
	ActivityProbability, MaxPitchRatio           float32
	LeakBoost                                    [19]uint8
}

type CELTFixedQ8Frame struct {
	PCM        []int32
	MaxBytes   int
	Analysis   CELTFixedQ8Analysis
	EnergyMask []int32
}

type CELTFixedQ8Params struct {
	SampleRate, Channels, FrameSize, Start, End int
	Bitrate, Complexity, LSBDepth               int
	VBR, ConstrainedVBR, LFE                    bool
	Frames                                      []CELTFixedQ8Frame
}

type CELTFixedQ8Record struct {
	Packet     []byte
	FinalRange uint32
}

var celtFixedQ8Helper HelperCache

func buildCELTFixedQ8Helper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "selected fixed CELT raw Q8 encode",
		OutputBase:  "gopus_libopus_celt_encode_fixed_q8",
		SourceFile:  "libopus_celt_encode_fixed_q8_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func ProbeCELTFixedRawQ8(p CELTFixedQ8Params) ([]CELTFixedQ8Record, error) {
	validRate := false
	for _, rate := range [...]int{8000, 12000, 16000, 24000, 48000} {
		validRate = validRate || p.SampleRate == rate
	}
	if !validRate || p.Channels < 1 || p.Channels > 2 || p.FrameSize <= 0 || p.FrameSize > 960 ||
		p.Start < 0 || p.Start >= p.End || p.End > 21 ||
		p.Complexity < 0 || p.Complexity > 10 || p.LSBDepth < 8 || p.LSBDepth > 24 ||
		len(p.Frames) < 1 || len(p.Frames) > 256 ||
		(p.Bitrate != -1 && p.Bitrate <= 0) {
		return nil, fmt.Errorf("invalid fixed CELT Q8 controls")
	}
	core := p.FrameSize * (48000 / p.SampleRate)
	if core != 120 && core != 240 && core != 480 && core != 960 {
		return nil, fmt.Errorf("fixed CELT Q8 core frame size %d", core)
	}
	perFrame := p.FrameSize * p.Channels
	hasMask := len(p.Frames[0].EnergyMask) != 0
	for f, frame := range p.Frames {
		if len(frame.PCM) != perFrame || frame.MaxBytes < 2 || frame.MaxBytes > 1275 ||
			(hasMask && len(frame.EnergyMask) != p.Channels*21) ||
			(!hasMask && len(frame.EnergyMask) != 0) {
			return nil, fmt.Errorf("invalid fixed CELT Q8 frame %d", f)
		}
	}
	bin, err := celtFixedQ8Helper.Path(buildCELTFixedQ8Helper)
	if err != nil {
		return nil, err
	}
	b2u := func(v bool) uint32 {
		if v {
			return 1
		}
		return 0
	}
	payload := NewOraclePayload("GQRI", uint32(p.Channels), uint32(p.FrameSize),
		uint32(p.Start), uint32(p.End), uint32(int32(p.Bitrate)), uint32(p.Complexity),
		uint32(p.SampleRate), b2u(p.VBR), b2u(p.ConstrainedVBR), b2u(p.LFE),
		uint32(p.LSBDepth), uint32(len(p.Frames)), b2u(hasMask))
	for _, frame := range p.Frames {
		payload.U32(uint32(frame.MaxBytes))
		payload.U32(uint32(perFrame))
		payload.I32s(frame.PCM...)
		a := frame.Analysis
		payload.U32(b2u(a.Valid))
		for _, value := range [...]float32{a.Tonality, a.TonalitySlope, a.Noisiness,
			a.Activity, a.MusicProb, a.MusicProbMin, a.MusicProbMax} {
			payload.U32(math.Float32bits(value))
		}
		payload.I32(a.Bandwidth)
		payload.Float32(a.ActivityProbability)
		payload.Float32(a.MaxPitchRatio)
		payload.Raw(a.LeakBoost[:])
		if hasMask {
			payload.I32s(frame.EnergyMask...)
		}
	}
	reader, err := RunOracle(bin, payload.Bytes(), "selected fixed CELT raw Q8 encode", "GQRO")
	if err != nil {
		return nil, err
	}
	reader.Count(len(p.Frames))
	out := make([]CELTFixedQ8Record, len(p.Frames))
	for i := range out {
		n := int(reader.U32())
		out[i].FinalRange = reader.U32()
		if n < 0 || n > p.Frames[i].MaxBytes {
			return nil, fmt.Errorf("fixed CELT Q8 frame %d packet size %d", i, n)
		}
		out[i].Packet = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
