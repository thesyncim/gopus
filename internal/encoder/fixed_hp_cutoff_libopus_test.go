//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
	"math"
	"testing"
)

func TestFixedHPCutoffResMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cutoffs := []int{60, 73, 89, 100}
	q8Boundaries := []int32{
		(120 << 8) + 127, (120 << 8) + 128, (120 << 8) + 129,
		-(120 << 8) - 127, -(120 << 8) - 128, -(120 << 8) - 129,
		(32767 << 8) + 127, (32767 << 8) + 128, (32767 << 8) + 129,
		-(32768 << 8) - 127, -(32768 << 8) - 128, -(32768 << 8) - 129,
	}
	type testCase struct {
		name   string
		fs     int
		ch     int
		mem    [4]int32
		input  [][]int32
		output [][]int32
		states [][4]int32
	}
	var cases []testCase
	for _, fs := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, seeded := range []bool{false, true} {
				tc := testCase{
					name: fmt.Sprintf("fs%d/ch%d/seeded%t", fs, channels, seeded),
					fs:   fs, ch: channels,
					input: make([][]int32, len(cutoffs)),
					mem:   [4]int32{1 << 18, -(1 << 17), 1 << 16, -(1 << 15)},
				}
				if !seeded {
					tc.mem = [4]int32{}
				}
				frameSize := fs / 50
				for frame := range tc.input {
					tc.input[frame] = make([]int32, frameSize*channels)
					for i := range tc.input[frame] {
						if i < len(q8Boundaries) {
							tc.input[frame][i] = q8Boundaries[i]
							continue
						}
						x := float32(.72*math.Sin(float64(frame*len(tc.input[frame])+i)*.017) +
							.43*math.Sin(float64(i)*.231+float64(frame)*.41))
						if (i+frame)%211 == 0 {
							x = 1.35
						} else if (i+frame)%223 == 0 {
							x = -1.35
						}
						tc.input[frame][i] = fixedFloatToRes(x)
					}
				}
				goState := tc.mem
				for frame := range tc.input {
					out := make([]int32, len(tc.input[frame]))
					silk.HPCutoffRes24(tc.input[frame], out, &goState, int32(fs), int32(channels), int32(cutoffs[frame]))
					tc.output = append(tc.output, out)
					tc.states = append(tc.states, goState)
				}
				cases = append(cases, tc)
			}
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, states, err := libopustest.ProbeFixedHPCutoff(tc.input, tc.mem, tc.fs, tc.ch, cutoffs)
			if err != nil {
				t.Fatal(err)
			}
			for frame := range tc.input {
				for i, got := range tc.output[frame] {
					if got != want[frame][i] {
						t.Fatalf("frame%d sample%d Q8=%d C=%d", frame, i, got, want[frame][i])
					}
				}
				if tc.states[frame] != states[frame] {
					t.Fatalf("frame%d state=%v C=%v", frame, tc.states[frame], states[frame])
				}
			}
		})
	}

	input := cases[0].input[0]
	output := make([]int32, len(input))
	state := cases[0].mem
	for range 3 {
		silk.HPCutoffRes24(input, output, &state, int32(cases[0].fs), int32(cases[0].ch), int32(cutoffs[0]))
	}
	allocs := testing.AllocsPerRun(50, func() {
		silk.HPCutoffRes24(input, output, &state, int32(cases[0].fs), int32(cases[0].ch), int32(cutoffs[0]))
	})
	if allocs != 0 {
		t.Fatalf("fixed hp cutoff warm allocations=%g, want 0", allocs)
	}
}

func TestFixedVoIPHPCutoffResetAndLowSpaceState(t *testing.T) {
	const frameSize = 480
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(.55*float64((i%37)-18)/18 + .22*float64((i%11)-5)/5)
	}
	newEncoder := func() *Encoder {
		e := NewEncoder(48000, 1)
		e.SetVoIPApplication(true)
		e.mode = ModeCELT
		e.bandwidth = types.BandwidthFullband
		e.bitrateMode = ModeCBR
		e.useVBR = false
		e.vbrConstraint = false
		e.bitrate = 64000
		e.complexity = 0
		e.forceChannels = 1
		return e
	}
	encode := func(e *Encoder, maxBytes int) ([]byte, error) {
		return e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, maxBytes)
	}

	e := newEncoder()
	packet, err := encode(e, 1)
	if err != nil || len(packet) != 1 {
		t.Fatalf("initial low-space packet len=%d err=%v", len(packet), err)
	}
	if e.fixedHPMem != ([4]int32{}) || e.hpMem != ([4]float32{}) || e.variableHPSmth2Inited {
		t.Fatalf("low-space packet changed HP state: fixed=%v float=%v smoother=%d initialized=%t",
			e.fixedHPMem, e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}
	e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15() + 8192
	e.variableHPSmth2Inited = true
	packet, err = encode(e, 400)
	if err != nil || len(packet) < 2 {
		t.Fatalf("real VoIP frame len=%d err=%v", len(packet), err)
	}
	memAfterReal := e.fixedHPMem
	if memAfterReal == ([4]int32{}) || !e.variableHPSmth2Inited {
		t.Fatalf("real frame did not advance fixed HP state: mem=%v smoother=%d initialized=%t",
			memAfterReal, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}
	smootherAfterReal := e.variableHPSmth2Q15
	packet, err = encode(e, 1)
	if err != nil || len(packet) != 1 {
		t.Fatalf("second low-space packet len=%d err=%v", len(packet), err)
	}
	if e.fixedHPMem != memAfterReal || e.variableHPSmth2Q15 != smootherAfterReal {
		t.Fatalf("second low-space packet advanced state: fixed=%v/%v smoother=%d/%d",
			e.fixedHPMem, memAfterReal, e.variableHPSmth2Q15, smootherAfterReal)
	}

	e.Reset()
	if e.fixedHPMem != ([4]int32{}) || e.hpMem != ([4]float32{}) ||
		e.variableHPSmth2Q15 != 0 || e.variableHPSmth2Inited {
		t.Fatalf("Reset did not clear VoIP HP state: fixed=%v float=%v smoother=%d initialized=%t",
			e.fixedHPMem, e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}

	resetPacket, err := encode(e, 400)
	if err != nil {
		t.Fatalf("encode after Reset: %v", err)
	}
	fresh := newEncoder()
	freshPacket, err := encode(fresh, 400)
	if err != nil {
		t.Fatalf("encode fresh state: %v", err)
	}
	if !bytes.Equal(resetPacket, freshPacket) || e.FinalRange() != fresh.FinalRange() || e.fixedHPMem != fresh.fixedHPMem {
		t.Fatalf("Reset replay differs from fresh VoIP state: packet=%x/%x range=%08x/%08x mem=%v/%v",
			resetPacket, freshPacket, e.FinalRange(), fresh.FinalRange(), e.fixedHPMem, fresh.fixedHPMem)
	}
}

func TestFixedOuterCBRInputPreprocessMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 480
		frames    = 3
	)
	pcm, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantAMMultisineV1, 48000, frameSize*frames, 1)
	if err != nil {
		t.Fatal(err)
	}

	quantized := make([]float32, len(pcm))
	raw := make([]int32, len(pcm))
	for i, sample := range pcm {
		// Match the public CBR oracle's opus_demo -f32 quantization before
		// both paths enter FLOAT2RES.
		q := float32(math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0)
		quantized[i] = q
		raw[i] = fixedFloatToRes(q)
	}
	want, wantMem, err := libopustest.ProbeFixedDCReject(raw, [4]int32{}, 48000, 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	e := NewEncoder(48000, 1)
	got := make([]int32, 0, len(raw))
	for frame := range frames {
		start := frame * frameSize
		e.prepareFixedInputRes(quantized[start : start+frameSize])
		e.preprocessFixedInputRes(frameSize)
		got = append(got, e.fixedFiltered...)
	}
	if len(got) != len(want) {
		t.Fatalf("filtered samples=%d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("filtered input sample %d (frame %d): Go=%d C=%d raw=%d", i, i/frameSize, got[i], want[i], raw[i])
		}
	}
	if e.fixedHPMem != wantMem {
		t.Fatalf("filter state after onset: Go=%v C=%v", e.fixedHPMem, wantMem)
	}
}

func fixedOuterRawPCM(frameSize, channels, frames int) []int16 {
	pcm := make([]int16, frameSize*channels*frames)
	state := uint32(6716)
	for i := range pcm {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		pcm[i] = int16(int32(state) >> 19)
	}
	return pcm
}

func TestFixedOuterOpusEncodeRecordsPreserveCalls(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	p := libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize, FrameCount: frames,
		PCM: fixedOuterRawPCM(frameSize, 1, frames),
	}
	records, err := libopustest.ProbeOpusEncodeFixedRecords(p)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := libopustest.ProbeOpusEncodeFixed(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != frames || len(legacy) != frames {
		t.Fatalf("C records=%d legacy=%d want=%d", len(records), len(legacy), frames)
	}
	for i, record := range records {
		if record.Status != 0 || !bytes.Equal(record.Packet, legacy[i]) {
			t.Fatalf("C record %d status=%d len=%d legacy=%d", i, record.Status, len(record.Packet), len(legacy[i]))
		}
	}
}

func TestFixedDCRejectQ8MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			input := make([]int32, rate/50*channels)
			for i := range input {
				input[i] = int32((i*317)%65535-32768)<<8 + int32(i%251)
			}
			memory := [4]int32{637281, -111, -625541, 222}
			want, wantMem, err := libopustest.ProbeFixedDCReject(input, memory, rate, channels, 3)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int32, len(input))
			fixedDCRejectRes(input, got, &memory, rate, channels, 3)
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("rate=%d channels=%d sample=%d got=%d want=%d", rate, channels, i, got[i], want[i])
				}
			}
			if memory != wantMem {
				t.Fatalf("rate=%d channels=%d memory=%v want=%v", rate, channels, memory, wantMem)
			}
		}
	}
}

func TestFixedOuterOpusEncodeRawInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	p := libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize, FrameCount: frames,
		PCM: fixedOuterRawPCM(frameSize, 1, frames),
	}
	inputFrames := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
	for frame := range inputFrames {
		inputFrames[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format:   0,
			ShortPCM: p.PCM[frame*frameSize : (frame+1)*frameSize],
		}
	}
	want, err := probePublicFixedMixedRecords(p, inputFrames)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEncoder(48000, 1)
	e.SetMode(ModeCELT)
	e.SetBandwidth(types.BandwidthFullband)
	e.SetComplexity(0)
	e.SetBitrate(64000)
	e.SetBitrateMode(ModeCBR)
	e.SetForceChannels(1)
	pcm := make([]float32, frameSize)
	for frame := range frames {
		for i := range pcm {
			pcm[i] = float32(p.PCM[frame*frameSize+i]) * (1.0 / 32768.0)
		}
		got, err := e.EncodeShortMixedWithAnalysisMaxBytes(pcm, frameSize, pcm, 4000)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
		}
		if want[frame].Status != 0 {
			t.Fatalf("C frame %d status=%d", frame, want[frame].Status)
		}
		if gotRange := e.FinalRange(); gotRange != want[frame].FinalRange {
			t.Fatalf("frame %d range=%08x want=%08x", frame, gotRange, want[frame].FinalRange)
		}
		if !bytes.Equal(got, want[frame].Packet) {
			first := 0
			for first < min(len(got), len(want[frame].Packet)) && got[first] == want[frame].Packet[first] {
				first++
			}
			t.Fatalf("frame %d packet differs at byte %d, len=%d want=%d", frame, first, len(got), len(want[frame].Packet))
		}
	}
}

func probePublicFixedMixedRecords(p libopustest.OpusEncodeFixedParams, frames []libopustest.OpusEncodeFixedMixedFrame) ([]libopustest.OpusEncodeFixedRecord, error) {
	if extsupport.QEXT {
		return libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(p, frames)
	}
	return libopustest.ProbeOpusEncodeFixedMixedRecords(p, frames)
}

// probePublicFixedCELTQ8 uses the same selected C feature set as the public
// fixed CELT encoder, even when its QEXT packet extension is runtime disabled.
func probePublicFixedCELTQ8(p libopustest.CELTFixedQ8Params) ([]libopustest.CELTFixedQ8Record, error) {
	if extsupport.QEXT {
		return libopustest.ProbeCELTFixedQEXTQ8(p)
	}
	return libopustest.ProbeCELTFixedRawQ8(p)
}

func TestFixedStereoPrefilterThresholdMatchesLibopus(t *testing.T) {
	assertFixedCapturedSequenceMatchesLibopus(t, "fs48000_ch2_dur200_wb_br6000_cx5_0")
}

// TestFixedCBRRawTailMatchesLibopus locks the fixed-storage CELT packet
// layout: raw end bytes remain at the end, with a zero gap after range bytes.
func TestFixedCBRRawTailMatchesLibopus(t *testing.T) {
	assertFixedCapturedSequenceMatchesLibopus(t, "fs48000_ch1_dur50_nb_br128000_cx10_2")
}

func assertFixedCapturedSequenceMatchesLibopus(t *testing.T, target string) {
	t.Helper()
	libopustest.RequireOracle(t)
	var spec encFixSpec
	found := false
	for _, candidate := range buildEncFixSweep() {
		if candidate.name == target {
			spec, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatalf("missing fixed CELT configuration %s", target)
	}
	const count = 6
	enc := configureFixCELT(spec)
	gotPackets := make([][]byte, count)
	gotRanges := make([]uint32, count)
	innerFrames := make([]libopustest.CELTFixedQ8Frame, count)
	topFrames := make([]libopustest.OpusEncodeFixedMixedFrame, count)
	innerBitrate, lsbDepth := 0, 0
	for f := range gotPackets {
		pcm := genFixFrame(spec, f, count)
		packet, err := enc.Encode(pcm, spec.frameSize)
		if err != nil {
			t.Fatalf("frame %d encode: %v", f, err)
		}
		if len(packet) < 1 || !enc.fixedCELTUsed {
			t.Fatalf("frame %d did not use integer CELT", f)
		}
		gotPackets[f] = append([]byte(nil), packet...)
		gotRanges[f] = enc.FinalRange()
		innerFrames[f] = fixedQ8OracleFrame(enc)
		if len(innerFrames[f].PCM) != spec.frameSize*spec.channels {
			t.Fatalf("frame %d Q8 input length=%d", f, len(innerFrames[f].PCM))
		}
		topFrames[f] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: append([]float32(nil), pcm...),
		}
		rate, _, depth := enc.LastFixedCELTControls()
		if f == 0 {
			innerBitrate, lsbDepth = rate, depth
		} else if rate != innerBitrate || depth != lsbDepth {
			t.Fatalf("frame %d inner controls changed", f)
		}
	}
	vbr := spec.mode != ModeCBR
	cvbr := spec.mode == ModeCVBR
	top, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: spec.rate, Channels: spec.channels,
		Application:    libopustest.OpusApplicationRestrictedLowDelay,
		MaxPacketBytes: 1276, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: spec.oracleBW,
		Bitrate:   spec.bitrate, Complexity: spec.complexity, VBR: vbr,
		VBRConstraint: cvbr,
		ForceChannels: spec.channels, FrameSize: spec.frameSize, FrameCount: count,
	}, topFrames)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed outer float encode", err)
		return
	}
	inner, err := probePublicFixedCELTQ8(libopustest.CELTFixedQ8Params{
		SampleRate: spec.rate, Channels: spec.channels, FrameSize: spec.frameSize,
		Start: 0, End: celtFixedEndBand(spec.bandwidth),
		Bitrate: innerBitrate, Complexity: spec.complexity, LSBDepth: lsbDepth,
		VBR: vbr, ConstrainedVBR: cvbr, Frames: innerFrames,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed CELT raw Q8", err)
		return
	}
	if len(top) != count || len(inner) != count {
		t.Fatalf("oracle records outer=%d inner=%d want %d", len(top), len(inner), count)
	}
	for f := range gotPackets {
		if top[f].Status < 0 || !bytes.Equal(gotPackets[f], top[f].Packet) ||
			gotRanges[f] != top[f].FinalRange {
			t.Fatalf("frame %d public packet/range differs: status=%d len=%d/%d range=%08x/%08x",
				f, top[f].Status, len(gotPackets[f]), len(top[f].Packet),
				gotRanges[f], top[f].FinalRange)
		}
		if !bytes.Equal(gotPackets[f][1:], inner[f].Packet) ||
			gotRanges[f] != inner[f].FinalRange {
			t.Fatalf("frame %d inner packet/range differs: len=%d/%d range=%08x/%08x",
				f, len(gotPackets[f])-1, len(inner[f].Packet),
				gotRanges[f], inner[f].FinalRange)
		}
	}
}
