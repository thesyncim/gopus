//go:build gopus_silk_trace

// Frame-level SILK encoder-control oracle comparison.
//
// Builds tools/csrc/libopus_silk_ctrl_info.c (which overrides
// silk_encode_frame_FLP via tools/csrc/silk_encode_frame_FLP_dump.c) and dumps,
// for every internal SILK frame, the silk_encoder_control_FLP state that drives
// NSQ + rate control. It then replays the same PCM through gopus' SILK FLP path
// with silk.WithSILKCtrlSnapshotHook and reports the FIRST diverging shaping/NSQ
// quantity, so a size delta in the unconstrained-VBR iter-0 break can be traced
// to its source.
//
//	Run: GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
//	       go test -tags gopus_silk_trace ./testvectors/ -run TestSILKCtrlOracle -v
package testvectors

import (
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

const (
	silkCtrlMaxNbSubfr  = 4
	silkCtrlMaxShapeLPC = 24
	silkCtrlLTPOrder    = 5
	silkCtrlInputMagic  = "GSCI"
	silkCtrlOutputMagic = "GSCO"
)

var silkCtrlHelper libopustest.HelperCache

// silkEncodeFrameDumpSource returns the absolute path to the instrumented
// silk_encode_frame_FLP override translation unit under tools/csrc.
func silkEncodeFrameDumpSource() string {
	// RefPath() == <repoRoot>/tmp_check/opus-<ver>; two parents up is repoRoot.
	repoRoot := filepath.Dir(filepath.Dir(libopustest.RefPath()))
	return filepath.Join(repoRoot, "tools", "csrc", "silk_encode_frame_FLP_dump.c")
}

func getSILKCtrlHelperPath(t testing.TB) (string, bool) {
	t.Helper()
	path, err := silkCtrlHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "silk ctrl",
		OutputBase: "gopus_libopus_silk_ctrl",
		SourceFile: "libopus_silk_ctrl_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes: []string{
			"silk", "silk/float", "celt",
		},
		Sources: []string{
			// The override TU that supplies our instrumented silk_encode_frame_FLP.
			// It is placed before libopus.a (BuildCHelper link order) so the linker
			// resolves the symbol here and skips the archived encode_frame_FLP.o.
			silkEncodeFrameDumpSource(),
		},
		Libs: []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		if libopustest.StrictRefRequired() {
			t.Fatalf("build silk ctrl helper: %v", err)
		}
		t.Skipf("silk ctrl helper unavailable: %v", err)
		return "", false
	}
	return path, true
}

// silkCtrlRecord mirrors one ctrl_record emitted by the C oracle.
type silkCtrlRecord struct {
	opusFrame     int32
	channel       int32
	nbSubfr       int32
	signalType    int32
	quantOffset   int32
	maxBits       int32
	useCBR        int32
	nBytes        int32
	predGain      float32
	ltpredCodGain float32
	lambda        float32
	inputQuality  float32
	codingQuality float32
	gainsUnqQ16   [silkCtrlMaxNbSubfr]int32
	gains         [silkCtrlMaxNbSubfr]float32
	ar            [silkCtrlMaxNbSubfr * silkCtrlMaxShapeLPC]float32
	lfMA          [silkCtrlMaxNbSubfr]float32
	lfAR          [silkCtrlMaxNbSubfr]float32
	tilt          [silkCtrlMaxNbSubfr]float32
	harmShapeGain [silkCtrlMaxNbSubfr]float32
	ltpCoef       [silkCtrlLTPOrder * silkCtrlMaxNbSubfr]float32
	ltpScale      float32
	pitchL        [silkCtrlMaxNbSubfr]int32
}

type silkCtrlOracleOut struct {
	packets [][]byte
	ranges  []uint32
	ctrl    []silkCtrlRecord
}

func runSILKCtrlOracle(helperPath string, req []byte, nFrames int) (*silkCtrlOracleOut, error) {
	raw, err := libopustest.RunHelper(helperPath, req)
	if err != nil {
		return nil, fmt.Errorf("run silk ctrl oracle: %w", err)
	}
	if len(raw) < 12 || string(raw[0:4]) != silkCtrlOutputMagic {
		return nil, fmt.Errorf("bad oracle response magic")
	}
	if binary.LittleEndian.Uint32(raw[4:8]) != 1 {
		return nil, fmt.Errorf("bad oracle version")
	}
	gotN := int(binary.LittleEndian.Uint32(raw[8:12]))
	if gotN != nFrames {
		return nil, fmt.Errorf("oracle frame count mismatch: got %d want %d", gotN, nFrames)
	}
	out := &silkCtrlOracleOut{}
	off := 12
	for i := 0; i < nFrames; i++ {
		pktLen := int(binary.LittleEndian.Uint32(raw[off:]))
		fr := binary.LittleEndian.Uint32(raw[off+4:])
		off += 8
		out.packets = append(out.packets, append([]byte(nil), raw[off:off+pktLen]...))
		out.ranges = append(out.ranges, fr)
		off += pktLen
	}
	nCtrl := int(binary.LittleEndian.Uint32(raw[off:]))
	off += 4
	rd := func() uint32 { v := binary.LittleEndian.Uint32(raw[off:]); off += 4; return v }
	ri := func() int32 { return int32(rd()) }
	rf := func() float32 { return math.Float32frombits(rd()) }
	for c := 0; c < nCtrl; c++ {
		var r silkCtrlRecord
		r.opusFrame = ri()
		r.channel = ri()
		r.nbSubfr = ri()
		r.signalType = ri()
		r.quantOffset = ri()
		r.maxBits = ri()
		r.useCBR = ri()
		r.nBytes = ri()
		r.predGain = rf()
		r.ltpredCodGain = rf()
		r.lambda = rf()
		r.inputQuality = rf()
		r.codingQuality = rf()
		for k := range r.gainsUnqQ16 {
			r.gainsUnqQ16[k] = ri()
		}
		for k := range r.gains {
			r.gains[k] = rf()
		}
		for k := range r.ar {
			r.ar[k] = rf()
		}
		for k := range r.lfMA {
			r.lfMA[k] = rf()
		}
		for k := range r.lfAR {
			r.lfAR[k] = rf()
		}
		for k := range r.tilt {
			r.tilt[k] = rf()
		}
		for k := range r.harmShapeGain {
			r.harmShapeGain[k] = rf()
		}
		for k := range r.ltpCoef {
			r.ltpCoef[k] = rf()
		}
		r.ltpScale = rf()
		for k := range r.pitchL {
			r.pitchL[k] = ri()
		}
		out.ctrl = append(out.ctrl, r)
	}
	if off != len(raw) {
		return nil, fmt.Errorf("trailing oracle bytes: consumed %d of %d", off, len(raw))
	}
	return out, nil
}

// TestSILKCtrlOracle bisects the first diverging SILK control quantity vs
// libopus for the pure-SILK unconstrained-VBR mono case.
func TestSILKCtrlOracle(t *testing.T) {
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	helperPath, ok := getSILKCtrlHelperPath(t)
	if !ok {
		return
	}

	const (
		channels  = 1
		frameSize = 480 // 10ms @ 48k -> NB
		bitrate   = 12000
		nFrames   = 8
	)
	pcm := makeVBRCVBRTestPCM(nFrames, frameSize, channels)

	req := buildVBRCVBRRequest(
		oracleModeVBR, opusApplicationVoIP,
		48000, channels, frameSize, bitrate,
		opusBandwidthNB, opusSignalVoice,
		pcm, nFrames,
	)
	// Reuse the GVCI builder but rewrite the magic to GSCI.
	copy(req[0:4], []byte(silkCtrlInputMagic))

	oracle, err := runSILKCtrlOracle(helperPath, req, nFrames)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}

	// Drive gopus with the snapshot hook.
	var snaps []silk.SILKCtrlSnapshot
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	mustNoErr(t, enc.SetFrameSize(frameSize))
	mustNoErr(t, enc.SetBitrate(bitrate))
	mustNoErr(t, enc.SetBandwidth(types.BandwidthNarrowband))
	mustNoErr(t, enc.SetSignal(types.SignalVoice))
	mustNoErr(t, enc.SetComplexity(10))
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)

	buf := make([]byte, 4000)
	goLens := make([]int, nFrames)
	silk.WithSILKCtrlSnapshotHook(func(s silk.SILKCtrlSnapshot) {
		snaps = append(snaps, s)
	}, func() {
		for i := 0; i < nFrames; i++ {
			frame := pcm[i*frameSize*channels : (i+1)*frameSize*channels]
			n, encErr := enc.Encode(frame, buf)
			if encErr != nil {
				t.Fatalf("encode frame %d: %v", i, encErr)
			}
			goLens[i] = n
		}
	})

	// Pair the ctrl records (mono => one per opus frame, channel 0) with snaps.
	var refCtrl []silkCtrlRecord
	for _, r := range oracle.ctrl {
		if r.channel == 0 {
			refCtrl = append(refCtrl, r)
		}
	}

	t.Logf("oracle packets: %v", pktLens(oracle.packets))
	t.Logf("gopus  packets: %v", goLens)
	t.Logf("ctrl records: ref=%d gopus_snaps=%d", len(refCtrl), len(snaps))

	n := len(refCtrl)
	if len(snaps) < n {
		n = len(snaps)
	}
	for i := 0; i < n; i++ {
		r := refCtrl[i]
		s := snaps[i]
		nbs := int(r.nbSubfr)
		var diffs []string
		if int(r.signalType) != s.SignalType {
			diffs = append(diffs, fmt.Sprintf("signalType ref=%d go=%d", r.signalType, s.SignalType))
		}
		if int(r.quantOffset) != s.QuantOffset {
			diffs = append(diffs, fmt.Sprintf("quantOffset ref=%d go=%d", r.quantOffset, s.QuantOffset))
		}
		// Lambda_Q10: oracle stores float Lambda; convert with truncation toward
		// nearest (libopus silk_float2int = round). gopus stores LambdaQ10 directly.
		refLambdaQ10 := int32(math.RoundToEven(float64(r.lambda) * 1024.0))
		if refLambdaQ10 != s.LambdaQ10 {
			diffs = append(diffs, fmt.Sprintf("LambdaQ10 ref~=%d(%.6f) go=%d", refLambdaQ10, r.lambda, s.LambdaQ10))
		}
		for k := 0; k < nbs; k++ {
			refG := int32(float64(r.gains[k]) * 65536.0) // gopus uses truncation for Gains_Q16
			if absI32(refG-s.GainsQ16[k]) > 1 {
				diffs = append(diffs, fmt.Sprintf("Gains_Q16[%d] ref=%d(go=%d)", k, refG, s.GainsQ16[k]))
			}
			refTilt := silkFloat2intRound(r.tilt[k] * 16384.0)
			if absI32(refTilt-s.TiltQ14[k]) > 0 {
				diffs = append(diffs, fmt.Sprintf("Tilt_Q14[%d] ref=%d go=%d", k, refTilt, s.TiltQ14[k]))
			}
			refHSG := silkFloat2intRound(r.harmShapeGain[k] * 16384.0)
			if absI32(refHSG-s.HarmShapeQ14[k]) > 0 {
				diffs = append(diffs, fmt.Sprintf("HarmShapeGain_Q14[%d] ref=%d go=%d", k, refHSG, s.HarmShapeQ14[k]))
			}
			refLF := (silkFloat2intRound(r.lfAR[k]*16384.0) << 16) | int32(uint16(silkFloat2intRound(r.lfMA[k]*16384.0)))
			if refLF != s.LFShpQ14[k] {
				diffs = append(diffs, fmt.Sprintf("LF_shp_Q14[%d] ref=%d go=%d", k, refLF, s.LFShpQ14[k]))
			}
			if r.pitchL[k] != s.PitchL[k] {
				diffs = append(diffs, fmt.Sprintf("pitchL[%d] ref=%d go=%d", k, r.pitchL[k], s.PitchL[k]))
			}
			for j := 0; j < silkCtrlMaxShapeLPC; j++ {
				refAR := silkFloat2intRound(r.ar[k*silkCtrlMaxShapeLPC+j] * 8192.0)
				goAR := int32(s.ARShpQ13[k*silkCtrlMaxShapeLPC+j])
				if absI32(refAR-goAR) > 0 {
					diffs = append(diffs, fmt.Sprintf("AR_Q13[%d][%d] ref=%d go=%d", k, j, refAR, goAR))
					break
				}
			}
		}
		if len(diffs) > 0 {
			t.Logf("FRAME %d FIRST DIVERGENCE (ref nBytes=%d): %v", i, r.nBytes, diffs)
			t.Logf("  ref: sig=%d qoff=%d lambda=%.6f gainsUnq=%v predGain=%.4f ltpCG=%.4f inQ=%.6f cdQ=%.6f",
				r.signalType, r.quantOffset, r.lambda, r.gainsUnqQ16[:nbs], r.predGain, r.ltpredCodGain, r.inputQuality, r.codingQuality)
			return
		}
	}
	t.Logf("NO control-state divergence across %d frames; size delta (if any) is in NSQ/encode bit usage", n)
}

func pktLens(pkts [][]byte) []int {
	out := make([]int, len(pkts))
	for i, p := range pkts {
		out[i] = len(p)
	}
	return out
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func silkFloat2intRound(f float32) int32 {
	return int32(math.RoundToEven(float64(f)))
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
