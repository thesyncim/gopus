package libopustest

import "fmt"

const (
	ctlSequenceInputMagic   = "GCTI"
	ctlSequenceOutputMagic  = "GCTO"
	ctlSequenceBatchMagic   = "GCTB"
	ctlSequenceBatchVersion = 1
	ctlSequenceMaxBatch     = 256
)

// CTL sequence opcodes. Values are in the libopus argument domain.
const (
	CTLOpSet     = 0
	CTLOpGet     = 1
	CTLOpProcess = 2
	CTLOpReset   = 3
)

// CTLOp is one step in a CTL behavioral program. Request is the libopus request
// code (e.g. OPUS_SET_BITRATE_REQUEST); Arg is the SET argument (ignored for
// GET / PROCESS / RESET).
type CTLOp struct {
	Op      int
	Request int32
	Arg     int32
}

// CTLResult is the oracle outcome of one CTLOp: the CTL/process return code, the
// GET value (only meaningful when HaveValue), and whether a value was produced.
type CTLResult struct {
	Ret       int32
	Value     int32
	HaveValue bool
	// Packet contains the encoded packet for OP_PROCESS when CapturePackets is
	// enabled on the sequence. Other operations and decoder processes return nil.
	Packet []byte
}

// CTLSequenceParams configures one oracle run against a single encoder or
// decoder driven through the supplied program.
type CTLSequenceParams struct {
	IsDecoder   bool
	SampleRate  int
	Channels    int
	Application int // libopus OPUS_APPLICATION_* (encoder only)
	FrameSize   int // per-channel samples per OP_PROCESS frame (0 => 960)
	// FeedPacket is the exact Opus packet the decoder OP_PROCESS decodes. The
	// same bytes must be decoded by gopus so decode-derived GETs are comparable.
	// Ignored for the encoder.
	FeedPacket []byte
	// CapturePackets asks the helper to return packet bytes for encoder OP_PROCESS
	// operations. It adds per-operation packet data to the oracle wire response.
	CapturePackets bool
	Ops            []CTLOp
}

var ctlSequenceHelper HelperCache

func buildCTLSequenceHelper() (string, error) {
	return BuildPublicAPIHelper(CHelperConfig{
		Label:       "opus ctl sequence",
		OutputBase:  "gopus_libopus_ctl_sequence",
		SourceFile:  "libopus_ctl_sequence_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		DeadStrip:   true,
	})
}

// CTLSequenceHelperPath returns the built CTL-sequence oracle path (building it
// on first use) so callers can detect availability before running the sweep.
func CTLSequenceHelperPath() (string, error) {
	return ctlSequenceHelper.Path(buildCTLSequenceHelper)
}

// ProbeCTLSequence drives one libopus encoder or decoder through the program in
// p.Ops and returns one CTLResult per op. This is the behavioral oracle for the
// public gopus typed CTL setters/getters.
func ProbeCTLSequence(p CTLSequenceParams) ([]CTLResult, error) {
	binPath, err := CTLSequenceHelperPath()
	if err != nil {
		return nil, err
	}
	if p.Channels < 1 || p.Channels > 2 {
		return nil, fmt.Errorf("ctl sequence: invalid channels %d", p.Channels)
	}

	version := ctlSequenceVersion(p)
	payload := &OraclePayload{}
	appendCTLSequenceProgram(payload, p)
	reader, err := RunOracleVersion(binPath, payload.Bytes(), "opus ctl sequence", ctlSequenceOutputMagic, version)
	if err != nil {
		return nil, err
	}
	out, err := readCTLSequenceResults(reader, version, p.Ops)
	if err != nil {
		return nil, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("ctl sequence oracle payload not fully consumed: %w", err)
	}
	return out, nil
}

// ProbeCTLSequenceBatch runs each CTL program in a fresh libopus state instance
// while sharing one helper process. Every program creates and destroys its own
// encoder or decoder, so controls and reset history do not cross boundaries.
func ProbeCTLSequenceBatch(programs []CTLSequenceParams) ([][]CTLResult, error) {
	if len(programs) == 0 {
		return nil, nil
	}
	if len(programs) > ctlSequenceMaxBatch {
		return nil, fmt.Errorf("ctl sequence: batch has %d programs, maximum is %d", len(programs), ctlSequenceMaxBatch)
	}
	for i, p := range programs {
		if p.Channels < 1 || p.Channels > 2 {
			return nil, fmt.Errorf("ctl sequence: program %d has invalid channels %d", i, p.Channels)
		}
	}

	binPath, err := CTLSequenceHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayloadVersion(ctlSequenceBatchMagic, ctlSequenceBatchVersion, uint32(len(programs)))
	for _, p := range programs {
		appendCTLSequenceProgram(payload, p)
	}
	data, err := RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run opus ctl sequence batch helper: %w", err)
	}
	return parseCTLSequenceBatchResults(data, programs)
}

func ctlSequenceVersion(p CTLSequenceParams) uint32 {
	if p.CapturePackets {
		return 2
	}
	return 1
}

func appendCTLSequenceProgram(payload *OraclePayload, p CTLSequenceParams) {
	payload.Magic(ctlSequenceInputMagic)
	version := ctlSequenceVersion(p)
	payload.U32(version)
	dec := uint32(0)
	if p.IsDecoder {
		dec = 1
	}
	frameSize := p.FrameSize
	if frameSize <= 0 {
		frameSize = 960
	}
	payload.U32(dec)
	payload.U32(uint32(p.SampleRate))
	payload.U32(uint32(p.Channels))
	payload.U32(uint32(p.Application))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(len(p.FeedPacket)))
	if len(p.FeedPacket) > 0 {
		payload.Raw(p.FeedPacket)
		if pad := (4 - len(p.FeedPacket)%4) % 4; pad > 0 {
			payload.Raw(make([]byte, pad))
		}
	}
	payload.U32(uint32(len(p.Ops)))
	for _, op := range p.Ops {
		payload.U32(uint32(op.Op))
		payload.I32(op.Request)
		payload.I32(op.Arg)
	}
}

func readCTLSequenceResults(reader *OracleReader, version uint32, ops []CTLOp) ([]CTLResult, error) {
	n := reader.Count(len(ops))
	out := make([]CTLResult, n)
	for i := range n {
		ret := reader.I32()
		val := reader.I32()
		have := reader.U32()
		out[i] = CTLResult{Ret: ret, Value: val, HaveValue: have != 0}
		if version >= 2 {
			packetLen := int(reader.U32())
			if packetLen < 0 || packetLen > 4000 {
				return nil, fmt.Errorf("ctl sequence: op %d packet length=%d", i, packetLen)
			}
			out[i].Packet = append([]byte(nil), reader.Bytes(packetLen)...)
			if pad := (4 - packetLen%4) % 4; pad > 0 {
				reader.Bytes(pad)
			}
		}
	}
	return out, nil
}

func parseCTLSequenceBatchResults(data []byte, programs []CTLSequenceParams) ([][]CTLResult, error) {
	reader, version, err := NewOracleReaderVersion("opus ctl sequence batch", ctlSequenceBatchMagic, data)
	if err != nil {
		return nil, err
	}
	if version != ctlSequenceBatchVersion {
		return nil, fmt.Errorf("opus ctl sequence batch helper version=%d want %d", version, ctlSequenceBatchVersion)
	}
	n := reader.Count(len(programs))
	out := make([][]CTLResult, n)
	for i := range n {
		if got := string(reader.Bytes(4)); reader.Err() != nil {
			return nil, reader.Err()
		} else if got != ctlSequenceOutputMagic {
			return nil, fmt.Errorf("ctl sequence batch program %d: unexpected result magic %q", i, got)
		}
		programVersion := reader.U32()
		wantVersion := ctlSequenceVersion(programs[i])
		if programVersion != wantVersion {
			return nil, fmt.Errorf("ctl sequence batch program %d helper version=%d want %d", i, programVersion, wantVersion)
		}
		results, err := readCTLSequenceResults(reader, programVersion, programs[i].Ops)
		if err != nil {
			return nil, fmt.Errorf("ctl sequence batch program %d: %w", i, err)
		}
		if err := reader.Err(); err != nil {
			return nil, fmt.Errorf("ctl sequence batch program %d: %w", i, err)
		}
		out[i] = results
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, fmt.Errorf("ctl sequence batch oracle payload not fully consumed: %w", err)
	}
	return out, nil
}
