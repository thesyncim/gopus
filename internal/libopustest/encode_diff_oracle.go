package libopustest

import "fmt"

const (
	encodeDiffInputMagic   = "GEDI"
	encodeDiffOutputMagic  = "GEDO"
	encodeDiffBatchMagic   = "GEDB"
	encodeDiffBatchVersion = 1
	encodeDiffMaxBatch     = 64
)

// EncodeDiff force-mode codes (opus_private.h MODE_* consumed by
// OPUS_SET_FORCE_MODE). Zero leaves the mode auto.
const (
	EncodeDiffForceModeAuto     = 0
	EncodeDiffForceModeSILKOnly = 1000
	EncodeDiffForceModeHybrid   = 1001
	EncodeDiffForceModeCELTOnly = 1002
)

// EncodeDiff bandwidth codes (OPUS_BANDWIDTH_*). Zero leaves the bandwidth
// unset (encoder auto-selects).
const (
	EncodeDiffBandwidthAuto          = 0
	EncodeDiffBandwidthNarrowband    = 1101
	EncodeDiffBandwidthMediumband    = 1102
	EncodeDiffBandwidthWideband      = 1103
	EncodeDiffBandwidthSuperwideband = 1104
	EncodeDiffBandwidthFullband      = 1105
)

// EncodeDiff signal codes (OPUS_SIGNAL_*). OPUS_AUTO (-1000) is the two's
// complement uint32.
const (
	EncodeDiffSignalAuto  = uint32(0xFFFFFC18) // OPUS_AUTO = -1000
	EncodeDiffSignalVoice = uint32(3001)
	EncodeDiffSignalMusic = uint32(3002)
)

// EncodeDiff application codes (OPUS_APPLICATION_*).
const (
	EncodeDiffApplicationVoIP         = 2048
	EncodeDiffApplicationAudio        = 2049
	EncodeDiffApplicationRestrictedLD = 2051
)

var encodeDiffHelper HelperCache

func buildEncodeDiffHelper() (string, error) {
	return BuildPublicAPIHelper(CHelperConfig{
		Label:       "public opus encode diff",
		OutputBase:  "gopus_libopus_encode_diff",
		SourceFile:  "libopus_encode_diff_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		DeadStrip:   true,
	})
}

func getEncodeDiffHelperPath() (string, error) {
	return encodeDiffHelper.Path(buildEncodeDiffHelper)
}

// EncodeDiffHelperPath returns the encode-diff oracle for the active public
// codec configuration, building it on first use before a test sweep.
func EncodeDiffHelperPath() (string, error) {
	return getEncodeDiffHelperPath()
}

// EncodeDiffParams configures the opus_encode_float input API oracle. The C
// codec uses the same fixed/float, feature and instruction configuration as Go.
// One encoder is driven statefully across all frames without a reset.
type EncodeDiffParams struct {
	SampleRate         int
	Channels           int
	Application        int
	ForceMode          int
	Bandwidth          int
	MaxBandwidth       int
	Bitrate            int
	Complexity         int
	Signal             uint32
	VBR                bool
	VBRConstraint      bool
	ForceChannels      int
	InbandFEC          int
	PacketLoss         int
	DTX                bool
	LSBDepth           int
	PredictionDisabled bool
	PhaseInvDisabled   bool
	FrameSize          int // per-channel samples at SampleRate
	FrameCount         int
	// PCM is interleaved float32, length FrameSize*Channels*FrameCount.
	PCM []float32
}

// EncodeDiffRecord is one frame's oracle result: opus_encode_float's return code
// Ret (>0 packet len, 1 = DTX/CELT-silence TOC-only, 0 = DTX no-output, <0 error),
// the post-encode final range, and the produced packet bytes (nil when Ret<=0).
type EncodeDiffRecord struct {
	Ret        int
	FinalRange uint32
	Packet     []byte
}

// ProbeEncodeDiff passes float PCM to libopus opus_encode_float and returns
// per-frame records. BuildPublicAPIHelper pairs the C codec with the current Go
// build; float input does not imply a floating-point codec implementation.
func ProbeEncodeDiff(p EncodeDiffParams) ([]EncodeDiffRecord, error) {
	binPath, err := getEncodeDiffHelperPath()
	if err != nil {
		return nil, err
	}
	if err := validateEncodeDiffParams(p); err != nil {
		return nil, err
	}

	payload := &OraclePayload{}
	appendEncodeDiffProgram(payload, p)
	reader, err := RunOracle(binPath, payload.Bytes(), "opus encode diff", encodeDiffOutputMagic)
	if err != nil {
		return nil, err
	}
	recs := readEncodeDiffRecords(reader, p.FrameCount)
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("encode diff oracle payload not fully consumed: %w", err)
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

// ProbeEncodeDiffBatch runs each stateful encode sequence with a fresh libopus
// encoder while sharing one helper process. Each sequence retains its own
// encoder state across the frames in its EncodeDiffParams.
func ProbeEncodeDiffBatch(programs []EncodeDiffParams) ([][]EncodeDiffRecord, error) {
	if len(programs) == 0 {
		return nil, nil
	}
	if len(programs) > encodeDiffMaxBatch {
		return nil, fmt.Errorf("encode diff: batch has %d programs, maximum is %d", len(programs), encodeDiffMaxBatch)
	}
	for i, p := range programs {
		if err := validateEncodeDiffParams(p); err != nil {
			return nil, fmt.Errorf("encode diff batch program %d: %w", i, err)
		}
	}

	binPath, err := getEncodeDiffHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayloadVersion(encodeDiffBatchMagic, encodeDiffBatchVersion, uint32(len(programs)))
	for _, p := range programs {
		appendEncodeDiffProgram(payload, p)
	}
	data, err := RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run encode diff batch helper: %w", err)
	}
	return parseEncodeDiffBatchResults(data, programs)
}

func validateEncodeDiffParams(p EncodeDiffParams) error {
	if p.Channels < 1 || p.Channels > 2 {
		return fmt.Errorf("encode diff: invalid channels %d", p.Channels)
	}
	if p.FrameSize <= 0 || p.FrameCount <= 0 {
		return fmt.Errorf("encode diff: invalid dimensions")
	}
	nsamples := p.FrameSize * p.Channels * p.FrameCount
	if len(p.PCM) != nsamples {
		return fmt.Errorf("encode diff: PCM len %d want %d", len(p.PCM), nsamples)
	}
	return nil
}

func appendEncodeDiffProgram(payload *OraclePayload, p EncodeDiffParams) {
	b2u := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}
	nsamples := p.FrameSize * p.Channels * p.FrameCount
	payload.Magic(encodeDiffInputMagic)
	payload.U32(1)
	payload.U32(uint32(p.SampleRate))
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.Application))
	payload.U32(uint32(p.ForceMode))
	payload.U32(uint32(p.Bandwidth))
	payload.U32(uint32(p.MaxBandwidth))
	payload.U32(uint32(p.Bitrate))
	payload.U32(uint32(p.Complexity))
	payload.U32(p.Signal)
	payload.U32(b2u(p.VBR))
	payload.U32(b2u(p.VBRConstraint))
	payload.U32(uint32(p.ForceChannels))
	payload.U32(uint32(p.InbandFEC))
	payload.U32(uint32(p.PacketLoss))
	payload.U32(b2u(p.DTX))
	payload.U32(uint32(p.LSBDepth))
	payload.U32(b2u(p.PredictionDisabled))
	payload.U32(b2u(p.PhaseInvDisabled))
	payload.U32(uint32(p.FrameSize))
	payload.U32(uint32(p.FrameCount))
	payload.U32(uint32(nsamples))
	payload.Float32s(p.PCM...)
}

func readEncodeDiffRecords(reader *OracleReader, frameCount int) []EncodeDiffRecord {
	n := reader.Count(frameCount)
	recs := make([]EncodeDiffRecord, n)
	for i := range n {
		ret := int(int32(reader.U32()))
		fr := reader.U32()
		plen := int(reader.U32())
		var pkt []byte
		if plen > 0 {
			pkt = append([]byte(nil), reader.Bytes(plen)...)
			if pad := (4 - plen%4) % 4; pad > 0 {
				reader.Bytes(pad)
			}
		}
		recs[i] = EncodeDiffRecord{Ret: ret, FinalRange: fr, Packet: pkt}
	}
	return recs
}

func parseEncodeDiffBatchResults(data []byte, programs []EncodeDiffParams) ([][]EncodeDiffRecord, error) {
	reader, version, err := NewOracleReaderVersion("opus encode diff batch", encodeDiffBatchMagic, data)
	if err != nil {
		return nil, err
	}
	if version != encodeDiffBatchVersion {
		return nil, fmt.Errorf("opus encode diff batch helper version=%d want %d", version, encodeDiffBatchVersion)
	}
	n := reader.Count(len(programs))
	results := make([][]EncodeDiffRecord, n)
	for i := range n {
		if got := string(reader.Bytes(4)); reader.Err() != nil {
			return nil, reader.Err()
		} else if got != encodeDiffOutputMagic {
			return nil, fmt.Errorf("encode diff batch program %d: unexpected result magic %q", i, got)
		}
		programVersion := reader.U32()
		if programVersion != 1 {
			return nil, fmt.Errorf("encode diff batch program %d helper version=%d want 1", i, programVersion)
		}
		results[i] = readEncodeDiffRecords(reader, programs[i].FrameCount)
		if err := reader.Err(); err != nil {
			return nil, fmt.Errorf("encode diff batch program %d: %w", i, err)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("encode diff batch oracle payload not fully consumed: %w", err)
	}
	return results, nil
}
