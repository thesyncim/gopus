//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

// CELTFixedQEXTMainParams describes a raw Q8 CELT frame encoded by a
// FIXED_POINT + ENABLE_QEXT libopus build with QEXT runtime enabled.
type CELTFixedQEXTMainParams struct {
	Channels, StreamChannels, FrameSize int
	Start, End, MaxBytes                int
	Bitrate, Complexity, SampleRate     int
	VBR, ConstrainedVBR                 bool
	LSBDepth                            int
	LFE                                 bool
	Analysis                            CELTFixedQ8Analysis
	PCM                                 []int32
}

// CELTFixedQEXTMainRecord is the one-frame output from the independent
// combined fixed-point/QEXT libopus archive.
type CELTFixedQEXTMainRecord struct {
	Packet      []byte
	MainPacket  []byte
	QEXTPayload []byte
	MainRange   uint32
	FinalRange  uint32
	TellFrac    int32
	Tell        int32
	HasQEXT     bool
}

var celtFixedQEXTMainHelper HelperCache

func buildCELTFixedQEXTMainHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT main-payload oracle",
		OutputBase:   "gopus_libopus_celt_encode_fixed_qext_main",
		SourceFile:   "libopus_celt_encode_fixed_qext_main_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTMain compares one native CELT frame against the combined
// fixed-QEXT reference, exposing main and extension payload ranges separately.
func ProbeCELTFixedQEXTMain(p CELTFixedQEXTMainParams) (CELTFixedQEXTMainRecord, error) {
	var out CELTFixedQEXTMainRecord
	streamChannels := p.StreamChannels
	if streamChannels == 0 {
		streamChannels = p.Channels
	}
	validRate := p.SampleRate == 48000 || p.SampleRate == 96000
	validFrame := p.FrameSize == 240 || p.FrameSize == 480 || p.FrameSize == 960
	if p.SampleRate == 96000 && p.FrameSize == 1920 {
		validFrame = true
	}
	if !validRate || !validFrame || p.Channels < 1 || p.Channels > 2 ||
		streamChannels < 1 || streamChannels > p.Channels ||
		p.Start < 0 || p.Start >= p.End || p.End > 21 ||
		p.MaxBytes < 2 || p.MaxBytes > 1275 || (p.Bitrate != -1 && p.Bitrate <= 0) ||
		p.Complexity < 0 || p.Complexity > 10 || p.LSBDepth < 8 || p.LSBDepth > 24 ||
		len(p.PCM) != p.Channels*p.FrameSize {
		return out, fmt.Errorf("invalid fixed-QEXT CELT main-payload controls")
	}
	boolWord := func(v bool) uint32 {
		if v {
			return 1
		}
		return 0
	}
	payload := NewOraclePayloadVersion("GQXM", 2, uint32(p.Channels), uint32(streamChannels),
		uint32(p.FrameSize), uint32(p.Start), uint32(p.End), uint32(p.MaxBytes),
		uint32(int32(p.Bitrate)), uint32(p.Complexity), uint32(p.SampleRate),
		boolWord(p.VBR), boolWord(p.ConstrainedVBR), uint32(p.LSBDepth), boolWord(p.LFE))
	analysis := p.Analysis
	payload.U32(boolWord(analysis.Valid))
	for _, value := range [...]float32{analysis.Tonality, analysis.TonalitySlope, analysis.Noisiness,
		analysis.Activity, analysis.MusicProb, analysis.MusicProbMin, analysis.MusicProbMax} {
		payload.Float32(value)
	}
	payload.I32(analysis.Bandwidth)
	payload.Float32(analysis.ActivityProbability)
	payload.Float32(analysis.MaxPitchRatio)
	payload.Raw(analysis.LeakBoost[:])
	payload.I32s(p.PCM...)
	bin, err := celtFixedQEXTMainHelper.Path(buildCELTFixedQEXTMainHelper)
	if err != nil {
		return out, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT main-payload", "GQXO")
	if err != nil {
		return out, err
	}
	reader.Count(1)
	n := int(reader.U32())
	out.MainRange = reader.U32()
	out.FinalRange = reader.U32()
	hasQEXT := reader.U32()
	if hasQEXT > 1 {
		return out, fmt.Errorf("invalid fixed-QEXT side-payload flag %d", hasQEXT)
	}
	out.HasQEXT = hasQEXT != 0
	out.TellFrac = int32(reader.U32())
	out.Tell = int32(reader.U32())
	if n < 0 || n > p.MaxBytes {
		return out, fmt.Errorf("fixed-QEXT CELT packet size %d exceeds capacity %d", n, p.MaxBytes)
	}
	out.Packet = append([]byte(nil), reader.Bytes(n)...)
	out.MainPacket = out.Packet
	if out.HasQEXT {
		if len(out.Packet) < 3 || out.Packet[0] != 0x41 {
			return CELTFixedQEXTMainRecord{}, fmt.Errorf("fixed-QEXT flag set without padding framing")
		}
		qextBytes, padBytes := 0, 0
		for i := 1; i < len(out.Packet); i++ {
			v := int(out.Packet[i])
			padBytes++
			if v == 255 {
				qextBytes += 254
				continue
			}
			qextBytes += v
			break
		}
		mainStart := 1 + padBytes
		mainLen := len(out.Packet) - mainStart - qextBytes
		if qextBytes < 21 || padBytes != (qextBytes+253)/254 || mainLen < 0 {
			return CELTFixedQEXTMainRecord{}, fmt.Errorf("malformed fixed-QEXT side framing")
		}
		out.MainPacket = out.Packet[mainStart : mainStart+mainLen]
		out.QEXTPayload = out.Packet[mainStart+mainLen:]
	}
	if err := reader.ExpectConsumed(); err != nil {
		return out, err
	}
	return out, nil
}
