package libopustest

import "fmt"

const (
	qextDecode96kInputMagic  = "GQDI"
	qextDecode96kOutputMagic = "GQDO"

	// QEXTDecode96kFormatFloat32 selects opus_decode_float output.
	QEXTDecode96kFormatFloat32 = uint32(0)
	// QEXTDecode96kFormatInt16 selects opus_decode output.
	QEXTDecode96kFormatInt16 = uint32(1)
	// QEXTDecode96kFormatInt24 selects opus_decode24 output.
	QEXTDecode96kFormatInt24 = uint32(2)
)

var qextDecode96kHelper HelperCache
var qextDecode96kFixedHelper HelperCache

func buildQEXTDecode96kHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "qext decode96k",
		OutputBase:  "gopus_libopus_qext_decode96k",
		SourceFile:  "libopus_qext_decode96k_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func buildQEXTDecode96kFixedHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed qext decode96k",
		OutputBase:   "gopus_libopus_fixed_qext_decode96k",
		SourceFile:   "libopus_qext_decode96k_info.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes:  []string{"celt", "silk"},
		FixedQEXTRef: true,
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func getQEXTDecode96kHelperPath() (string, error) {
	return qextDecode96kHelper.Path(buildQEXTDecode96kHelper)
}

// QEXTDecode96kParams configures a native 96 kHz QEXT full-packet decode probe.
// The reference decoder is created with opus_decoder_create(96000, channels),
// which under ENABLE_QEXT runs the native 96 kHz CELT mode plus the >20 kHz
// extension-band decode chain.
type QEXTDecode96kParams struct {
	SampleFormat           uint32 // QEXTDecode96kFormat* (float32/int16/int24)
	Channels               int
	SampleRate             int      // explicit native rate for selected fixed-QEXT probes
	PhaseInversionDisabled bool     // explicit control for version-5 selected fixed-QEXT probes
	MaxFrameSize           int      // per-channel sample capacity passed to opus_decode (96 kHz)
	GainQ8                 int32    // decoder output gain for version-2 probes; zero for version 1
	PacketFormats          []uint32 // per-packet int16/int24 formats for mixed-format probes
	Packets                [][]byte // Opus packets to decode in sequence through one decoder
}

// QEXTDecode96kResult holds the decoded native 96 kHz PCM and per-packet final
// range. For float32 output PCM is populated; Int16/Int24 carry the integer
// formats. Exactly one of the three is non-nil depending on SampleFormat.
type QEXTDecode96kResult struct {
	PCM         []float32
	Int16       []int16
	Int24       []int32
	MixedInt32  []int32 // mixed-format output; int16 samples are sign-extended
	FinalRanges []uint32
}

// ProbeQEXTDecode96k decodes the supplied Opus packets through the QEXT-enabled
// libopus reference at Fs=96000 and returns the native 96 kHz PCM (interleaved)
// plus the per-packet OPUS_GET_FINAL_RANGE values.
func ProbeQEXTDecode96k(p QEXTDecode96kParams) (QEXTDecode96kResult, error) {
	binPath, err := getQEXTDecode96kHelperPath()
	if err != nil {
		return QEXTDecode96kResult{}, err
	}
	version := uint32(1)
	if p.GainQ8 != 0 {
		version = 2
	}
	return probeQEXTDecode96k(p, binPath, version)
}

// ProbeQEXTDecode96kMixed decodes packets through one persistent QEXT-enabled
// decoder while alternating opus_decode and opus_decode24. MixedInt32 stores
// both formats as int32 values, sign-extending opus_decode output.
func ProbeQEXTDecode96kMixed(p QEXTDecode96kParams) (QEXTDecode96kResult, error) {
	return probeQEXTDecode96kMixed(p, getQEXTDecode96kHelperPath)
}

// ProbeQEXTDecode96kFixedMixed uses the selected FIXED_POINT+ENABLE_QEXT
// archive for the same mixed opus_decode/opus_decode24 sequence.
func ProbeQEXTDecode96kFixedMixed(p QEXTDecode96kParams) (QEXTDecode96kResult, error) {
	return probeQEXTDecode96kMixed(p, func() (string, error) {
		return qextDecode96kFixedHelper.Path(buildQEXTDecode96kFixedHelper)
	})
}

func probeQEXTDecode96kMixed(p QEXTDecode96kParams, helper func() (string, error)) (QEXTDecode96kResult, error) {
	if len(p.PacketFormats) != len(p.Packets) {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k mixed: %d formats for %d packets", len(p.PacketFormats), len(p.Packets))
	}
	for i, format := range p.PacketFormats {
		if format != QEXTDecode96kFormatInt16 && format != QEXTDecode96kFormatInt24 {
			return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k mixed: packet %d has unsupported format %d", i, format)
		}
	}
	binPath, err := helper()
	if err != nil {
		return QEXTDecode96kResult{}, err
	}
	p.SampleFormat = QEXTDecode96kFormatInt24
	return probeQEXTDecode96k(p, binPath, 3)
}

// ProbeQEXTDecode96kFixed decodes packets through the selected
// FIXED_POINT+ENABLE_QEXT libopus reference. Version 2 adds an explicit output
// gain control while retaining the version-1 packet and result layout.
func ProbeQEXTDecode96kFixed(p QEXTDecode96kParams) (QEXTDecode96kResult, error) {
	binPath, err := qextDecode96kFixedHelper.Path(buildQEXTDecode96kFixedHelper)
	if err != nil {
		return QEXTDecode96kResult{}, err
	}
	return probeQEXTDecode96k(p, binPath, 2)
}

// ProbeQEXTDecodeFixed decodes a received sequence through the selected
// FIXED_POINT+ENABLE_QEXT reference at 48 or 96 kHz. Protocol v5 carries the
// API sample rate and phase-inversion control explicitly.
func ProbeQEXTDecodeFixed(p QEXTDecode96kParams) (QEXTDecode96kResult, error) {
	if p.SampleRate != 48000 && p.SampleRate != 96000 {
		return QEXTDecode96kResult{}, fmt.Errorf("fixed qext decode: unsupported sample rate %d", p.SampleRate)
	}
	binPath, err := qextDecode96kFixedHelper.Path(buildQEXTDecode96kFixedHelper)
	if err != nil {
		return QEXTDecode96kResult{}, err
	}
	return probeQEXTDecode96k(p, binPath, 5)
}

func probeQEXTDecode96k(p QEXTDecode96kParams, binPath string, version uint32) (QEXTDecode96kResult, error) {
	if p.Channels < 1 || p.Channels > 2 {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k: invalid channels %d", p.Channels)
	}
	if p.MaxFrameSize <= 0 {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k: invalid maxFrameSize %d", p.MaxFrameSize)
	}

	payload := NewOraclePayloadVersion(qextDecode96kInputMagic, version)
	payload.U32(p.SampleFormat)
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.MaxFrameSize))
	payload.U32(uint32(len(p.Packets)))
	if version >= 2 {
		payload.I32(p.GainQ8)
	}
	if version >= 4 {
		payload.U32(uint32(p.SampleRate))
	}
	if version == 5 {
		if p.PhaseInversionDisabled {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
	}
	for i, pkt := range p.Packets {
		if version == 3 {
			payload.U32(p.PacketFormats[i])
		}
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}

	reader, err := RunOracleVersion(binPath, payload.Bytes(), "qext decode96k", qextDecode96kOutputMagic, version)
	if err != nil {
		return QEXTDecode96kResult{}, err
	}

	total := int(reader.U32())
	var res QEXTDecode96kResult
	if version == 3 {
		res.MixedInt32 = make([]int32, total)
		for i := range res.MixedInt32 {
			res.MixedInt32[i] = reader.I32()
		}
	} else {
		switch p.SampleFormat {
		case QEXTDecode96kFormatInt16:
			res.Int16 = make([]int16, total)
			for i := range res.Int16 {
				res.Int16[i] = reader.I16()
			}
		case QEXTDecode96kFormatInt24:
			res.Int24 = make([]int32, total)
			for i := range res.Int24 {
				res.Int24[i] = reader.I32()
			}
		default:
			res.PCM = make([]float32, total)
			for i := range res.PCM {
				res.PCM[i] = reader.Float32()
			}
		}
	}

	nRanges := int(reader.U32())
	res.FinalRanges = make([]uint32, nRanges)
	for i := range res.FinalRanges {
		res.FinalRanges[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return QEXTDecode96kResult{}, fmt.Errorf("qext decode96k oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return QEXTDecode96kResult{}, err
	}
	return res, nil
}
