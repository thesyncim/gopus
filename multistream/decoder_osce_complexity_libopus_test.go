//go:build gopus_osce && !gopus_fixed_point

package multistream

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/types"
)

const msOSCEComplexityFrameSize = 960

type msOSCEComplexityStep struct {
	complexity int
	reset      bool
	packet     []byte
	ret        int
	finalRange uint32
	pcmBits    []uint32
}

type msOSCEComplexitySequence struct {
	arch     uint32
	dnnBuild uint32
	features uint32
	steps    []msOSCEComplexityStep
}

var (
	msOSCEComplexityHelper libopustest.HelperCache
	msOSCELACEModelHelper  libopustest.HelperCache
)

func msOSCEComplexityModelBlob(t *testing.T) []byte {
	t.Helper()
	path, err := msOSCELACEModelHelper.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper("", "libopus_osce_lace_model_blob.c", "gopus_ms_osce_lace_model_blob", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream OSCE LACE/NoLACE model blob", err)
	}
	blob, err := libopustest.RunHelper(path, nil)
	if err != nil {
		t.Fatalf("read selected OSCE model blob: %v", err)
	}
	return blob
}

func selectedMSOSCEComplexitySequence(t *testing.T, packets [][]byte) msOSCEComplexitySequence {
	t.Helper()
	libopustest.RequireOracle(t)
	bin, err := msOSCEComplexityHelper.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper("", "libopus_multistream_osce_complexity_sequence.c", "gopus_libopus_multistream_osce_complexity_sequence", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream OSCE complexity sequence", err)
	}
	steps := []msOSCEComplexityStep{
		{complexity: 0, packet: packets[0]},
		{complexity: 5, packet: packets[1]},
		{complexity: 6, packet: packets[2]},
		{complexity: 7, packet: packets[3]},
		{complexity: 7, reset: true, packet: packets[4]},
		{complexity: 5, packet: packets[5]},
		{complexity: 6, packet: packets[6]},
		{complexity: 0, packet: packets[7]},
		{complexity: 7, packet: packets[8]},
	}
	var request bytes.Buffer
	request.WriteString("GMSC")
	put := func(value uint32) { _ = binary.Write(&request, binary.LittleEndian, value) }
	put(1)
	put(48000)
	put(1) // channels
	put(1) // streams
	put(0) // coupled streams
	put(msOSCEComplexityFrameSize)
	put(uint32(len(steps)))
	request.WriteByte(0) // mono mapping
	for _, step := range steps {
		put(uint32(step.complexity))
		if step.reset {
			put(1)
		} else {
			put(0)
		}
		put(uint32(len(step.packet)))
		_, _ = request.Write(step.packet)
	}
	wire, err := libopustest.RunHelper(bin, request.Bytes())
	if err != nil {
		t.Fatalf("run selected C OpusMSDecoder sequence helper: %v", err)
	}
	r := bytes.NewReader(wire)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "GMSR" {
		t.Fatalf("C multistream OSCE protocol magic=%q err=%v", magic, err)
	}
	readU32 := func() uint32 {
		t.Helper()
		var value uint32
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	version := readU32()
	result := msOSCEComplexitySequence{arch: readU32(), dnnBuild: readU32(), features: readU32()}
	stepCount := readU32()
	if version != 2 || int(stepCount) != len(steps) {
		t.Fatalf("C protocol version/step count=%d/%d want 2/%d", version, stepCount, len(steps))
	}
	wantFeatures := uint32(2) // ENABLE_OSCE
	if extsupport.DRED {
		wantFeatures |= 1
	}
	if extsupport.QEXT {
		wantFeatures |= 4
	}
	if result.features != wantFeatures {
		t.Fatalf("selected C feature mask=%03b Go feature mask=%03b", result.features, wantFeatures)
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatalf("resolve selected C reference variant: %v", err)
	}
	if runtime.GOARCH == "arm64" {
		const armDNNFlags = uint32(1<<1 | 1<<2 | 1<<3 | 1<<4 | 1<<5)
		gotARM := result.dnnBuild & armDNNFlags
		switch variant {
		case libopustooling.LibopusReferenceScalar:
			if gotARM != 0 {
				t.Fatalf("scalar Go lane selected arm DNN flags %#x, want generic scalar", gotARM)
			}
		case libopustooling.LibopusReferenceSIMD:
			wantARM := uint32(1<<1 | 1<<2 | 1<<3 | 1<<4 | 1<<5)
			if gotARM != wantARM {
				t.Fatalf("SIMD Go lane selected arm DNN flags %#x, want static NEON+DOTPROD flags %#x", gotARM, wantARM)
			}
		default:
			t.Fatalf("unexpected selected reference variant %q", variant)
		}
		if result.dnnBuild&1 != 0 || result.arch != 0 {
			t.Fatalf("C ARM DNN dispatch is not static: build flags=%#x opus_select_arch=%d", result.dnnBuild, result.arch)
		}
	}
	t.Logf("selected C variant=%s arch=%d static DNN flags=%#x features=%03b helper=%s", variant, result.arch, result.dnnBuild, result.features, bin)
	result.steps = steps
	for i := range result.steps {
		step := &result.steps[i]
		step.ret = int(int32(readU32()))
		step.finalRange = readU32()
		if step.ret != msOSCEComplexityFrameSize {
			t.Fatalf("C step %d complexity=%d returned %d samples", i, step.complexity, step.ret)
		}
		step.pcmBits = make([]uint32, step.ret)
		for j := range step.pcmBits {
			step.pcmBits[j] = readU32()
		}
	}
	if r.Len() != 0 {
		t.Fatalf("C protocol trailing bytes=%d", r.Len())
	}
	return result
}

func msOSCEComplexitySILKPackets(t *testing.T) [][]byte {
	t.Helper()
	const packetCount = 9
	enc := internalenc.NewEncoder(48000, 1)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(40000)
	packets := make([][]byte, 0, packetCount)
	for frame := range packetCount {
		pcm := make([]float32, msOSCEComplexityFrameSize)
		for i := range pcm {
			tm := float64(frame*msOSCEComplexityFrameSize+i) / 48000.0
			pcm[i] = float32(0.27*math.Sin(2*math.Pi*191*tm+0.11) +
				0.13*math.Sin(2*math.Pi*367*tm+0.29))
		}
		packet, err := enc.Encode(pcm, msOSCEComplexityFrameSize)
		if err != nil {
			t.Fatalf("encode SILK-WB packet %d: %v", frame, err)
		}
		if len(packet) == 0 {
			t.Fatalf("encoded SILK-WB packet %d is empty", frame)
		}
		toc := parseStreamTOC(packet[0])
		if toc.mode != streamModeSILK || toc.bandwidth != 2 {
			t.Fatalf("packet %d TOC=%+v, want SILK WB", frame, toc)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

func TestMultistreamOSCEComplexityLifecycleMatchesSelectedLibopus(t *testing.T) {
	packets := msOSCEComplexitySILKPackets(t)
	want := selectedMSOSCEComplexitySequence(t, packets)
	modelBytes := msOSCEComplexityModelBlob(t)
	model, err := dnnblob.Clone(modelBytes)
	if err != nil {
		t.Fatalf("clone selected LACE/NoLACE model: %v", err)
	}
	if !model.SupportsOSCELACE() || !model.SupportsOSCENoLACE() {
		t.Fatal("selected OSCE model blob lacks LACE or NoLACE weights")
	}
	dec, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, msOSCEComplexityFrameSize)
	for i, step := range want.steps {
		if i == 2 {
			// The C decoder has its static OSCE models from initialization. Bind
			// the equivalent selected weights to Go only after the complexity-0/5
			// control frames, which do not select a postfilter.
			if dec.decoders[0].(*streamState).osceState != nil {
				t.Fatal("Go bound OSCE runtime before the late model update")
			}
			dec.SetDNNBlob(model)
			if dec.decoders[0].(*streamState).osceState == nil {
				t.Fatal("late SetDNNBlob did not bind the stream OSCE runtime")
			}
		}
		if step.reset {
			dec.Reset()
			st := dec.decoders[0].(*streamState)
			if st.osceState == nil || st.osceState.prevLACEActive || st.osceState.laceMethod != streamOSCELACEModeNone {
				t.Fatalf("step %d Reset did not clear active LACE method state", i)
			}
		}
		if err := dec.SetComplexity(step.complexity); err != nil {
			t.Fatalf("step %d SetComplexity(%d): %v", i, step.complexity, err)
		}
		got, err := dec.DecodeIntoFloat32(step.packet, pcm, msOSCEComplexityFrameSize)
		if err != nil {
			t.Fatalf("step %d complexity=%d DecodeIntoFloat32: %v", i, step.complexity, err)
		}
		if got != step.ret || dec.FinalRange() != step.finalRange {
			t.Fatalf("step %d complexity=%d returned/range=%d/%08x C=%d/%08x", i, step.complexity,
				got, dec.FinalRange(), step.ret, step.finalRange)
		}
		for j, sample := range pcm[:got] {
			if bits := math.Float32bits(sample); bits != step.pcmBits[j] {
				t.Fatalf("step %d complexity=%d PCM[%d]=%08x C=%08x", i, step.complexity, j, bits, step.pcmBits[j])
			}
		}
		st := dec.decoders[0].(*streamState)
		if st.osceLACEOverrideSet {
			t.Fatalf("step %d unexpectedly set an explicit LACE override", i)
		}
	}
}

func TestMultistreamOSCEExplicitLACEOverridePrecedence(t *testing.T) {
	dec, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	st := dec.decoders[0].(*streamState)
	if err := dec.SetComplexity(6); err != nil {
		t.Fatal(err)
	}
	if !st.osceLACEEnabledForComplexity() {
		t.Fatal("complexity 6 did not enable automatic LACE selection")
	}
	dec.SetOSCELACE(false)
	if !dec.osceLACEOverrideSet || !st.osceLACEOverrideSet || st.osceLACEEnabledForComplexity() {
		t.Fatal("explicit false override did not suppress automatic LACE selection")
	}
	if err := dec.SetComplexity(5); err != nil {
		t.Fatal(err)
	}
	dec.SetOSCELACE(true)
	if !st.osceLACEEnabledForComplexity() || pickStreamOSCELACEMode(dec.Complexity()) != streamOSCELACEModeNone {
		t.Fatal("explicit true below complexity 6 selected a method")
	}
	dec.Reset()
	if !st.osceLACEOverrideSet || !st.osceLACEEnabled {
		t.Fatal("Reset cleared the explicit LACE control setting")
	}
	if !st.osceLACEEnabledForComplexity() {
		t.Fatalf("Reset changed explicit LACE precedence at complexity %d", dec.Complexity())
	}
}
