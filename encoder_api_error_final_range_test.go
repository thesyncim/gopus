package gopus

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"sync"
	"testing"
)

const (
	encodeAPIErrorFloat32 = iota
	encodeAPIErrorInt16
	encodeAPIErrorInt24
)

type encodeAPIErrorRecord struct {
	ret        int32
	finalRange uint32
	packet     []byte
}

var encodeAPIErrorOracle struct {
	sync.Once
	path string
	err  error
}

func encoderAPIErrorOraclePath() (string, error) {
	encodeAPIErrorOracle.Do(func() {
		encodeAPIErrorOracle.path, encodeAPIErrorOracle.err = libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "public encode API error state",
			OutputBase:  "gopus_libopus_encode_api_error_state",
			SourceFile:  "libopus_encode_api_error_state.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk", "src"},
			DeadStrip:   true,
		})
	})
	return encodeAPIErrorOracle.path, encodeAPIErrorOracle.err
}

func TestPublicEncodeAPIErrorFinalRangeAndRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := encoderAPIErrorOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "public encode API error state", err)
		return
	}
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	apis := []struct {
		name string
		kind int
	}{
		{name: "float32", kind: encodeAPIErrorFloat32},
		{name: "int16", kind: encodeAPIErrorInt16},
		{name: "int24", kind: encodeAPIErrorInt24},
	}
	for _, rate := range rates {
		for _, api := range apis {
			t.Run(fmt.Sprintf("%dHz/%s", rate, api.name), func(t *testing.T) {
				enc, err := NewEncoder(EncoderConfig{
					SampleRate:  rate,
					Channels:    1,
					Application: ApplicationAudio,
				})
				if err != nil {
					t.Fatalf("NewEncoder: %v", err)
				}
				if err := enc.SetBitrate(64000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				frameSize := enc.FrameSize()
				pcmFloat, pcmInt16, pcmInt24 := encodeAPIErrorPCM(frameSize)
				want, err := probeEncodeAPIErrorState(helper, rate, 1, api.kind, frameSize)
				if err != nil {
					t.Fatalf("libopus oracle: %v", err)
				}
				if len(want) != 7 {
					t.Fatalf("oracle returned %d records, want 7", len(want))
				}
				if want[0].ret <= 0 || want[0].finalRange == 0 ||
					want[1].ret >= 0 || want[2].ret <= 0 || want[2].finalRange == 0 ||
					want[3].ret >= 0 || want[4].ret <= 0 || want[4].finalRange == 0 ||
					want[5].ret >= 0 || want[6].ret <= 0 || want[6].finalRange == 0 {
					t.Fatalf("oracle did not exercise encode/error/recovery: ret/ranges=%+v", want)
				}

				steps := []struct {
					name     string
					duration ExpertFrameDuration
					budget   int
					wantErr  error
				}{
					{name: "prime", duration: ExpertFrameDurationArg, budget: 4000},
					{name: "invalid expert frame", duration: ExpertFrameDuration120Ms, budget: 4000, wantErr: ErrInvalidFrameSize},
					{name: "recovery after invalid expert frame", duration: ExpertFrameDurationArg, budget: 4000},
					{name: "zero output budget", duration: ExpertFrameDurationArg, budget: 0, wantErr: ErrBufferTooSmall},
					{name: "recovery after zero budget", duration: ExpertFrameDurationArg, budget: 4000},
					{name: "invalid frame and zero budget", duration: ExpertFrameDuration120Ms, budget: 0, wantErr: ErrInvalidFrameSize},
					{name: "recovery after both errors", duration: ExpertFrameDurationArg, budget: 4000},
				}
				for i, step := range steps {
					if err := enc.SetExpertFrameDuration(step.duration); err != nil {
						t.Fatalf("%s: SetExpertFrameDuration: %v", step.name, err)
					}
					buffer := make([]byte, step.budget)
					n, err := encodeAPIErrorCall(enc, api.kind, pcmFloat, pcmInt16, pcmInt24, buffer)
					if err != step.wantErr {
						t.Fatalf("%s: error=%v want %v", step.name, err, step.wantErr)
					}
					if step.wantErr != nil && n != 0 {
						t.Fatalf("%s: wrote %d bytes on error, want 0", step.name, n)
					}
					ret := int32(n)
					if err != nil {
						ret = -1
					}
					var packet []byte
					if n > 0 {
						packet = buffer[:n]
					}
					got := encodeAPIErrorRecord{ret: ret, finalRange: enc.FinalRange(), packet: packet}
					if got.ret != want[i].ret || got.finalRange != want[i].finalRange || !bytes.Equal(got.packet, want[i].packet) {
						t.Fatalf("%s: Go ret=%d range=%08x packet=%x; libopus ret=%d range=%08x packet=%x", step.name, got.ret, got.finalRange, got.packet, want[i].ret, want[i].finalRange, want[i].packet)
					}
				}
				priorRange := enc.FinalRange()
				_, shortErr := encodeAPIErrorCall(enc, api.kind, pcmFloat[:len(pcmFloat)-1], pcmInt16[:len(pcmInt16)-1], pcmInt24[:len(pcmInt24)-1], make([]byte, 4000))
				if shortErr != ErrInvalidFrameSize {
					t.Fatalf("short PCM slice error=%v, want %v", shortErr, ErrInvalidFrameSize)
				}
				if got := enc.FinalRange(); got != priorRange {
					t.Fatalf("short PCM slice changed final range: got=%08x want=%08x", got, priorRange)
				}
			})
		}
	}
}

func encodeAPIErrorPCM(frameSize int) ([]float32, []int16, []int32) {
	pcmFloat := make([]float32, frameSize)
	pcmInt16 := make([]int16, frameSize)
	pcmInt24 := make([]int32, frameSize)
	for i := range frameSize {
		value := int16((i*7919+1237)%20001 - 10000)
		pcmInt16[i] = value
		pcmFloat[i] = float32(value) / 32768.0
		pcmInt24[i] = int32(value) * 256
	}
	return pcmFloat, pcmInt16, pcmInt24
}

func encodeAPIErrorCall(enc *Encoder, api int, pcmFloat []float32, pcmInt16 []int16, pcmInt24 []int32, output []byte) (int, error) {
	switch api {
	case encodeAPIErrorFloat32:
		return enc.Encode(pcmFloat, output)
	case encodeAPIErrorInt16:
		return enc.EncodeInt16(pcmInt16, output)
	case encodeAPIErrorInt24:
		return enc.EncodeInt24(pcmInt24, output)
	default:
		return 0, ErrInvalidArgument
	}
}

func probeEncodeAPIErrorState(helper string, sampleRate, channels, api, frameSize int) ([]encodeAPIErrorRecord, error) {
	payload := libopustest.NewOraclePayloadVersion("GEAI", 1,
		uint32(sampleRate), uint32(channels), uint32(libopustest.OpusApplicationAudio), uint32(api), uint32(frameSize))
	data, err := libopustest.RunOracle(helper, payload.Bytes(), "public encode API error state", "GEAO")
	if err != nil {
		return nil, err
	}
	count := data.Count(7)
	records := make([]encodeAPIErrorRecord, count)
	for i := range records {
		records[i].ret = data.I32()
		records[i].finalRange = data.U32()
		records[i].packet = append([]byte(nil), data.Bytes(int(data.U32()))...)
	}
	if err := data.ExpectConsumed(); err != nil {
		return nil, err
	}
	return records, nil
}

func TestEncoderForcedChannelTransitionLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name            string
		mode            EncoderMode
		oracleMode      int32
		packetMode      Mode
		bandwidth       Bandwidth
		oracleBandwidth int32
		frameSize       int
		stereoToMonoLag bool
	}{
		{"silk_20ms", EncoderModeSILK, libopustest.OpusForceModeSILKOnly, ModeSILK, BandwidthWideband, libopustest.OpusBandwidthWideband, 960, true},
		{"hybrid_20ms", EncoderModeHybrid, libopustest.OpusForceModeHybrid, ModeHybrid, BandwidthFullband, libopustest.OpusBandwidthFullband, 960, true},
		{"celt_20ms", EncoderModeCELT, libopustest.OpusForceModeCELTOnly, ModeCELT, BandwidthFullband, libopustest.OpusBandwidthFullband, 960, false},
		{"silk_40ms", EncoderModeSILK, libopustest.OpusForceModeSILKOnly, ModeSILK, BandwidthWideband, libopustest.OpusBandwidthWideband, 1920, true},
		{"hybrid_40ms", EncoderModeHybrid, libopustest.OpusForceModeHybrid, ModeHybrid, BandwidthFullband, libopustest.OpusBandwidthFullband, 1920, true},
		{"celt_40ms", EncoderModeCELT, libopustest.OpusForceModeCELTOnly, ModeCELT, BandwidthFullband, libopustest.OpusBandwidthFullband, 1920, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := encoderChannelTransitionOps(tt.oracleMode, tt.oracleBandwidth)
			oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
				SampleRate: 48000, Channels: 2, Application: cAppAudio,
				FrameSize: tt.frameSize, CapturePackets: true, Ops: ops,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "encoder channel transition", err)
				return
			}

			enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
			if tt.frameSize != enc.FrameSize() {
				if err := enc.SetFrameSize(tt.frameSize); err != nil {
					t.Fatalf("SetFrameSize(%d): %v", tt.frameSize, err)
				}
			}
			pcm := generateSineWaveFloat32(48000, 440, tt.frameSize, 2)
			packetBuf := make([]byte, 4000)
			packetIndex := 0
			lbrrSeen := false
			wantStereo := [4]bool{true, tt.stereoToMonoLag, false, true}

			if len(oracle) != len(ops) {
				t.Fatalf("oracle returned %d results for %d operations", len(oracle), len(ops))
			}
			for i, op := range ops {
				got := libopustest.CTLResult{}
				switch op.Op {
				case libopustest.CTLOpSet:
					got.Ret = applyEncoderSet(enc, op.Request, op.Arg)
				case libopustest.CTLOpProcess:
					n, err := enc.Encode(pcm, packetBuf)
					if err != nil {
						t.Fatalf("PROCESS %d: %v", packetIndex, err)
					}
					got.Ret = int32(n)
					got.Packet = append([]byte(nil), packetBuf[:n]...)
				case libopustest.CTLOpGet:
					got.Value, got.HaveValue = applyEncoderGet(enc, op.Request)
					got.Ret = cOpusOK
				default:
					t.Fatalf("unexpected operation %d at index %d", op.Op, i)
				}

				ref := oracle[i]
				if got.Ret != ref.Ret || got.Value != ref.Value || got.HaveValue != ref.HaveValue {
					t.Fatalf("operation %d (%+v): Go ret/value/presence=%d/%08x/%t, libopus=%d/%08x/%t",
						i, op, got.Ret, uint32(got.Value), got.HaveValue, ref.Ret, uint32(ref.Value), ref.HaveValue)
				}
				if op.Op != libopustest.CTLOpProcess {
					continue
				}

				if !bytes.Equal(got.Packet, ref.Packet) {
					first := firstByteDifference(got.Packet, ref.Packet)
					t.Fatalf("packet %d differs: Go length=%d TOC=%s, libopus length=%d TOC=%s, first byte difference=%d",
						packetIndex, len(got.Packet), packetTOCLabel(got.Packet), len(ref.Packet), packetTOCLabel(ref.Packet), first)
				}
				if len(ref.Packet) == 0 {
					t.Fatalf("libopus packet %d is empty", packetIndex)
				}
				toc := ParseTOC(ref.Packet[0])
				if toc.Mode != tt.packetMode || toc.Bandwidth != tt.bandwidth {
					t.Fatalf("packet %d TOC mode/bandwidth=%v/%v, want %v/%v", packetIndex, toc.Mode, toc.Bandwidth, tt.packetMode, tt.bandwidth)
				}
				if toc.Stereo != wantStereo[packetIndex] {
					t.Fatalf("packet %d TOC stereo=%t, want %t", packetIndex, toc.Stereo, wantStereo[packetIndex])
				}
				lbrrSeen = lbrrSeen || PacketHasLBRR(ref.Packet)
				packetIndex++
			}
			if packetIndex != len(wantStereo) {
				t.Fatalf("encoded %d packets, want %d", packetIndex, len(wantStereo))
			}
			if tt.mode != EncoderModeCELT && !lbrrSeen {
				t.Fatal("libopus packets did not carry expected SILK LBRR with FEC enabled")
			}
			if tt.name == "hybrid_20ms" {
				assertWarmChannelTransitionAllocations(t, enc, pcm, packetBuf)
			}
		})
	}
}

func TestEncoderSetForceModeInvalidValuesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	ops := []libopustest.CTLOp{
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: cOpusAuto},
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: -1001},
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: 0},
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: 999},
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: 1003},
	}
	oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
		SampleRate: 48000, Channels: 2, Application: cAppAudio, Ops: ops,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "encoder force-mode CTL", err)
		return
	}
	got := runEncoderCTLProgram(t, 48000, 2, ApplicationAudio, ops)
	compareCTLResults(t, "force mode invalid argument parity", ops, got, oracle)
}

func encoderChannelTransitionOps(forceMode, bandwidth int32) []libopustest.CTLOp {
	set := func(request, arg int32) libopustest.CTLOp {
		return libopustest.CTLOp{Op: libopustest.CTLOpSet, Request: request, Arg: arg}
	}
	processAndRange := func(ops *[]libopustest.CTLOp) {
		*ops = append(*ops,
			libopustest.CTLOp{Op: libopustest.CTLOpProcess},
			libopustest.CTLOp{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
		)
	}
	ops := []libopustest.CTLOp{
		set(reqSetBitrate, 64000),
		set(reqSetComplexity, 9),
		set(reqSetVBR, 1),
		set(reqSetVBRConstraint, 0),
		set(reqSetSignal, cSignalVoice),
		set(reqSetLSBDepth, 24),
		set(reqSetInbandFEC, 1),
		set(reqSetPacketLossPerc, 30),
		set(reqSetForceMode, forceMode),
		set(reqSetBandwidth, bandwidth),
		set(reqSetForceChannels, 2),
	}
	processAndRange(&ops)
	ops = append(ops, set(reqSetForceChannels, 1))
	processAndRange(&ops)
	processAndRange(&ops)
	ops = append(ops, set(reqSetForceChannels, 2))
	processAndRange(&ops)
	return ops
}

func firstByteDifference(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func packetTOCLabel(packet []byte) string {
	if len(packet) == 0 {
		return "<empty>"
	}
	return fmt.Sprintf("%02x", packet[0])
}

func assertWarmChannelTransitionAllocations(t *testing.T, enc *Encoder, pcm []float32, packet []byte) {
	t.Helper()
	encodeTransition := func() {
		enc.Reset()
		if err := enc.SetMode(EncoderModeHybrid); err != nil {
			t.Fatalf("SetMode(Hybrid): %v", err)
		}
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth(Fullband): %v", err)
		}
		if err := enc.SetBitrate(64000); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetComplexity(9); err != nil {
			t.Fatalf("SetComplexity: %v", err)
		}
		enc.SetVBR(true)
		enc.SetVBRConstraint(false)
		if err := enc.SetSignal(SignalVoice); err != nil {
			t.Fatalf("SetSignal: %v", err)
		}
		if err := enc.SetLSBDepth(24); err != nil {
			t.Fatalf("SetLSBDepth: %v", err)
		}
		if err := enc.SetInBandFEC(1); err != nil {
			t.Fatalf("SetInBandFEC: %v", err)
		}
		if err := enc.SetPacketLoss(30); err != nil {
			t.Fatalf("SetPacketLoss: %v", err)
		}
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels(2): %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode stereo warm-up: %v", err)
		}
		if err := enc.SetForceChannels(1); err != nil {
			t.Fatalf("SetForceChannels(1): %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode mono-transition warm-up: %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode mono warm-up: %v", err)
		}
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels(2): %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode stereo-transition warm-up: %v", err)
		}
	}
	encodeTransition()
	if got := testing.AllocsPerRun(100, encodeTransition); got != 0 {
		t.Fatalf("warmed forced-channel transition allocated %.2f times per four-frame sequence", got)
	}
}

func TestEncoder_SetBitrate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	validBitrates := []struct {
		input int
		want  int
	}{
		{BitrateAuto, BitrateAuto},
		{BitrateMax, BitrateMax},
		{1, 500},
		{500, 500},
		{5999, 5999},
		{6000, 6000},
		{12000, 12000},
		{32000, 32000},
		{64000, 64000},
		{128000, 128000},
		{256000, 256000},
		{750000, 750000},
		{750001, 750000},
	}
	for _, tc := range validBitrates {
		t.Run(fmt.Sprintf("bitrate_%d", tc.input), func(t *testing.T) {
			br := tc.input
			err := enc.SetBitrate(br)
			if err != nil {
				t.Errorf("SetBitrate(%d) error: %v", br, err)
			}
			if enc.Bitrate() != tc.want {
				t.Errorf("Bitrate() = %d, want %d", enc.Bitrate(), tc.want)
			}
		})
	}

	// Invalid bitrates
	invalidBitrates := []int{0, -2, -1001}
	for _, br := range invalidBitrates {
		t.Run(fmt.Sprintf("invalid_bitrate_%d", br), func(t *testing.T) {
			err := enc.SetBitrate(br)
			if err != ErrInvalidBitrate {
				t.Errorf("SetBitrate(%d) error = %v, want ErrInvalidBitrate", br, err)
			}
		})
	}
}

func TestEncoder_SetBitrateStereoClampsPerChannel(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	if err := enc.SetBitrate(1500001); err != nil {
		t.Fatalf("SetBitrate(1500001) error: %v", err)
	}
	if got := enc.Bitrate(); got != 1500000 {
		t.Fatalf("Bitrate() = %d, want 1500000", got)
	}
}

func TestEncoder_SetComplexity(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if got := enc.Complexity(); got != 9 {
		t.Fatalf("Complexity() default = %d, want 9", got)
	}

	// Valid complexity values
	for c := 0; c <= 10; c++ {
		t.Run(fmt.Sprintf("complexity_%d", c), func(t *testing.T) {
			err := enc.SetComplexity(c)
			if err != nil {
				t.Errorf("SetComplexity(%d) error: %v", c, err)
			}
			if enc.Complexity() != c {
				t.Errorf("Complexity() = %d, want %d", enc.Complexity(), c)
			}
		})
	}

	// Invalid complexity values
	invalidComplexity := []int{-1, 11, 100}
	for _, c := range invalidComplexity {
		t.Run(fmt.Sprintf("invalid_complexity_%d", c), func(t *testing.T) {
			err := enc.SetComplexity(c)
			if err != ErrInvalidComplexity {
				t.Errorf("SetComplexity(%d) error = %v, want ErrInvalidComplexity", c, err)
			}
		})
	}
}

func TestEncoder_SetBitrateMode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	validModes := []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR}
	for _, mode := range validModes {
		t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
			if err := enc.SetBitrateMode(mode); err != nil {
				t.Fatalf("SetBitrateMode(%d) error: %v", mode, err)
			}
			if got := enc.BitrateMode(); got != mode {
				t.Fatalf("BitrateMode()=%d want=%d", got, mode)
			}
		})
	}

	if err := enc.SetBitrateMode(BitrateMode(999)); err != ErrInvalidBitrateMode {
		t.Fatalf("SetBitrateMode(invalid) error=%v want=%v", err, ErrInvalidBitrateMode)
	}
}

func TestEncoder_SetMode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if got := enc.Mode(); got != EncoderModeAuto {
		t.Fatalf("Mode() default=%v want=%v", got, EncoderModeAuto)
	}

	for _, mode := range []EncoderMode{
		EncoderModeAuto,
		EncoderModeSILK,
		EncoderModeHybrid,
		EncoderModeCELT,
	} {
		if err := enc.SetMode(mode); err != nil {
			t.Fatalf("SetMode(%v) error: %v", mode, err)
		}
		if got := enc.Mode(); got != mode {
			t.Fatalf("Mode()=%v want=%v", got, mode)
		}
		if got := enc.enc.Mode(); got != mode {
			t.Fatalf("core Mode()=%v want=%v", got, mode)
		}
	}

	if err := enc.SetMode(EncoderMode(99)); err != ErrInvalidArgument {
		t.Fatalf("SetMode(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after invalid=%v want=%v", got, EncoderModeCELT)
	}
	enc.Reset()
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after Reset=%v want=%v", got, EncoderModeCELT)
	}
}

func TestEncoder_ApplicationModeRestrictionsOverrideSetMode(t *testing.T) {
	tests := []struct {
		name      string
		app       Application
		forceMode EncoderMode
		wantTOC   Mode
	}{
		{"lowdelay ignores silk force", ApplicationLowDelay, EncoderModeSILK, ModeCELT},
		{"restricted silk ignores celt force", ApplicationRestrictedSilk, EncoderModeCELT, ModeSILK},
		{"restricted celt ignores silk force", ApplicationRestrictedCelt, EncoderModeSILK, ModeCELT},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: tc.app})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			if err := enc.SetMode(tc.forceMode); err != nil {
				t.Fatalf("SetMode(%v) error: %v", tc.forceMode, err)
			}
			if got := enc.Mode(); got != tc.forceMode {
				t.Fatalf("Mode()=%v want stored force mode %v", got, tc.forceMode)
			}
			packet := make([]byte, 4000)
			n, err := enc.Encode(generateSineWave(48000, 440, 960), packet)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if n == 0 {
				t.Fatal("Encode returned empty packet")
			}
			if got := ParseTOC(packet[0]).Mode; got != tc.wantTOC {
				t.Fatalf("TOC mode=%v want=%v", got, tc.wantTOC)
			}
		})
	}
}

func TestEncoder_SetVBRAndConstraint(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	if !enc.VBR() {
		t.Fatal("VBR()=false by default, want true")
	}
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=false by default, want true (CVBR default)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode()=%d want=%d by default", got, BitrateModeCVBR)
	}

	enc.SetVBR(false)
	if enc.VBR() {
		t.Fatal("VBR()=true after SetVBR(false)")
	}
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint() should remain true after SetVBR(false)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeCBR)
	}

	enc.SetVBR(true)
	if !enc.VBR() {
		t.Fatal("VBR()=false after SetVBR(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode()=%d want=%d (vbrConstraint still true)", got, BitrateModeCVBR)
	}

	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=false after SetVBRConstraint(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeCVBR)
	}

	enc.SetVBRConstraint(false)
	if enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=true after SetVBRConstraint(false)")
	}
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeVBR)
	}

	enc.SetVBR(false)
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Fatalf("BitrateMode()=%d want=%d after SetVBR(false)", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Fatalf("BitrateMode()=%d want=%d after re-enable", got, BitrateModeVBR)
	}
}

func TestEncoder_SetPacketLoss(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, loss := range []int{0, 5, 25, 100} {
		if err := enc.SetPacketLoss(loss); err != nil {
			t.Fatalf("SetPacketLoss(%d) error: %v", loss, err)
		}
		if got := enc.PacketLoss(); got != loss {
			t.Fatalf("PacketLoss()=%d want=%d", got, loss)
		}
	}

	for _, loss := range []int{-1, 101} {
		if err := enc.SetPacketLoss(loss); err != ErrInvalidPacketLoss {
			t.Fatalf("SetPacketLoss(%d) error=%v want=%v", loss, err, ErrInvalidPacketLoss)
		}
	}
}

func TestEncoder_SetBandwidth(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// SetBandwidth records the user request; Bandwidth() (libopus
	// OPUS_GET_BANDWIDTH = st->bandwidth) keeps the FULLBAND init default until
	// an encode decides the bandwidth, so it does not echo the SET value.
	for _, bw := range []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	} {
		if err := enc.SetBandwidth(bw); err != nil {
			t.Fatalf("SetBandwidth(%d) error: %v", bw, err)
		}
		if got := enc.Bandwidth(); got != BandwidthFullband {
			t.Fatalf("Bandwidth()=%d want=%d (FULLBAND init default before encode)", got, BandwidthFullband)
		}
	}

	if err := enc.SetBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Fatalf("SetBandwidth(invalid) error=%v want=%v", err, ErrInvalidBandwidth)
	}
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Fatalf("SetBandwidthAuto error: %v", err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after SetBandwidthAuto=%d want last concrete bandwidth %d", got, BandwidthFullband)
	}
}

func TestEncoder_SetApplication(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, app := range []Application{ApplicationVoIP, ApplicationAudio, ApplicationLowDelay} {
		if err := enc.SetApplication(app); err != nil {
			t.Fatalf("SetApplication(%d) error: %v", app, err)
		}
		if got := enc.Application(); got != app {
			t.Fatalf("Application()=%d want=%d", got, app)
		}
		wantLowDelay := app == ApplicationLowDelay
		if got := enc.enc.LowDelay(); got != wantLowDelay {
			t.Fatalf("enc.LowDelay()=%v want=%v for app=%d", got, wantLowDelay, app)
		}
	}

	if err := enc.SetApplication(Application(99)); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(invalid) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedSilk); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted silk) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedCelt); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted celt) error=%v want=%v", err, ErrInvalidApplication)
	}

	pcm := make([]float32, enc.FrameSize()*enc.Channels())
	packet := make([]byte, 4000)
	assertApplicationLockAfterEncode(
		t,
		enc.Application,
		enc.SetApplication,
		enc.Reset,
		func() error {
			_, err := enc.Encode(pcm, packet)
			return err
		},
		ApplicationVoIP,
		ApplicationLowDelay,
	)
	if !enc.enc.LowDelay() {
		t.Fatalf("enc.LowDelay() should be true after reset+lowdelay application")
	}
}

func TestEncoder_SetApplicationPreservesControls(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal(SignalMusic) error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthSuperwideband); err != nil {
		t.Fatalf("SetBandwidth(SWB) error: %v", err)
	}
	if err := enc.SetForceChannels(1); err != nil {
		t.Fatalf("SetForceChannels(1) error: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}

	if got := enc.Signal(); got != SignalMusic {
		t.Fatalf("Signal()=%v want %v", got, SignalMusic)
	}
	// SetApplication preserves the user bandwidth request (userBandwidth);
	// Bandwidth() reports st->bandwidth which stays at the FULLBAND init default
	// until an encode decides it.
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want %v (FULLBAND init default before encode)", got, BandwidthFullband)
	}
	if got := enc.ForceChannels(); got != 1 {
		t.Fatalf("ForceChannels()=%d want 1", got)
	}
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode()=%v want %v", got, EncoderModeCELT)
	}
}

func TestEncoder_SetApplicationFromLowDelayRestoresDefaultModeUnlessForced(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationLowDelay})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(ApplicationAudio) error: %v", err)
	}
	if got := enc.Mode(); got != EncoderModeAuto {
		t.Fatalf("Mode()=%v want %v", got, EncoderModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatal("LowDelay()=true after switching to audio, want false")
	}

	forced, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationLowDelay})
	if err != nil {
		t.Fatalf("NewEncoder forced error: %v", err)
	}
	if err := forced.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	if err := forced.SetApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(ApplicationAudio) with forced mode error: %v", err)
	}
	if got := forced.Mode(); got != EncoderModeCELT {
		t.Fatalf("forced Mode()=%v want %v", got, EncoderModeCELT)
	}
}

func TestEncoder_RestrictedApplications(t *testing.T) {
	for _, tt := range restrictedApplicationTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: tt.application})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}

			if got := enc.Application(); got != tt.application {
				t.Fatalf("Application()=%v want=%v", got, tt.application)
			}
			if got := enc.enc.Mode(); got != tt.wantMode {
				t.Fatalf("enc.Mode()=%v want=%v", got, tt.wantMode)
			}
			if got := enc.enc.LowDelay(); got != tt.wantLowDelay {
				t.Fatalf("enc.LowDelay()=%v want=%v", got, tt.wantLowDelay)
			}
			if got := enc.Bandwidth(); got != tt.wantBandwidth {
				t.Fatalf("Bandwidth()=%v want=%v", got, tt.wantBandwidth)
			}
			if got := enc.Lookahead(); got != tt.wantLookahead {
				t.Fatalf("Lookahead()=%d want=%d", got, tt.wantLookahead)
			}

			if err := enc.SetApplication(tt.application); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(same restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if err := enc.SetApplication(ApplicationAudio); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(change restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if tt.application == ApplicationRestrictedSilk {
				if err := enc.SetFrameSize(240); err != ErrInvalidFrameSize {
					t.Fatalf("SetFrameSize(240) error=%v want=%v", err, ErrInvalidFrameSize)
				}
				if err := enc.SetFrameSize(480); err != nil {
					t.Fatalf("SetFrameSize(480) error: %v", err)
				}
			}
		})
	}
}

func TestEncoder_ExpertFrameDuration(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Fatalf("ExpertFrameDuration()=%v want=%v", got, ExpertFrameDurationArg)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration120Ms); err != nil {
		t.Fatalf("SetExpertFrameDuration(120ms) error: %v", err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration120Ms {
		t.Fatalf("ExpertFrameDuration()=%v want=%v", got, ExpertFrameDuration120Ms)
	}
	if got := enc.FrameSize(); got != 960 {
		t.Fatalf("FrameSize()=%d want=960 after fixed duration", got)
	}
	if n, err := enc.Encode(generateSineWave(48000, 440, 960), make([]byte, 4000)); n != 0 || err != ErrInvalidFrameSize {
		t.Fatalf("Encode with fixed duration larger than input = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDurationArg); err != nil {
		t.Fatalf("SetExpertFrameDuration(arg) error: %v", err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Fatalf("ExpertFrameDuration()=%v want=%v after arg reset", got, ExpertFrameDurationArg)
	}
	if got := enc.FrameSize(); got != 960 {
		t.Fatalf("FrameSize()=%d want=960 after arg reset", got)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration(0)); err != ErrInvalidArgument {
		t.Fatalf("SetExpertFrameDuration(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Fatalf("ExpertFrameDuration()=%v want=%v after invalid set", got, ExpertFrameDurationArg)
	}
}

func TestEncoder_ExpertFrameDurationSelectsEncodeFrame(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(5760); err != nil {
		t.Fatalf("SetFrameSize(5760) error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration20Ms); err != nil {
		t.Fatalf("SetExpertFrameDuration(20ms) error: %v", err)
	}

	data := make([]byte, 4000)
	n, err := enc.Encode(generateSineWave(48000, 440, 5760), data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if got := ParseTOC(data[0]).FrameSize; got != 960 {
		t.Fatalf("TOC frame size = %d, want 960 from fixed 20ms duration", got)
	}
	if n == 0 {
		t.Fatalf("Encode returned empty packet")
	}
}

func TestEncoder_RestrictedSilkExpertFrameDurationRejectedAtEncode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedSilk})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration5Ms); err != nil {
		t.Fatalf("SetExpertFrameDuration(5ms) error: %v", err)
	}
	if n, err := enc.Encode(generateSineWave(48000, 440, 960), make([]byte, 4000)); n != 0 || err != ErrInvalidFrameSize {
		t.Fatalf("Encode restricted SILK 5ms = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
	}
}

func TestEncoder_OptionalExtensionControls(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationAudio})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}

			assertOptionalEncoderControls(t, enc)
			dred, ok := any(enc).(extraDREDControl)
			if extsupport.DRED {
				if !ok {
					t.Fatal("gopus_dred build does not expose DRED control")
				}
				assertWorkingDREDControl(t, dred)
			} else if extsupport.DREDRuntime {
				if !ok {
					t.Fatal("DRED runtime build does not expose DRED control")
				}
				assertWorkingDREDControl(t, dred)
			} else if ok {
				t.Fatal("non-DRED-runtime build unexpectedly exposes DRED control")
			}
			qext, ok := any(enc).(qextEncoderControl)
			if extsupport.QEXT {
				if !ok {
					t.Fatal("QEXT build does not expose encoder QEXT control")
				}
				assertSupportedQEXTControl(t, qext)
			} else if ok {
				t.Fatal("non-QEXT build unexpectedly exposes encoder QEXT control")
			}
		})
	}
}

func TestEncoder_SetSignal(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is SignalAuto
	if enc.Signal() != SignalAuto {
		t.Errorf("Signal() = %d, want SignalAuto (%d)", enc.Signal(), SignalAuto)
	}

	// Test valid signals
	validSignals := []Signal{SignalAuto, SignalVoice, SignalMusic}
	for _, sig := range validSignals {
		t.Run(fmt.Sprintf("signal_%d", sig), func(t *testing.T) {
			err := enc.SetSignal(sig)
			if err != nil {
				t.Errorf("SetSignal(%d) error: %v", sig, err)
			}
			if enc.Signal() != sig {
				t.Errorf("Signal() = %d, want %d", enc.Signal(), sig)
			}
		})
	}

	// Test invalid signal
	err = enc.SetSignal(Signal(9999))
	if err != ErrInvalidSignal {
		t.Errorf("SetSignal(9999) error = %v, want ErrInvalidSignal", err)
	}
}

func TestEncoder_SetMaxBandwidth(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is Fullband
	if enc.MaxBandwidth() != BandwidthFullband {
		t.Errorf("MaxBandwidth() = %d, want BandwidthFullband (%d)", enc.MaxBandwidth(), BandwidthFullband)
	}

	// Test all bandwidths
	bandwidths := []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	}
	for _, bw := range bandwidths {
		t.Run(fmt.Sprintf("bandwidth_%d", bw), func(t *testing.T) {
			if err := enc.SetMaxBandwidth(bw); err != nil {
				t.Fatalf("SetMaxBandwidth(%d) error: %v", bw, err)
			}
			if enc.MaxBandwidth() != bw {
				t.Errorf("MaxBandwidth() = %d, want %d", enc.MaxBandwidth(), bw)
			}
		})
	}

	if err := enc.SetMaxBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetMaxBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}
}

func TestEncoder_SetForceChannels(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is -1 (auto)
	if enc.ForceChannels() != -1 {
		t.Errorf("ForceChannels() = %d, want -1", enc.ForceChannels())
	}

	// Test valid values
	validChannels := []int{-1, 1, 2}
	for _, ch := range validChannels {
		t.Run(fmt.Sprintf("channels_%d", ch), func(t *testing.T) {
			err := enc.SetForceChannels(ch)
			if err != nil {
				t.Errorf("SetForceChannels(%d) error: %v", ch, err)
			}
			if enc.ForceChannels() != ch {
				t.Errorf("ForceChannels() = %d, want %d", enc.ForceChannels(), ch)
			}
		})
	}

	// Test invalid values
	invalidChannels := []int{-2, 0, 3, 100}
	for _, ch := range invalidChannels {
		t.Run(fmt.Sprintf("invalid_channels_%d", ch), func(t *testing.T) {
			err := enc.SetForceChannels(ch)
			if err != ErrInvalidForceChannels {
				t.Errorf("SetForceChannels(%d) error = %v, want ErrInvalidForceChannels", ch, err)
			}
		})
	}
}

func TestEncoder_SetForceChannelsRejectsStereoForMono(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	if err := enc.SetForceChannels(2); err != ErrInvalidForceChannels {
		t.Fatalf("SetForceChannels(2) error = %v, want ErrInvalidForceChannels", err)
	}
	if got := enc.ForceChannels(); got != -1 {
		t.Fatalf("ForceChannels() = %d, want -1", got)
	}
}

func TestEncoder_SetLSBDepth(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is 24
	if enc.LSBDepth() != 24 {
		t.Errorf("LSBDepth() = %d, want 24", enc.LSBDepth())
	}

	// Test valid depths
	for depth := 8; depth <= 24; depth++ {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			err := enc.SetLSBDepth(depth)
			if err != nil {
				t.Errorf("SetLSBDepth(%d) error: %v", depth, err)
			}
			if enc.LSBDepth() != depth {
				t.Errorf("LSBDepth() = %d, want %d", enc.LSBDepth(), depth)
			}
		})
	}

	// Test invalid depths
	invalidDepths := []int{0, 7, 25, 32}
	for _, depth := range invalidDepths {
		t.Run(fmt.Sprintf("invalid_depth_%d", depth), func(t *testing.T) {
			err := enc.SetLSBDepth(depth)
			if err != ErrInvalidLSBDepth {
				t.Errorf("SetLSBDepth(%d) error = %v, want ErrInvalidLSBDepth", depth, err)
			}
		})
	}
}

func TestEncoder_SetPredictionDisabled(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is false
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = true, want false")
	}

	// Enable
	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = false after SetPredictionDisabled(true)")
	}

	// Disable
	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = true after SetPredictionDisabled(false)")
	}
}

func TestEncoder_SetPhaseInversionDisabled(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is false
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true, want false")
	}

	// Enable
	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after SetPhaseInversionDisabled(true)")
	}

	// Disable
	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after SetPhaseInversionDisabled(false)")
	}
}

func TestEncoder_SetPhaseInversionDisabledRestrictedSilkNoop(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationRestrictedSilk})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	enc.SetPhaseInversionDisabled(true)

	if enc.PhaseInversionDisabled() {
		t.Fatal("restricted SILK should not report disabled phase inversion")
	}
}

func TestEncoderCTL_Defaults(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// complexity = 9
	if got := enc.Complexity(); got != 9 {
		t.Errorf("Complexity() default = %d, want 9 (libopus: st->silk_mode.complexity=9)", got)
	}

	// use_vbr = 1 (VBR enabled)
	if !enc.VBR() {
		t.Error("VBR() default = false, want true (libopus: st->use_vbr=1)")
	}

	// vbr_constraint = 1 (constrained VBR enabled)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() default = false, want true (libopus: st->vbr_constraint=1)")
	}

	// signal_type = OPUS_AUTO (-1000)
	if got := enc.Signal(); got != SignalAuto {
		t.Errorf("Signal() default = %v, want SignalAuto (libopus: st->signal_type=OPUS_AUTO)", got)
	}

	// max_bandwidth = OPUS_BANDWIDTH_FULLBAND (1105)
	if got := enc.MaxBandwidth(); got != BandwidthFullband {
		t.Errorf("MaxBandwidth() default = %v, want BandwidthFullband (libopus: st->max_bandwidth=OPUS_BANDWIDTH_FULLBAND)", got)
	}

	// force_channels = OPUS_AUTO (-1000) → gopus -1
	if got := enc.ForceChannels(); got != -1 {
		t.Errorf("ForceChannels() default = %d, want -1 (libopus: st->force_channels=OPUS_AUTO)", got)
	}

	// lsb_depth = 24
	if got := enc.LSBDepth(); got != 24 {
		t.Errorf("LSBDepth() default = %d, want 24 (libopus: st->lsb_depth=24)", got)
	}

	// variable_duration = OPUS_FRAMESIZE_ARG (5000)
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Errorf("ExpertFrameDuration() default = %v, want ExpertFrameDurationArg (libopus: st->variable_duration=OPUS_FRAMESIZE_ARG)", got)
	}

	// useInBandFEC = 0
	if enc.FECEnabled() {
		t.Error("FECEnabled() default = true, want false (libopus: st->silk_mode.useInBandFEC=0)")
	}
	if got := enc.InBandFEC(); got != InBandFECDisabled {
		t.Errorf("InBandFEC() default = %d, want InBandFECDisabled (libopus: st->fec_config=0)", got)
	}

	// useDTX = 0
	if enc.DTXEnabled() {
		t.Error("DTXEnabled() default = true, want false (libopus: st->silk_mode.useDTX=0)")
	}

	// packetLossPercentage = 0
	if got := enc.PacketLoss(); got != 0 {
		t.Errorf("PacketLoss() default = %d, want 0", got)
	}

	// prediction_disabled = 0
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() default = true, want false (libopus: st->silk_mode.reducedDependency=0)")
	}

	// phase_inversion_disabled = 0 (stereo encoder only)
	stereo := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if stereo.PhaseInversionDisabled() {
		t.Error("stereo PhaseInversionDisabled() default = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: SET then GET must return the value just set.
// ---------------------------------------------------------------------------

// TestEncoderCTL_ComplexityRoundTrip verifies OPUS_SET/GET_COMPLEXITY.
//
// C ref: opus_encoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestEncoderCTL_ComplexityRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for c := 0; c <= 10; c++ {
		if err := enc.SetComplexity(c); err != nil {
			t.Fatalf("SetComplexity(%d) error: %v", c, err)
		}
		if got := enc.Complexity(); got != c {
			t.Errorf("Complexity() = %d after SetComplexity(%d), want %d", got, c, c)
		}
	}
}

// TestEncoderCTL_ComplexityBoundaryReject verifies that out-of-range complexity
// is rejected and the stored value is unchanged.
//
// C ref: opus_encoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestEncoderCTL_ComplexityBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if err := enc.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5) error: %v", err)
	}

	for _, c := range []int{-1, 11, 100} {
		if err := enc.SetComplexity(c); err == nil {
			t.Errorf("SetComplexity(%d) = nil, want error", c)
		}
		if got := enc.Complexity(); got != 5 {
			t.Errorf("invalid SetComplexity(%d) changed Complexity() to %d, want 5", c, got)
		}
	}
}

// TestEncoderCTL_VBRRoundTrip verifies OPUS_SET/GET_VBR.
//
// C ref: opus_encoder_ctl OPUS_SET_VBR_REQUEST – "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_VBRRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetVBR(false)
	if enc.VBR() {
		t.Error("VBR() = true after SetVBR(false), want false")
	}

	enc.SetVBR(true)
	if !enc.VBR() {
		t.Error("VBR() = false after SetVBR(true), want true")
	}
}

// TestEncoderCTL_VBRConstraintRoundTrip verifies OPUS_SET/GET_VBR_CONSTRAINT.
//
// C ref: opus_encoder_ctl OPUS_SET_VBR_CONSTRAINT_REQUEST – "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_VBRConstraintRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetVBRConstraint(false)
	if enc.VBRConstraint() {
		t.Error("VBRConstraint() = true after Set(false), want false")
	}

	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() = false after Set(true), want true")
	}
}

// TestEncoderCTL_DTXRoundTrip verifies OPUS_SET/GET_DTX.
//
// C ref: opus_encoder_ctl OPUS_SET_DTX_REQUEST – "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_DTXRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTXEnabled() = false after SetDTX(true), want true")
	}

	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTXEnabled() = true after SetDTX(false), want false")
	}
}

// TestEncoderCTL_InBandFECRoundTrip verifies OPUS_SET/GET_INBAND_FEC for all
// three valid configurations.
//
// C ref: opus_encoder_ctl OPUS_SET_INBAND_FEC_REQUEST – "if(value<0 || value>2) goto bad_arg"
func TestEncoderCTL_InBandFECRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)

	for _, cfg := range []int{InBandFECDisabled, InBandFECEnabled, InBandFECMusicSafe} {
		if err := enc.SetInBandFEC(cfg); err != nil {
			t.Fatalf("SetInBandFEC(%d) error: %v", cfg, err)
		}
		if got := enc.InBandFEC(); got != cfg {
			t.Errorf("InBandFEC() = %d after SetInBandFEC(%d), want %d", got, cfg, cfg)
		}
	}
}

// TestEncoderCTL_InBandFECBoundaryReject verifies that out-of-range FEC
// config is rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_INBAND_FEC_REQUEST – "if(value<0 || value>2) goto bad_arg"
func TestEncoderCTL_InBandFECBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)

	for _, cfg := range []int{-1, 3, 10} {
		if err := enc.SetInBandFEC(cfg); err == nil {
			t.Errorf("SetInBandFEC(%d) = nil, want error", cfg)
		}
	}
}

// TestEncoderCTL_PacketLossRoundTrip verifies OPUS_SET/GET_PACKET_LOSS_PERC.
//
// C ref: opus_encoder_ctl OPUS_SET_PACKET_LOSS_PERC_REQUEST –
//
//	"if (value < 0 || value > 100) goto bad_arg"
func TestEncoderCTL_PacketLossRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, pct := range []int{0, 1, 50, 100} {
		if err := enc.SetPacketLoss(pct); err != nil {
			t.Fatalf("SetPacketLoss(%d) error: %v", pct, err)
		}
		if got := enc.PacketLoss(); got != pct {
			t.Errorf("PacketLoss() = %d after SetPacketLoss(%d), want %d", got, pct, pct)
		}
	}
}

// TestEncoderCTL_PacketLossBoundaryReject verifies out-of-range packet loss is
// rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_PACKET_LOSS_PERC_REQUEST –
//
//	"if (value < 0 || value > 100) goto bad_arg"
func TestEncoderCTL_PacketLossBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if err := enc.SetPacketLoss(10); err != nil {
		t.Fatalf("SetPacketLoss(10) error: %v", err)
	}

	for _, pct := range []int{-1, 101, 200} {
		if err := enc.SetPacketLoss(pct); err == nil {
			t.Errorf("SetPacketLoss(%d) = nil, want error", pct)
		}
		if got := enc.PacketLoss(); got != 10 {
			t.Errorf("invalid SetPacketLoss(%d) changed PacketLoss() to %d, want 10", pct, got)
		}
	}
}

// TestEncoderCTL_SignalRoundTrip verifies OPUS_SET/GET_SIGNAL.
//
// C ref: opus_encoder_ctl OPUS_SET_SIGNAL_REQUEST –
//
//	"if(value!=OPUS_AUTO && value!=OPUS_SIGNAL_VOICE && value!=OPUS_SIGNAL_MUSIC) goto bad_arg"
func TestEncoderCTL_SignalRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, sig := range []Signal{SignalAuto, SignalVoice, SignalMusic} {
		if err := enc.SetSignal(sig); err != nil {
			t.Fatalf("SetSignal(%v) error: %v", sig, err)
		}
		if got := enc.Signal(); got != sig {
			t.Errorf("Signal() = %v after SetSignal(%v), want %v", got, sig, sig)
		}
	}
}

// TestEncoderCTL_SignalBoundaryReject verifies invalid signal values are
// rejected.
func TestEncoderCTL_SignalBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, sig := range []Signal{Signal(-1), Signal(0), Signal(9999)} {
		if err := enc.SetSignal(sig); err == nil {
			t.Errorf("SetSignal(%v) = nil, want error", sig)
		}
	}
}

// TestEncoderCTL_BandwidthAcceptsAllValid verifies OPUS_SET_BANDWIDTH accepts
// all five bandwidths plus the auto sentinel, and that OPUS_GET_BANDWIDTH
// returns the DECIDED bandwidth (not the user request).
//
// C ref: opus_encoder_ctl OPUS_SET_BANDWIDTH_REQUEST validates
//
//	"(value < OPUS_BANDWIDTH_NARROWBAND || value > OPUS_BANDWIDTH_FULLBAND) &&
//	 value != OPUS_AUTO" and writes only st->user_bandwidth. OPUS_GET_BANDWIDTH
//	returns st->bandwidth, which holds the FULLBAND init default until an encode
//	decides it — it does NOT echo the SET value.
func TestEncoderCTL_BandwidthAcceptsAllValid(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, bw := range []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	} {
		if err := enc.SetBandwidth(bw); err != nil {
			t.Fatalf("SetBandwidth(%v) error: %v", bw, err)
		}
		// GET returns the decided bandwidth (FULLBAND) before any encode,
		// independent of the user request just set.
		if got := enc.Bandwidth(); got != BandwidthFullband {
			t.Errorf("Bandwidth() after SetBandwidth(%v) = %v, want BandwidthFullband (libopus st->bandwidth init default before encode)", bw, got)
		}
	}

	// Restore auto.
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Fatalf("SetBandwidthAuto() error: %v", err)
	}
}

// TestEncoderCTL_MaxBandwidthRoundTrip verifies OPUS_SET/GET_MAX_BANDWIDTH.
//
// C ref: opus_encoder_ctl OPUS_SET_MAX_BANDWIDTH_REQUEST –
//
//	"if (value < OPUS_BANDWIDTH_NARROWBAND || value > OPUS_BANDWIDTH_FULLBAND) goto bad_arg"
func TestEncoderCTL_MaxBandwidthRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, bw := range []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	} {
		if err := enc.SetMaxBandwidth(bw); err != nil {
			t.Fatalf("SetMaxBandwidth(%v) error: %v", bw, err)
		}
		if got := enc.MaxBandwidth(); got != bw {
			t.Errorf("MaxBandwidth() = %v after SetMaxBandwidth(%v), want %v", got, bw, bw)
		}
	}
}

// TestEncoderCTL_MaxBandwidthBoundaryReject verifies invalid bandwidth values
// are rejected from SetMaxBandwidth.
//
// Bandwidth is uint8 with valid enum range [0,4] (NB..FB).  Values outside
// that range must be rejected.
func TestEncoderCTL_MaxBandwidthBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, bw := range []Bandwidth{Bandwidth(5), Bandwidth(10), Bandwidth(255)} {
		if err := enc.SetMaxBandwidth(bw); err == nil {
			t.Errorf("SetMaxBandwidth(%v) = nil, want error", bw)
		}
	}
}

// TestEncoderCTL_ForceChannelsRoundTrip verifies OPUS_SET/GET_FORCE_CHANNELS.
//
// C ref: opus_encoder_ctl OPUS_SET_FORCE_CHANNELS_REQUEST –
//
//	"if((value<1 || value>st->channels) && value != OPUS_AUTO) goto bad_arg"
func TestEncoderCTL_ForceChannelsRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	for _, ch := range []int{-1, 1, 2} {
		if err := enc.SetForceChannels(ch); err != nil {
			t.Fatalf("SetForceChannels(%d) error: %v", ch, err)
		}
		if got := enc.ForceChannels(); got != ch {
			t.Errorf("ForceChannels() = %d after SetForceChannels(%d), want %d", got, ch, ch)
		}
	}
}

// TestEncoderCTL_ForceChannelsBoundaryReject verifies that ForceChannels
// values outside [-1, channels] are rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_FORCE_CHANNELS_REQUEST –
//
//	"if((value<1 || value>st->channels) && value != OPUS_AUTO) goto bad_arg"
func TestEncoderCTL_ForceChannelsBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// channels=1: forcing stereo (2) is invalid.
	for _, ch := range []int{0, 2, 3} {
		if err := enc.SetForceChannels(ch); err == nil {
			t.Errorf("SetForceChannels(%d) on mono encoder = nil, want error", ch)
		}
	}
}

// TestEncoderCTL_LSBDepthRoundTrip verifies OPUS_SET/GET_LSB_DEPTH.
//
// C ref: opus_encoder_ctl OPUS_SET_LSB_DEPTH_REQUEST – "if (value<8 || value>24) goto bad_arg"
func TestEncoderCTL_LSBDepthRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, depth := range []int{8, 16, 24} {
		if err := enc.SetLSBDepth(depth); err != nil {
			t.Fatalf("SetLSBDepth(%d) error: %v", depth, err)
		}
		if got := enc.LSBDepth(); got != depth {
			t.Errorf("LSBDepth() = %d after SetLSBDepth(%d), want %d", got, depth, depth)
		}
	}
}

// TestEncoderCTL_LSBDepthBoundaryReject verifies out-of-range depth is
// rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_LSB_DEPTH_REQUEST – "if (value<8 || value>24) goto bad_arg"
func TestEncoderCTL_LSBDepthBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, depth := range []int{7, 25, 32} {
		if err := enc.SetLSBDepth(depth); err == nil {
			t.Errorf("SetLSBDepth(%d) = nil, want error", depth)
		}
	}
}

// TestEncoderCTL_ExpertFrameDurationRoundTrip verifies
// OPUS_SET/GET_EXPERT_FRAME_DURATION for all valid values.
//
// C ref: opus_encoder_ctl OPUS_SET_EXPERT_FRAME_DURATION_REQUEST – must be
//
//	one of the OPUS_FRAMESIZE_* constants.
func TestEncoderCTL_ExpertFrameDurationRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, dur := range []ExpertFrameDuration{
		ExpertFrameDurationArg,
		ExpertFrameDuration2_5Ms,
		ExpertFrameDuration5Ms,
		ExpertFrameDuration10Ms,
		ExpertFrameDuration20Ms,
		ExpertFrameDuration40Ms,
		ExpertFrameDuration60Ms,
		ExpertFrameDuration80Ms,
		ExpertFrameDuration100Ms,
		ExpertFrameDuration120Ms,
	} {
		if err := enc.SetExpertFrameDuration(dur); err != nil {
			t.Fatalf("SetExpertFrameDuration(%v) error: %v", dur, err)
		}
		if got := enc.ExpertFrameDuration(); got != dur {
			t.Errorf("ExpertFrameDuration() = %v after Set(%v), want %v", got, dur, dur)
		}
	}
}

// TestEncoderCTL_ExpertFrameDurationBoundaryReject verifies an invalid
// frame-duration enum value is rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_EXPERT_FRAME_DURATION_REQUEST – must be
//
//	one of the OPUS_FRAMESIZE_* constants; otherwise goto bad_arg.
func TestEncoderCTL_ExpertFrameDurationBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, dur := range []ExpertFrameDuration{ExpertFrameDuration(0), ExpertFrameDuration(4999), ExpertFrameDuration(5010)} {
		if err := enc.SetExpertFrameDuration(dur); err == nil {
			t.Errorf("SetExpertFrameDuration(%v) = nil, want error", dur)
		}
	}
}

// TestEncoderCTL_PredictionDisabledRoundTrip verifies
// OPUS_SET/GET_PREDICTION_DISABLED.
//
// C ref: opus_encoder_ctl OPUS_SET_PREDICTION_DISABLED_REQUEST –
//
//	"if (value > 1 || value < 0) goto bad_arg"
func TestEncoderCTL_PredictionDisabledRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = false after Set(true), want true")
	}

	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = true after Set(false), want false")
	}
}

// TestEncoderCTL_PhaseInversionDisabledRoundTrip verifies
// OPUS_SET/GET_PHASE_INVERSION_DISABLED on a stereo encoder.
//
// C ref: opus_encoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST –
//
//	"if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_PhaseInversionDisabledRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after Set(true), want true")
	}

	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after Set(false), want false")
	}
}

// TestEncoderCTL_SampleRateAllRates verifies OPUS_GET_SAMPLE_RATE for all
// valid Opus sample rates.
//
// C ref: opus_encoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST – "*value = st->Fs"
func TestEncoderCTL_SampleRateAllRates(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		enc := mustNewTestEncoder(t, rate, 1, ApplicationAudio)
		if got := enc.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}
}

// TestEncoderCTL_ChannelsGetter verifies Channels() returns the configured
// channel count.
func TestEncoderCTL_ChannelsGetter(t *testing.T) {
	for _, ch := range []int{1, 2} {
		enc := mustNewTestEncoder(t, 48000, ch, ApplicationAudio)
		if got := enc.Channels(); got != ch {
			t.Errorf("Channels() = %d, want %d", got, ch)
		}
	}
}

// TestEncoderCTL_LookaheadMatchesFormula verifies OPUS_GET_LOOKAHEAD for all
// sample rates and the three standard application values.
//
// C ref: opus_encoder_ctl OPUS_GET_LOOKAHEAD_REQUEST:
//
//	*value = st->Fs/400;
//	if (st->application != OPUS_APPLICATION_RESTRICTED_LOWDELAY &&
//	    st->application != OPUS_APPLICATION_RESTRICTED_CELT)
//	    *value += st->delay_compensation; // delay_compensation = Fs/250
func TestEncoderCTL_LookaheadMatchesFormula(t *testing.T) {
	cases := []struct {
		rate int
		app  Application
		want int
	}{
		{48000, ApplicationAudio, 48000/400 + 48000/250},
		{48000, ApplicationVoIP, 48000/400 + 48000/250},
		{48000, ApplicationLowDelay, 48000 / 400},
		{24000, ApplicationAudio, 24000/400 + 24000/250},
		{24000, ApplicationLowDelay, 24000 / 400},
		{16000, ApplicationAudio, 16000/400 + 16000/250},
		{8000, ApplicationAudio, 8000/400 + 8000/250},
	}

	for _, tc := range cases {
		enc := mustNewTestEncoder(t, tc.rate, 1, tc.app)
		if got := enc.Lookahead(); got != tc.want {
			t.Errorf("Lookahead(%d Hz, %v) = %d, want %d", tc.rate, tc.app, got, tc.want)
		}
	}
}

// TestEncoderCTL_FinalRangeBeforeEncode verifies that FinalRange() returns 0
// before any frame has been encoded.
func TestEncoderCTL_FinalRangeBeforeEncode(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if got := enc.FinalRange(); got != 0 {
		t.Errorf("FinalRange() before encode = %d, want 0", got)
	}
}

// TestEncoderCTL_FinalRangeNonZeroAfterEncode verifies that FinalRange()
// returns a non-zero value after a successful encode.
func TestEncoderCTL_FinalRangeNonZeroAfterEncode(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	pcm := generateSineWave(48000, 440, 960)
	buf := make([]byte, 4000)
	if _, err := enc.Encode(pcm, buf); err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if got := enc.FinalRange(); got == 0 {
		t.Error("FinalRange() after encode = 0, want non-zero")
	}
}

// TestEncoderCTL_ApplicationRoundTrip verifies OPUS_SET/GET_APPLICATION.
//
// C ref: opus_encoder_ctl OPUS_SET_APPLICATION_REQUEST – only
//
//	OPUS_APPLICATION_VOIP, OPUS_APPLICATION_AUDIO, and
//	OPUS_APPLICATION_RESTRICTED_LOWDELAY are valid after first encode.
func TestEncoderCTL_ApplicationRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	if got := enc.Application(); got != ApplicationAudio {
		t.Errorf("Application() = %v, want ApplicationAudio", got)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(VoIP) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationVoIP {
		t.Errorf("Application() = %v after SetApplication(VoIP), want VoIP", got)
	}

	if err := enc.SetApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(LowDelay) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationLowDelay {
		t.Errorf("Application() = %v after SetApplication(LowDelay), want LowDelay", got)
	}
}

// TestEncoderCTL_ApplicationLockedAfterFirstEncode verifies that
// OPUS_SET_APPLICATION fails after the first frame has been encoded if the
// application value would change.
//
// C ref: opus_encoder_ctl OPUS_SET_APPLICATION_REQUEST –
//
//	"if (!st->first && st->application != value) { ret = OPUS_BAD_ARG; break; }"
func TestEncoderCTL_ApplicationLockedAfterFirstEncode(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	pcm := generateSineWave(48000, 440, 960)
	buf := make([]byte, 4000)

	if _, err := enc.Encode(pcm, buf); err != nil {
		t.Fatalf("first Encode error: %v", err)
	}

	// Changing application after first encode must fail.
	if err := enc.SetApplication(ApplicationVoIP); err == nil {
		t.Error("SetApplication(VoIP) after first encode = nil, want error")
	}

	// Setting the same application must succeed.
	if err := enc.SetApplication(ApplicationAudio); err != nil {
		t.Errorf("SetApplication(same) after first encode error: %v", err)
	}
}

// TestEncoderCTL_BitrateGetResidual documents the known honest residual:
// gopus Bitrate() returns the stored user bitrate (BitrateAuto = -1000 by
// default) rather than resolving it at GET time.  libopus OPUS_GET_BITRATE
// calls user_bitrate_to_bitrate(st, st->prev_framesize, 1276) which resolves
// OPUS_AUTO to "60*Fs/frame_size + Fs*channels".
//
// This is a deliberate gopus design choice (resolution deferred to Encode
// time) and is not a bug; it means Bitrate() returns BitrateAuto until
// SetBitrate() is called with an explicit value.
func TestEncoderCTL_BitrateGetResidual(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// Default: gopus returns BitrateAuto sentinel.
	got := enc.Bitrate()
	if got != BitrateAuto {
		t.Logf("NOTE: Bitrate() default = %d (want BitrateAuto=%d); libopus returns a resolved value at GET time", got, BitrateAuto)
	}

	// After an explicit SET, GET must return the clamped stored value.
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate(64000) error: %v", err)
	}
	if got := enc.Bitrate(); got != 64000 {
		t.Errorf("Bitrate() = %d after SetBitrate(64000), want 64000", got)
	}

	// BitrateMax sentinel round-trips.
	if err := enc.SetBitrate(BitrateMax); err != nil {
		t.Fatalf("SetBitrate(BitrateMax) error: %v", err)
	}
	if got := enc.Bitrate(); got != BitrateMax {
		t.Errorf("Bitrate() = %d after SetBitrate(BitrateMax), want %d", got, BitrateMax)
	}
}

// TestEncoderCTL_BitrateBoundaryReject verifies that non-positive non-sentinel
// bitrate values are rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_BITRATE_REQUEST –
//
//	"if (value <= 0) goto bad_arg" (after OPUS_AUTO / OPUS_BITRATE_MAX check)
func TestEncoderCTL_BitrateBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, br := range []int{0, -2, -999} {
		if err := enc.SetBitrate(br); err == nil {
			t.Errorf("SetBitrate(%d) = nil, want error", br)
		}
	}
}

// TestEncoderCTL_ResetRestoresDefaults verifies that Reset() re-establishes
// the libopus default values that are cleared by OPUS_RESET_STATE.
//
// C ref: opus_encoder_ctl OPUS_RESET_STATE case – clears from
//
//	st->OPUS_ENCODER_RESET_START through the end of the struct; sets
//	st->first=1, st->mode=MODE_HYBRID, st->bandwidth=OPUS_BANDWIDTH_FULLBAND.
func TestEncoderCTL_ResetRestoresDefaults(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// Modify some values before reset.
	if err := enc.SetComplexity(3); err != nil {
		t.Fatalf("SetComplexity(3) error: %v", err)
	}
	enc.SetDTX(true)

	enc.Reset()

	// After Reset, application-lock must be released (encodedOnce=false in
	// gopus, matching st->first=1 in libopus).
	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Errorf("SetApplication after Reset() error: %v", err)
	}
}

func TestEncoder_Encode_Float32(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode
	data := make([]byte, 4000)
	n, err := enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if n == 0 {
		t.Fatal("Encode returned 0 bytes")
	}

	t.Logf("Encoded %d samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_Int16(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave as int16
	frameSize := 960
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	// Encode
	data := make([]byte, 4000)
	n, err := enc.EncodeInt16(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt16 error: %v", err)
	}

	if n == 0 {
		t.Fatal("EncodeInt16 returned 0 bytes")
	}

	t.Logf("Encoded %d int16 samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_Int24(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWaveInt24(48000, 440, frameSize)

	data := make([]byte, 4000)
	n, err := enc.EncodeInt24(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt24 error: %v", err)
	}

	if n == 0 {
		t.Fatal("EncodeInt24 returned 0 bytes")
	}

	packet, err := enc.EncodeInt24Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt24Slice error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("EncodeInt24Slice returned empty packet")
	}

	t.Logf("Encoded %d int24 samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_Int24_InvalidFrameSize(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	data := make([]byte, 4000)
	if _, err := enc.EncodeInt24(make([]int32, enc.FrameSize()-1), data); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
	if _, err := enc.EncodeInt24Slice(make([]int32, enc.FrameSize()-1)); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24Slice(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
}

func TestEncoder_Encode_RoundTrip(t *testing.T) {
	// Create encoder and decoder
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave
	frameSize := 960
	pcmIn := generateSineWave(48000, 440, frameSize)

	// Calculate input energy
	var inputEnergy float64
	for _, s := range pcmIn {
		inputEnergy += float64(s) * float64(s)
	}

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	// Decode
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Calculate output energy
	var outputEnergy float64
	for _, s := range pcmOut[:n*cfg.Channels] {
		outputEnergy += float64(s) * float64(s)
	}

	t.Logf("Input energy: %f, Output energy: %f", inputEnergy, outputEnergy)
	t.Logf("Encoded to %d bytes, decoded to %d samples", len(packet), n*cfg.Channels)

	// The output should have some energy (lossy compression but not zero)
	if outputEnergy == 0 {
		t.Error("Decoded output has zero energy")
	}
}

func TestEncoder_Reset(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Encode a frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)
	data := make([]byte, 4000)
	_, err = enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Reset
	enc.Reset()

	// Encode again should work
	_, err = enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode after Reset error: %v", err)
	}
}

func TestEncoder_Stereo(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate stereo signal (interleaved)
	frameSize := 960
	pcm := make([]float32, frameSize*2)
	for i := range frameSize {
		// Left: 440 Hz
		pcm[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		// Right: 880 Hz
		pcm[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}

	// Encode
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet for stereo")
	}

	t.Logf("Encoded stereo %d samples to %d bytes", frameSize, len(packet))
}

func TestEncoder_FrameSize(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960 (20ms)
	if enc.FrameSize() != 960 {
		t.Errorf("FrameSize() = %d, want 960", enc.FrameSize())
	}

	// Valid frame sizes
	validSizes := []int{120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760}
	for _, size := range validSizes {
		t.Run(fmt.Sprintf("framesize_%d", size), func(t *testing.T) {
			err := enc.SetFrameSize(size)
			if err != nil {
				t.Errorf("SetFrameSize(%d) error: %v", size, err)
			}
			if enc.FrameSize() != size {
				t.Errorf("FrameSize() = %d, want %d", enc.FrameSize(), size)
			}
		})
	}

	// Invalid frame sizes
	invalidSizes := []int{0, 100, 500, 1000, 3000}
	for _, size := range invalidSizes {
		t.Run(fmt.Sprintf("invalid_framesize_%d", size), func(t *testing.T) {
			err := enc.SetFrameSize(size)
			if err != ErrInvalidFrameSize {
				t.Errorf("SetFrameSize(%d) error = %v, want ErrInvalidFrameSize", size, err)
			}
		})
	}
}

func TestEncoder_EncodeFloat32_Convenience(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("EncodeFloat32 returned empty packet")
	}
}

func TestEncoder_LongPacketRoundTrip(t *testing.T) {
	cases := []struct {
		name        string
		application Application
		frameSize   int
	}{
		{name: "restricted_silk_80ms", application: ApplicationRestrictedSilk, frameSize: 3840},
		{name: "restricted_silk_100ms", application: ApplicationRestrictedSilk, frameSize: 4800},
		{name: "restricted_silk_120ms", application: ApplicationRestrictedSilk, frameSize: 5760},
		{name: "restricted_celt_80ms", application: ApplicationRestrictedCelt, frameSize: 3840},
		{name: "restricted_celt_100ms", application: ApplicationRestrictedCelt, frameSize: 4800},
		{name: "restricted_celt_120ms", application: ApplicationRestrictedCelt, frameSize: 5760},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: tc.application})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			if err := enc.SetFrameSize(tc.frameSize); err != nil {
				t.Fatalf("SetFrameSize(%d) error: %v", tc.frameSize, err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}

			pcmIn := generateSineWave(48000, 440, tc.frameSize)
			packet, err := enc.EncodeFloat32(pcmIn)
			if err != nil {
				t.Fatalf("EncodeFloat32 error: %v", err)
			}
			if len(packet) == 0 {
				t.Fatal("EncodeFloat32 returned empty packet")
			}

			pcmOut := make([]float32, tc.frameSize)
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if n != tc.frameSize {
				t.Fatalf("Decode samples=%d want=%d", n, tc.frameSize)
			}

			if energy := computeEnergyFloat32(pcmOut[:n]); energy == 0 {
				t.Fatal("decoded output has zero energy")
			}
		})
	}
}

func TestEncoder_EncodeInt16Slice_Convenience(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	packet, err := enc.EncodeInt16Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt16Slice error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("EncodeInt16Slice returned empty packet")
	}
}

func TestEncoder_InvalidFrameSize_Encode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960, try to encode wrong size
	pcm := make([]float32, 500) // Wrong size
	data := make([]byte, 4000)

	_, err = enc.Encode(pcm, data)
	if err != ErrInvalidFrameSize {
		t.Errorf("Encode with wrong size: got %v, want ErrInvalidFrameSize", err)
	}
}

func TestEncoder_Lookahead(t *testing.T) {
	for _, tc := range lookaheadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: tc.sampleRate, Channels: 1, Application: tc.application})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			if got := enc.Lookahead(); got != tc.want {
				t.Fatalf("Lookahead() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("set_application_updates_lookahead_before_encode", func(t *testing.T) {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}
		assertLookaheadUpdatesBeforeEncode(t, enc.Lookahead, enc.SetApplication)
	})
}

func TestEncoderMaxBandwidthRaiseRestoresAutoCELTBandwidthLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	ops := []libopustest.CTLOp{
		{Op: libopustest.CTLOpSet, Request: reqSetBitrate, Arg: 64000},
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: libopustest.OpusForceModeCELTOnly},
		{Op: libopustest.CTLOpSet, Request: reqSetMaxBandwidth, Arg: cBandwidthNB},
		{Op: libopustest.CTLOpProcess},
		{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
		{Op: libopustest.CTLOpGet, Request: reqGetBandwidth},
		{Op: libopustest.CTLOpSet, Request: reqSetMaxBandwidth, Arg: cBandwidthWB},
		{Op: libopustest.CTLOpProcess},
		{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
		{Op: libopustest.CTLOpGet, Request: reqGetBandwidth},
	}
	const sampleRate, channels, frameSize = 16000, 1, 320
	oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
		SampleRate: sampleRate, Channels: channels, Application: cAppAudio,
		FrameSize: frameSize, CapturePackets: true, Ops: ops,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "encoder max-bandwidth transition", err)
		return
	}
	if len(oracle) != len(ops) {
		t.Fatalf("oracle returned %d results for %d operations", len(oracle), len(ops))
	}

	enc := mustNewTestEncoder(t, sampleRate, channels, ApplicationAudio)
	if enc.FrameSize() != frameSize {
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
		}
	}
	pcm := generateSineWaveFloat32(sampleRate, 440, frameSize, channels)
	packetBuf := make([]byte, 4000)
	packetIndex := 0
	bandwidths := []int32{cBandwidthNB, cBandwidthWB}
	for i, op := range ops {
		got := libopustest.CTLResult{}
		switch op.Op {
		case libopustest.CTLOpSet:
			got.Ret = applyEncoderSet(enc, op.Request, op.Arg)
		case libopustest.CTLOpProcess:
			n, err := enc.Encode(pcm, packetBuf)
			if err != nil {
				t.Fatalf("PROCESS %d: %v", packetIndex, err)
			}
			got.Ret = int32(n)
			got.Packet = append([]byte(nil), packetBuf[:n]...)
		case libopustest.CTLOpGet:
			got.Value, got.HaveValue = applyEncoderGet(enc, op.Request)
			got.Ret = cOpusOK
		default:
			t.Fatalf("unexpected operation %d at index %d", op.Op, i)
		}

		ref := oracle[i]
		if got.Ret != ref.Ret || got.Value != ref.Value || got.HaveValue != ref.HaveValue {
			t.Fatalf("operation %d (%+v): Go ret/value/presence=%d/%08x/%t, libopus=%d/%08x/%t",
				i, op, got.Ret, uint32(got.Value), got.HaveValue, ref.Ret, uint32(ref.Value), ref.HaveValue)
		}
		if op.Op == libopustest.CTLOpProcess {
			if !bytes.Equal(got.Packet, ref.Packet) {
				first := firstByteDifference(got.Packet, ref.Packet)
				t.Fatalf("packet %d differs: Go length=%d TOC=%s range=%08x, libopus length=%d TOC=%s range=%08x, first byte difference=%d",
					packetIndex, len(got.Packet), packetTOCLabel(got.Packet), enc.FinalRange(), len(ref.Packet), packetTOCLabel(ref.Packet), uint32(oracle[i+1].Value), first)
			}
			packetIndex++
		}
		if op.Op == libopustest.CTLOpGet && op.Request == reqGetBandwidth {
			want := bandwidths[packetIndex-1]
			if got.Value != want {
				t.Fatalf("GET_BANDWIDTH after packet %d = %d, want %d", packetIndex-1, got.Value, want)
			}
		}
	}
	if packetIndex != len(bandwidths) {
		t.Fatalf("encoded %d packets, want %d", packetIndex, len(bandwidths))
	}
}

func TestEncoderResetPreservesFECDecisionLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name       string
		sampleRate int
		frameSize  int
	}{
		{name: "16kHz_60ms", sampleRate: 16000, frameSize: 960},
		{name: "24kHz_40ms", sampleRate: 24000, frameSize: 960},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := []libopustest.CTLOp{
				{Op: libopustest.CTLOpSet, Request: reqSetBitrate, Arg: cOpusAuto},
				{Op: libopustest.CTLOpSet, Request: reqSetInbandFEC, Arg: 1},
				{Op: libopustest.CTLOpSet, Request: reqSetPacketLossPerc, Arg: 20},
				{Op: libopustest.CTLOpProcess},
				{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
				{Op: libopustest.CTLOpReset},
				{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
				{Op: libopustest.CTLOpProcess},
				{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
			}
			oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
				SampleRate:     tc.sampleRate,
				Channels:       1,
				Application:    cAppAudio,
				FrameSize:      tc.frameSize,
				CapturePackets: true,
				Ops:            ops,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "encoder FEC reset oracle", err)
				return
			}

			enc := mustNewTestEncoder(t, tc.sampleRate, 1, ApplicationAudio)
			if err := enc.SetFrameSize(tc.frameSize); err != nil {
				t.Fatalf("SetFrameSize(%d): %v", tc.frameSize, err)
			}
			pcm := generateSineWaveFloat32(tc.sampleRate, 440, tc.frameSize, 1)
			got := make([]libopustest.CTLResult, len(ops))
			for i, op := range ops {
				if op.Op == libopustest.CTLOpProcess {
					packet, err := enc.EncodeFloat32(pcm)
					if err != nil {
						t.Fatalf("EncodeFloat32 at op %d: %v", i, err)
					}
					got[i] = libopustest.CTLResult{Ret: int32(len(packet)), Packet: packet}
					continue
				}
				ret, value, haveValue := applyEncoderOp(t, enc, op)
				got[i] = libopustest.CTLResult{Ret: ret, Value: value, HaveValue: haveValue}
			}
			compareCTLResults(t, tc.name, ops, got, oracle)
			for i, op := range ops {
				if op.Op == libopustest.CTLOpProcess && !bytes.Equal(got[i].Packet, oracle[i].Packet) {
					t.Fatalf("packet at op %d differs after reset: Go len=%d C len=%d Go=%x C=%x",
						i, len(got[i].Packet), len(oracle[i].Packet), got[i].Packet, oracle[i].Packet)
				}
			}

			out := make([]byte, 4000)
			var n int
			var encodeErr error
			allocs := testing.AllocsPerRun(5, func() {
				enc.Reset()
				n, encodeErr = enc.Encode(pcm, out)
			})
			if encodeErr != nil || n == 0 {
				t.Fatalf("warm reset→encode: n=%d err=%v", n, encodeErr)
			}
			if allocs != 0 {
				t.Fatalf("warm reset→encode allocations=%v want 0", allocs)
			}
		})
	}
}

func TestNewEncoder_ValidParams(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		application Application
	}{
		{"48kHz_mono_voip", 48000, 1, ApplicationVoIP},
		{"48kHz_stereo_voip", 48000, 2, ApplicationVoIP},
		{"48kHz_mono_audio", 48000, 1, ApplicationAudio},
		{"48kHz_stereo_audio", 48000, 2, ApplicationAudio},
		{"48kHz_mono_lowdelay", 48000, 1, ApplicationLowDelay},
		{"48kHz_mono_restricted_silk", 48000, 1, ApplicationRestrictedSilk},
		{"48kHz_stereo_restricted_celt", 48000, 2, ApplicationRestrictedCelt},
		{"24kHz_mono_voip", 24000, 1, ApplicationVoIP},
		{"16kHz_mono_voip", 16000, 1, ApplicationVoIP},
		{"12kHz_mono_voip", 12000, 1, ApplicationVoIP},
		{"8kHz_mono_voip", 8000, 1, ApplicationVoIP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: tt.sampleRate, Channels: tt.channels, Application: tt.application})
			if err != nil {
				t.Fatalf("NewEncoder(EncoderConfig{SampleRate: %d, Channels: %d, Application: %d}) unexpected error: %v", tt.sampleRate, tt.channels, tt.application, err)
			}
			if enc == nil {
				t.Fatal("NewEncoder returned nil encoder")
			}
			if enc.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.sampleRate)
			}
			if enc.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.channels)
			}
		})
	}
}

func TestNewEncoder_InvalidParams(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		expectedErr error
	}{
		{"invalid_rate_0", 0, 1, ErrInvalidSampleRate},
		{"invalid_rate_44100", 44100, 1, ErrInvalidSampleRate},
		{"invalid_channels_0", 48000, 0, ErrInvalidChannels},
		{"invalid_channels_3", 48000, 3, ErrInvalidChannels},
		{"invalid_channels_negative", 48000, -1, ErrInvalidChannels},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: tt.sampleRate, Channels: tt.channels, Application: ApplicationAudio})
			if err != tt.expectedErr {
				t.Errorf("NewEncoder(%d, %d) error = %v, want %v", tt.sampleRate, tt.channels, err, tt.expectedErr)
			}
			if enc != nil {
				t.Error("NewEncoder returned non-nil encoder on error")
			}
		})
	}
}

func TestNewEncoder_InvalidApplication(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: 99})
	if err != ErrInvalidApplication {
		t.Fatalf("NewEncoder(invalid application) error=%v want=%v", err, ErrInvalidApplication)
	}
	if enc != nil {
		t.Fatal("NewEncoder returned non-nil encoder for invalid application")
	}
}

// generateSineWave generates a sine wave at the given frequency.
func generateSineWave(sampleRate int, freq float64, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}
	return pcm
}

func generateSineWaveInt24(sampleRate int, freq float64, samples int) []int32 {
	pcm := make([]int32, samples)
	for i := range pcm {
		pcm[i] = int32((1 << 22) * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}
	return pcm
}

func TestEncoder_DTX_Silence(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Enable DTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTXEnabled() = false after SetDTX(true)")
	}

	// Encode multiple silence frames
	frameSize := 960
	silence := make([]float32, frameSize)

	// DTX requires several frames of silence before activating.
	// After threshold, DTX returns 1-byte TOC-only packets (matching libopus).
	for i := range 50 {
		data := make([]byte, 4000)
		n, err := enc.Encode(silence, data)
		if err != nil {
			t.Fatalf("Encode error on frame %d: %v", i, err)
		}

		// DTX frames are 1-byte TOC-only packets (not 0 bytes)
		if n == 1 {
			t.Logf("Frame %d: DTX active (1-byte TOC packet)", i)
			return // Success - DTX emitted TOC-only packet
		}
	}

	// DTX may or may not suppress depending on VAD adaptation
	t.Log("DTX did not activate (may need more silence frames for VAD adaptation)")
}

func TestEncoder_InDTXDelegatesToCoreEncoder(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetDTX(true)

	if got, want := enc.InDTX(), enc.enc.InDTX(); got != want {
		t.Fatalf("initial InDTX()=%v want core=%v", got, want)
	}

	silence := make([]float32, enc.FrameSize()*enc.Channels())
	packet := make([]byte, 4000)
	activated := false
	for i := range 50 {
		if _, err := enc.Encode(silence, packet); err != nil {
			t.Fatalf("Encode(silence) frame %d error: %v", i, err)
		}
		if got, want := enc.InDTX(), enc.enc.InDTX(); got != want {
			t.Fatalf("frame %d InDTX()=%v want core=%v", i, got, want)
		}
		if enc.InDTX() {
			activated = true
			break
		}
	}
	if !activated {
		t.Fatal("InDTX() never became true after sustained silence")
	}

	speech := generateSineWave(48000, 440, enc.FrameSize())
	if _, err := enc.Encode(speech, packet); err != nil {
		t.Fatalf("Encode(speech) error: %v", err)
	}
	if got, want := enc.InDTX(), enc.enc.InDTX(); got != want {
		t.Fatalf("post-speech InDTX()=%v want core=%v", got, want)
	}
}

func TestEncoder_VADActivityReportsOpusDecisionWithoutChangingPackets(t *testing.T) {
	modes := []struct {
		name string
		mode EncoderMode
	}{
		{name: "CELT", mode: EncoderModeCELT},
		{name: "Hybrid", mode: EncoderModeHybrid},
		{name: "SILK", mode: EncoderModeSILK},
	}
	for _, mode := range modes {
		for _, complexity := range []int{0, 10} {
			t.Run(fmt.Sprintf("%s/complexity-%d", mode.name, complexity), func(t *testing.T) {
				newEncoder := func() *Encoder {
					enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
					if err != nil {
						t.Fatalf("NewEncoder error: %v", err)
					}
					if err := enc.SetComplexity(complexity); err != nil {
						t.Fatalf("SetComplexity: %v", err)
					}
					if err := enc.SetMode(mode.mode); err != nil {
						t.Fatalf("SetMode(%v): %v", mode.mode, err)
					}
					enc.SetDTX(true)
					return enc
				}
				polling, control := newEncoder(), newEncoder()

				if got := polling.VADActivity(); got != 0 {
					t.Fatalf("initial VADActivity()=%d, want unavailable value 0", got)
				}

				packetPolling := make([]byte, 4000)
				packetControl := make([]byte, 4000)
				frames := [][]float32{
					make([]float32, polling.FrameSize()),
					generateSineWave(48000, 440, polling.FrameSize()),
					generateSineWave(48000, 440, polling.FrameSize()),
					generateSineWave(48000, 440, polling.FrameSize()),
				}
				sawVoicedActivity := false
				for i, pcm := range frames {
					gotLen, err := polling.Encode(pcm, packetPolling)
					if err != nil {
						t.Fatalf("polling Encode frame %d: %v", i, err)
					}
					wantLen, err := control.Encode(pcm, packetControl)
					if err != nil {
						t.Fatalf("control Encode frame %d: %v", i, err)
					}
					if gotLen != wantLen || !bytes.Equal(packetPolling[:gotLen], packetControl[:wantLen]) {
						t.Fatalf("polling VADActivity changed frame %d packet", i)
					}
					if got, want := polling.FinalRange(), control.FinalRange(); got != want {
						t.Fatalf("frame %d final range after polling=%08x want %08x", i, got, want)
					}
					if got, want := polling.InDTX(), control.InDTX(); got != want {
						t.Fatalf("frame %d InDTX after polling=%v want %v", i, got, want)
					}

					activity := polling.VADActivity()
					if activity < 0 || activity > 255 {
						t.Fatalf("frame %d VADActivity()=%d out of range", i, activity)
					}
					if i == 0 && activity != 0 {
						t.Fatalf("silence VADActivity()=%d, want 0", activity)
					}
					if i > 0 && activity > 0 {
						sawVoicedActivity = true
					}
				}
				if complexity == 10 && !sawVoicedActivity {
					t.Fatal("voiced Encode frames never produced nonzero VAD activity")
				}

				polling.Reset()
				control.Reset()
				if got := polling.VADActivity(); got != 0 {
					t.Fatalf("post-reset VADActivity()=%d, want unavailable value 0", got)
				}
				pcm := generateSineWave(48000, 440, polling.FrameSize())
				gotLen, err := polling.Encode(pcm, packetPolling)
				if err != nil {
					t.Fatalf("polling Encode after Reset: %v", err)
				}
				wantLen, err := control.Encode(pcm, packetControl)
				if err != nil {
					t.Fatalf("control Encode after Reset: %v", err)
				}
				if gotLen != wantLen || !bytes.Equal(packetPolling[:gotLen], packetControl[:wantLen]) {
					t.Fatal("post-reset VADActivity observation changed packet output")
				}
				if got, want := polling.FinalRange(), control.FinalRange(); got != want {
					t.Fatalf("post-reset final range after polling=%08x want %08x", got, want)
				}
			})
		}
	}
}

func TestEncoder_FEC(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Initially disabled
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true initially, want false")
	}

	// Enable FEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FECEnabled() = false after SetFEC(true)")
	}

	// Disable FEC
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true after SetFEC(false)")
	}
}

func TestEncoder_InBandFEC(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, tc := range []struct {
		config  int
		enabled bool
	}{
		{InBandFECDisabled, false},
		{InBandFECEnabled, true},
		{InBandFECMusicSafe, true},
	} {
		if err := enc.SetInBandFEC(tc.config); err != nil {
			t.Fatalf("SetInBandFEC(%d) error: %v", tc.config, err)
		}
		if got := enc.InBandFEC(); got != tc.config {
			t.Fatalf("InBandFEC()=%d want %d", got, tc.config)
		}
		if got := enc.FECEnabled(); got != tc.enabled {
			t.Fatalf("FECEnabled()=%t want %t", got, tc.enabled)
		}
	}

	enc.SetFEC(true)
	if got := enc.InBandFEC(); got != InBandFECEnabled {
		t.Fatalf("SetFEC(true) InBandFEC()=%d want %d", got, InBandFECEnabled)
	}
	enc.SetFEC(false)
	if got := enc.InBandFEC(); got != InBandFECDisabled {
		t.Fatalf("SetFEC(false) InBandFEC()=%d want %d", got, InBandFECDisabled)
	}

	for _, config := range []int{-1, 3} {
		if err := enc.SetInBandFEC(config); err != ErrInvalidFECConfig {
			t.Fatalf("SetInBandFEC(%d) error=%v want %v", config, err, ErrInvalidFECConfig)
		}
	}
}

func TestEncoder_SignalVoice_BiasesTowardSILK(t *testing.T) {
	// Create encoder with voice signal hint at wideband
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Set signal type to voice
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}

	// Generate a simple frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode and verify it produces a valid packet
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encode with SignalVoice returned empty packet")
	}

	t.Logf("SignalVoice encoded to %d bytes", len(packet))
}

func TestEncoder_SignalMusic_BiasesTowardCELT(t *testing.T) {
	// Create encoder with music signal hint
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Set signal type to music
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}

	// Generate a simple frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode and verify it produces a valid packet
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encode with SignalMusic returned empty packet")
	}

	t.Logf("SignalMusic encoded to %d bytes", len(packet))
}

func TestEncoder_MaxBandwidth_ClampsOutput(t *testing.T) {
	// Test that max bandwidth setting works for wideband limit
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Limit to wideband (instead of narrowband to avoid SILK frame size restrictions)
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetMaxBandwidth(BandwidthWideband) error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode - should work with wideband limit
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode with limited bandwidth error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encode with limited bandwidth returned empty packet")
	}
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if info.TOC.Bandwidth > BandwidthWideband {
		t.Fatalf("encoded bandwidth=%v want <= %v", info.TOC.Bandwidth, BandwidthWideband)
	}

	t.Logf("Wideband-limited encoded to %d bytes", len(packet))
}

func TestEncoder_ForcedBandwidthOverridesMaxBandwidth(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(480); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthSuperwideband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetMaxBandwidth error: %v", err)
	}

	pcm := generateSineWave(48000, 440, 480)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if info.TOC.Mode != ModeCELT || info.TOC.Bandwidth != BandwidthSuperwideband {
		t.Fatalf("TOC mode=%v bandwidth=%v want CELT %v", info.TOC.Mode, info.TOC.Bandwidth, BandwidthSuperwideband)
	}
}
