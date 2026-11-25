//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"reflect"
	"runtime"
	"testing"
)

func probeDecodeWithFEC96kOracle(p libopustest.QEXTDecode96kParams) (libopustest.QEXTDecode96kResult, error) {
	return libopustest.ProbeQEXTDecodeFECSequence(p)
}

func TestPublicQEXTHybridNative96TransitionsMatchSelectedFloatReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			celtPacket := makeFloatQEXTCELTPacketForNative96Transition(t, opusDemo, channels)
			hybridPacket := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			for _, sequence := range []struct {
				name    string
				packets [][]byte
			}{
				{name: "received-hybrid", packets: [][]byte{hybridPacket, hybridPacket, hybridPacket}},
				{name: "celt-loss", packets: [][]byte{celtPacket, celtPacket, nil}},
				{name: "celt-hybrid-transition", packets: [][]byte{celtPacket, celtPacket, hybridPacket, hybridPacket, hybridPacket, hybridPacket, celtPacket, celtPacket, celtPacket}},
			} {
				t.Run(sequence.name, func(t *testing.T) {
					for frame, packet := range sequence.packets {
						if len(packet) == 0 {
							continue
						}
						wantMode := ModeHybrid
						switch sequence.name {
						case "celt-loss":
							wantMode = ModeCELT
						case "celt-hybrid-transition":
							if frame < 2 || frame > 5 {
								wantMode = ModeCELT
							}
						}
						if got := ParseTOC(packet[0]).Mode; got != wantMode {
							t.Fatalf("frame %d mode=%v, want %v", frame, got, wantMode)
						}
					}
					assertFloatQEXTHybridNative96Sequence(t, channels, sequence.packets)
				})
			}
		})
	}
}

func assertFloatQEXTHybridNative96Sequence(t *testing.T, channels int, packets [][]byte) {
	t.Helper()
	const frameSize = 1920
	want, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		MaxFrameSize: frameSize,
		Packets:      packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected native-96 float-QEXT decoder", err)
		return
	}
	if len(packets) > 6 && ParseTOC(packets[6][0]).Mode == ModeCELT && ParseTOC(packets[5][0]).Mode == ModeHybrid {
		probe, probeErr := NewDecoder(DefaultDecoderConfig(96000, channels))
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		probeFrame := make([]float32, frameSize*channels)
		for frame := 0; frame < 6; frame++ {
			if _, probeErr := probe.Decode(packets[frame], probeFrame); probeErr != nil {
				t.Fatalf("probe frame %d: %v", frame, probeErr)
			}
		}
		const transitionFrameSize = 480 // the CELT-mode transition conceals 5 ms at 96 kHz
		plc := make([]float32, transitionFrameSize*channels)
		if probeErr := probe.hybridDecoder.DecodePLCToFloat32WithPacketStereoInto(transitionFrameSize, channels == 2, plc); probeErr != nil {
			t.Fatalf("direct Hybrid PLC probe: %v", probeErr)
		}
		for i := 0; i < 120*channels; i++ {
			if got, expected := math.Float32bits(plc[i]), math.Float32bits(want.PCM[6*frameSize*channels+i]); got != expected {
				t.Fatalf("direct Hybrid PLC transition prefix[%d]=%08x C=%08x", i, got, expected)
			}
		}
	}
	dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]float32, frameSize*channels)
	run := func() {
		for frame, packet := range packets {
			n, err := dec.Decode(packet, out)
			if err != nil || n != frameSize {
				t.Fatalf("frame %d decode=(%d,%v), want (%d,nil)", frame, n, err, frameSize)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[frame]; got != expected {
				t.Fatalf("frame %d FinalRange=%08x C=%08x", frame, got, expected)
			}
			start := frame * len(out)
			for i, sample := range out {
				if got, expected := math.Float32bits(sample), math.Float32bits(want.PCM[start+i]); got != expected {
					t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
				}
			}
		}
	}
	run()
	dec.Reset()
	run()
	var decodeErr error
	allocs := testing.AllocsPerRun(20, func() {
		_, decodeErr = dec.Decode(packets[len(packets)-1], out)
	})
	if decodeErr != nil {
		t.Fatalf("warmed native-96 Hybrid decode: %v", decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed native-96 Hybrid decode allocated %g times/call", allocs)
	}
}

func makeFloatQEXTCELTPacketForNative96Transition(t *testing.T, opusDemo string, channels int) []byte {
	t.Helper()
	pcm := make([]float32, 960*channels)
	for i := 0; i < 960; i++ {
		tm := float64(i) / 48000
		left := float32(0.31*math.Sin(2*math.Pi*6200*tm) + 0.19*math.Sin(2*math.Pi*21800*tm))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = float32(float64(left) * 0.87)
		}
	}
	return encodeLibopusPacketAtBitrate(t, opusDemo, channels, pcm, true, true, 256000)
}

var qextNative96PLCPitchTraceHelper libopustest.HelperCache

func buildQEXTNative96PLCPitchTraceHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "native 96 kHz QEXT PLC pitch trace",
		OutputBase:  "gopus_libopus_celt_plc_qext_period_trace",
		SourceFile:  "libopus_celt_plc_qext_period_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTNative96PLCPitchPeriodMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			packet := makeFloatQEXTCELTPacketForNative96Transition(t, opusDemo, channels)
			want := probeQEXTNative96PLCTrace(t, channels, packet)
			dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]float32, 1920*channels)
			for i := 0; i < 2; i++ {
				if _, err := dec.Decode(packet, out); err != nil {
					t.Fatalf("received frame %d: %v", i, err)
				}
			}
			core := reflect.ValueOf(dec.celtDecoder).Elem()
			// decode_mem reserves 2*(history+overlap) samples per channel and
			// decode_mem[c] is the window at decodeMemOff (internal/celt/decode_mem.go).
			goMem := core.FieldByName("decodeMem")
			if !goMem.IsValid() {
				t.Fatal("CELT decoder has no decode_mem")
			}
			overlap := int(core.FieldByName("synthOverlap").Int())
			if overlap <= 0 {
				overlap = celt.Overlap
			}
			memLen := goMem.Len() / (2 * channels)
			memOff := int(core.FieldByName("decodeMemOff").Int())
			historyLen := memLen - overlap
			oldHist := make([]float32, historyLen*channels)
			for channel := 0; channel < channels; channel++ {
				base := channel*2*memLen + memOff
				for i := 0; i < historyLen; i++ {
					oldHist[channel*historyLen+i] = float32(goMem.Index(base + i).Float())
				}
			}
			if _, err := dec.Decode(nil, out); err != nil {
				t.Fatalf("PLC frame: %v", err)
			}
			period := reflect.ValueOf(dec.celtDecoder).Elem().FieldByName("plcLastPitchPeriod")
			if !period.IsValid() {
				t.Fatal("CELT decoder has no plcLastPitchPeriod state")
			}
			if got := int(period.Int()); got != want.period {
				t.Fatalf("native-96 PLC pitch period=%d, selected C=%d", got, want.period)
			}
			plcExc := core.FieldByName("scratchPLCExc")
			maxPeriod := plcExc.Len() - 24
			excLength := min(2*want.period, maxPeriod)
			decayLength := excLength >> 1
			base1 := 24 + maxPeriod - decayLength
			base2 := 24 + maxPeriod - 2*decayLength
			channel := channels - 1 // scratchPLCExc is reused for the final channel.
			e1, e2 := qextPLCTraceEnergy(plcExc, base1, decayLength), qextPLCTraceEnergy(plcExc, base2, decayLength)
			if e1 > e2 {
				e1 = e2
			}
			decayRatio := e1 / e2
			decay := float32(math.Sqrt(float64(decayRatio)))
			if got, want := math.Float32bits(decayRatio), math.Float32bits(want.sqrtArg[channel]); got != want {
				t.Fatalf("PLC decay ratio[%d]=%08x C=%08x", channel, got, want)
			}
			if got, want := math.Float32bits(decay), math.Float32bits(want.sqrt[channel]); got != want {
				t.Fatalf("PLC decay[%d]=%08x C=%08x", channel, got, want)
			}
			lpc := reflect.ValueOf(dec.celtDecoder).Elem().FieldByName("plcLPC")
			if !lpc.IsValid() {
				t.Fatal("CELT decoder has no PLC LPC state")
			}
			for i := range 24 {
				gotBits := math.Float32bits(float32(lpc.Index((channels-1)*24 + i).Float()))
				wantBits := math.Float32bits(want.lpc[channels-1][i])
				if gotBits != wantBits {
					t.Fatalf("PLC LPC[%d]=%08x C=%08x", i, gotBits, wantBits)
				}
			}
			fir := reflect.ValueOf(dec.celtDecoder).Elem().FieldByName("scratchPLCFIRTmp")
			for i := range want.fir[channels-1] {
				gotBits := math.Float32bits(float32(fir.Index(i).Float()))
				wantBits := math.Float32bits(want.fir[channels-1][i])
				if gotBits != wantBits {
					t.Fatalf("PLC FIR[%d]=%08x C=%08x", i, gotBits, wantBits)
				}
			}
			for channel := 0; channel < channels; channel++ {
				for i := range 24 {
					gotBits := math.Float32bits(oldHist[channel*historyLen+historyLen-1-i])
					wantBits := math.Float32bits(want.iirMem[channel][i])
					if gotBits != wantBits {
						t.Fatalf("PLC IIR history[%d][%d]=%08x C=%08x", channel, i, gotBits, wantBits)
					}
				}
			}
			exc := core.FieldByName("scratchPLCExc")
			excMaxPeriod := exc.Len() - 24
			offset := excMaxPeriod - want.period
			attenuation := decay
			j := 0
			for i := range want.iirInput[channel] {
				if j >= want.period {
					j -= want.period
					attenuation = float32(attenuation * want.sqrt[channel])
				}
				v := float32(attenuation * float32(exc.Index(24+offset+j).Float()))
				gotBits, wantBits := math.Float32bits(v), math.Float32bits(want.iirInput[channel][i])
				if gotBits != wantBits {
					t.Fatalf("PLC IIR input[%d][%d]=%08x C=%08x", channel, i, gotBits, wantBits)
				}
				j++
			}
			goIIRY := core.FieldByName("scratchPLCIIRY")
			unrolledLength := len(want.iir[channel]) &^ 3
			for i := range want.iir[channel] {
				got := float32(goIIRY.Index(i + 24).Float())
				if i < unrolledLength {
					got = -got
				}
				if gotBits, wantBits := math.Float32bits(got), math.Float32bits(want.iir[channel][i]); gotBits != wantBits {
					t.Fatalf("IIR kernel output[%d][%d]=%08x C=%08x", channel, i, gotBits, wantBits)
				}
			}
			state := reflect.ValueOf(dec.celtDecoder).Elem().FieldByName("preemphState")
			for channel := 0; channel < channels; channel++ {
				got := float32(state.Index(channel).Float())
				if gotBits, wantBits := math.Float32bits(got), math.Float32bits(want.preemph[channel]); gotBits != wantBits {
					t.Fatalf("PLC de-emphasis state[%d]=%08x C=%08x", channel, gotBits, wantBits)
				}
			}
		})
	}
}

func qextPLCTraceEnergy(values reflect.Value, start, length int) float32 {
	sum := float32(1)
	for i := range length {
		x := float32(values.Index(start + i).Float())
		if runtime.GOARCH == "arm64" {
			// The helper compiles celt_decode_lost's scalar E1/E2 loop with
			// the host C compiler's default target flags. On arm64, that target
			// contracts the source multiply-add; on amd64's baseline target, it
			// rounds the product before adding it.
			sum = float32(math.FMA(float64(x), float64(x), float64(sum)))
		} else {
			product := float32(x * x)
			sum = float32(sum + product)
		}
	}
	return sum
}

type qextNative96PLCTrace struct {
	period   int
	raw      []float32
	preemph  []float32
	lpc      [][]float32
	fir      [][]float32
	sqrtArg  []float32
	sqrt     []float32
	iir      [][]float32
	iirInput [][]float32
	iirMem   [][]float32
}

func probeQEXTNative96PLCTrace(t *testing.T, channels int, packet []byte) qextNative96PLCTrace {
	t.Helper()
	binPath, err := qextNative96PLCPitchTraceHelper.Path(buildQEXTNative96PLCPitchTraceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT PLC pitch trace", err)
	}
	payload := libopustest.NewOraclePayload("GCLI", uint32(channels), uint32(len(packet)))
	payload.Raw(packet)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "native 96 kHz QEXT PLC pitch trace", "GCLO")
	if err != nil {
		t.Fatal(err)
	}
	period := int(reader.U32())
	if frameSize, gotChannels := reader.U32(), reader.U32(); frameSize != 1920 || gotChannels != uint32(channels) {
		t.Fatalf("native 96 kHz PLC trace dimensions=%d/%d, want 1920/%d", frameSize, gotChannels, channels)
	}
	trace := qextNative96PLCTrace{period: period, raw: make([]float32, 1920*channels), preemph: make([]float32, channels)}
	for i := range trace.raw {
		trace.raw[i] = reader.Float32()
	}
	for i := range trace.preemph {
		trace.preemph[i] = reader.Float32()
	}
	lpcCalls, firCalls := int(reader.U32()), int(reader.U32())
	if lpcCalls != channels || firCalls != channels {
		t.Fatalf("PLC trace calls: LPC=%d FIR=%d, want %d", lpcCalls, firCalls, channels)
	}
	trace.lpc = make([][]float32, lpcCalls)
	for i := range trace.lpc {
		trace.lpc[i] = make([]float32, 24)
		for j := range trace.lpc[i] {
			trace.lpc[i][j] = reader.Float32()
		}
	}
	trace.fir = make([][]float32, firCalls)
	for i := range trace.fir {
		n := int(reader.U32())
		if n <= 0 || n > 2048 {
			t.Fatalf("invalid PLC FIR trace length %d", n)
		}
		trace.fir[i] = make([]float32, n)
		for j := range trace.fir[i] {
			trace.fir[i][j] = reader.Float32()
		}
	}
	sqrtCalls := int(reader.U32())
	if sqrtCalls < 0 || sqrtCalls > 8 {
		t.Fatalf("invalid PLC sqrt trace count %d", sqrtCalls)
	}
	trace.sqrtArg = make([]float32, sqrtCalls)
	trace.sqrt = make([]float32, sqrtCalls)
	for i := range trace.sqrtArg {
		trace.sqrtArg[i] = reader.Float32()
		trace.sqrt[i] = reader.Float32()
	}
	iirCalls := int(reader.U32())
	if iirCalls != channels {
		t.Fatalf("PLC IIR trace calls=%d want %d", iirCalls, channels)
	}
	trace.iir = make([][]float32, iirCalls)
	trace.iirInput = make([][]float32, iirCalls)
	trace.iirMem = make([][]float32, iirCalls)
	for i := range trace.iir {
		n := int(reader.U32())
		if n != 2160 {
			t.Fatalf("native96 PLC IIR length=%d want 2160", n)
		}
		trace.iir[i] = make([]float32, n)
		trace.iirInput[i] = make([]float32, n)
		trace.iirMem[i] = make([]float32, 24)
		for j := range trace.iirInput[i] {
			trace.iirInput[i][j] = reader.Float32()
		}
		for j := range trace.iirMem[i] {
			trace.iirMem[i][j] = reader.Float32()
		}
		for j := range trace.iir[i] {
			trace.iir[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}

func TestQEXTHybridTransitionPLCUsesPreviousCELTBandwidth(t *testing.T) {
	libopustest.RequireOracle(t)

	// The sequence retains a CELT Fullband state through a long PLC and FEC
	// fallback before a 10 ms Hybrid SWB packet triggers recursive CELT PLC.
	// libopus applies the new end band after that recursive PLC frame.
	packet := func(encoded string) []byte {
		decoded, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	celtPacket := packet("e49efa537fb4e25b1cc490cabe23eaa3b73fd972543b0ce936b94abb777cf2f22fc700e6ad1b4c7d3e40d0ff0da354f36697")
	hybridPacket := packet("6083c9198b547c7503685baa52e07a356f46b0047917aee341febfb4e7122e25a263b34067906156af03471b0742430c39ddd6cf0e3a8292ea96a6e0")
	packets := [][]byte{celtPacket, nil, hybridPacket, hybridPacket}
	decodeFEC := []bool{false, true, true, false}
	wantStatus := []int32{240, 5760, 5760, 960}
	wantRanges := []uint32{0x1306a200, 0, 0, 0x0ad35400}

	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			ref, err := libopustest.ProbeQEXTDecodeFECSequence(libopustest.QEXTDecode96kParams{
				SampleFormat:           libopustest.QEXTDecode96kFormatFloat32,
				Channels:               channels,
				SampleRate:             96000,
				PhaseInversionDisabled: false,
				MaxFrameSize:           5760,
				Packets:                packets,
				DecodeFEC:              decodeFEC,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC sequence", err)
			}
			if len(ref.Status) != len(wantStatus) || len(ref.FinalRanges) != len(wantRanges) {
				t.Fatalf("libopus returned %d statuses and %d ranges", len(ref.Status), len(ref.FinalRanges))
			}
			for i := range wantStatus {
				if ref.Status[i] != wantStatus[i] || ref.FinalRanges[i] != wantRanges[i] {
					t.Fatalf("libopus step %d=(status %d, range %08x), want (%d, %08x)",
						i, ref.Status[i], ref.FinalRanges[i], wantStatus[i], wantRanges[i])
				}
			}

			cfg := DefaultDecoderConfig(96000, channels)
			cfg.MaxPacketBytes = 8192
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatal(err)
			}
			dec.SetPhaseInversionDisabled(false)
			out := make([]float32, 5760*channels)
			offset := 0
			for i, packet := range packets {
				var n int
				if decodeFEC[i] {
					n, err = dec.DecodeWithFEC(packet, out, true)
				} else {
					n, err = dec.Decode(packet, out)
				}
				if err != nil || int32(n) != wantStatus[i] || int32(n) != ref.Status[i] {
					t.Fatalf("step %d Go=(%d,%v), C=%d", i, n, err, ref.Status[i])
				}
				if got := dec.FinalRange(); got != wantRanges[i] || got != ref.FinalRanges[i] {
					t.Fatalf("step %d final range=%08x, C=%08x", i, got, ref.FinalRanges[i])
				}
				count := n * channels
				if offset+count > len(ref.PCM) {
					t.Fatalf("step %d consumes past C PCM: offset=%d count=%d length=%d", i, offset, count, len(ref.PCM))
				}
				for j := 0; j < count; j++ {
					if got, want := math.Float32bits(out[j]), math.Float32bits(ref.PCM[offset+j]); got != want {
						t.Fatalf("step %d float32[%d]=%08x, C=%08x", i, j, got, want)
					}
				}
				offset += count
			}
			if offset != len(ref.PCM) {
				t.Fatalf("consumed %d C PCM samples, returned %d", offset, len(ref.PCM))
			}
		})
	}
}

type hd96kDynallocToneCase struct {
	channels  int
	frameSize int
	mode      BitrateMode
	pcm       []float32
}

func hd96kDynallocTonePCM(channels, frameSize, frameCount int) []float32 {
	pcm := make([]float32, channels*frameSize*frameCount)
	for i := range frameSize * frameCount {
		sample := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/96000))
		for ch := range channels {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func hd96kDynallocModeName(mode BitrateMode) string {
	if mode == BitrateModeCVBR {
		return "cvbr"
	}
	return "vbr"
}

func TestHD96kDynallocToneScaleMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const bitrate, complexity, frameCount = 195000, 9, 2

	var cases []hd96kDynallocToneCase
	var programs []libopustest.EncodeDiffParams
	for _, frameSize := range []int{1920, 3840} {
		for _, channels := range []int{1, 2} {
			for _, mode := range []BitrateMode{BitrateModeVBR, BitrateModeCVBR} {
				constrained := mode == BitrateModeCVBR
				pcm := hd96kDynallocTonePCM(channels, frameSize, frameCount)
				cases = append(cases, hd96kDynallocToneCase{
					channels: channels, frameSize: frameSize, mode: mode, pcm: pcm,
				})
				programs = append(programs, libopustest.EncodeDiffParams{
					SampleRate: 96000, Channels: channels,
					Application: libopustest.EncodeDiffApplicationAudio,
					ForceMode:   libopustest.EncodeDiffForceModeCELTOnly,
					Bitrate:     bitrate, Complexity: complexity,
					Signal: libopustest.EncodeDiffSignalAuto,
					VBR:    true, VBRConstraint: constrained, LSBDepth: 24,
					FrameSize: frameSize, FrameCount: frameCount, PCM: pcm,
				})
			}
		}
	}
	want, err := libopustest.ProbeEncodeDiffBatch(programs)
	if err != nil {
		t.Fatalf("probe native 96 kHz QEXT-disabled encodes: %v", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("oracle sequences=%d, want %d", len(want), len(cases))
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("qext_off/ch%d/frame%d/%s", tc.channels, tc.frameSize, hd96kDynallocModeName(tc.mode)), func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{
				SampleRate: 96000, Channels: tc.channels, Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetBitrate(bitrate) },
				func() error { return enc.SetComplexity(complexity) },
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBitrateMode(tc.mode) },
				func() error { return enc.SetFrameSize(tc.frameSize) },
				func() error { return enc.SetQEXT(false) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			for frame := range frameCount {
				pcm := tc.pcm[frame*tc.frameSize*tc.channels : (frame+1)*tc.frameSize*tc.channels]
				got, err := enc.EncodeFloat32(pcm)
				if err != nil {
					t.Fatalf("frame %d encode: %v", frame, err)
				}
				ref := want[i][frame]
				if ref.Ret <= 0 || !bytes.Equal(got, ref.Packet) || enc.FinalRange() != ref.FinalRange {
					t.Fatalf("frame %d: Go bytes=%d range=%08x; C bytes=%d range=%08x ret=%d",
						frame, len(got), enc.FinalRange(), len(ref.Packet), ref.FinalRange, ref.Ret)
				}
			}
			if tc.channels == 2 && tc.frameSize == 1920 && tc.mode == BitrateModeVBR {
				frame := tc.pcm[:tc.frameSize*tc.channels]
				packet := make([]byte, 8000)
				if allocs := testing.AllocsPerRun(20, func() {
					if _, err := enc.Encode(frame, packet); err != nil {
						t.Fatalf("warm encode: %v", err)
					}
				}); allocs != 0 {
					t.Fatalf("warm native 96 kHz Encode allocs=%g, want 0", allocs)
				}
			}
		})
	}

	for _, frameSize := range []int{1920, 3840} {
		for _, channels := range []int{1, 2} {
			for _, mode := range []BitrateMode{BitrateModeVBR, BitrateModeCVBR} {
				mode := mode
				t.Run(fmt.Sprintf("qext_on/ch%d/frame%d/%s", channels, frameSize, hd96kDynallocModeName(mode)), func(t *testing.T) {
					pcm := hd96kDynallocTonePCM(channels, frameSize, frameCount)
					want, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
						Channels: channels, FrameSize: frameSize, Bitrate: bitrate,
						Complexity: complexity, VBR: true,
						VBRConstraint: mode == BitrateModeCVBR,
						MaxPacketSize: 8000, PCM: pcm, FrameCount: frameCount,
					})
					if err != nil {
						t.Fatalf("probe native 96 kHz QEXT encode: %v", err)
					}
					enc, err := NewEncoder(EncoderConfig{
						SampleRate: 96000, Channels: channels, Application: ApplicationRestrictedCelt,
					})
					if err != nil {
						t.Fatal(err)
					}
					for _, set := range []func() error{
						func() error { return enc.SetBitrate(bitrate) },
						func() error { return enc.SetComplexity(complexity) },
						func() error { return enc.SetBitrateMode(mode) },
						func() error { return enc.SetFrameSize(frameSize) },
						func() error { return enc.SetQEXT(true) },
					} {
						if err := set(); err != nil {
							t.Fatal(err)
						}
					}
					for frame := range frameCount {
						input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
						got, err := enc.EncodeFloat32(input)
						if err != nil {
							t.Fatalf("frame %d encode: %v", frame, err)
						}
						if !bytes.Equal(got, want.Packets[frame]) || enc.FinalRange() != want.FinalRanges[frame] {
							t.Fatalf("frame %d: Go bytes=%d range=%08x; C bytes=%d range=%08x",
								frame, len(got), enc.FinalRange(), len(want.Packets[frame]), want.FinalRanges[frame])
						}
					}
					if channels == 2 && frameSize == 1920 && mode == BitrateModeVBR {
						packet := make([]byte, 8000)
						if allocs := testing.AllocsPerRun(20, func() {
							if _, err := enc.Encode(pcm[:frameSize*channels], packet); err != nil {
								t.Fatalf("warm encode: %v", err)
							}
						}); allocs != 0 {
							t.Fatalf("warm native 96 kHz QEXT Encode allocs=%g, want 0", allocs)
						}
					}
				})
			}
		}
	}

}

func TestHD96kPublicFinalRangeMatchesLibopus(t *testing.T) {
	const frameSize = 1920
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			for _, gain := range []float32{1, -0.65} {
				t.Run(map[float32]string{1: "positive", -0.65: "inverted"}[gain], func(t *testing.T) {
					pcm := hd96kParitySine(channels, frameSize)
					for i := range pcm {
						pcm[i] *= gain
					}
					want, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
						Channels: channels, FrameSize: frameSize, Bitrate: 256000, Complexity: 10,
						VBR: false, MaxPacketSize: 8000, PCM: pcm, FrameCount: 1,
					})
					if err != nil {
						libopustest.HelperUnavailable(t, "native 96 kHz QEXT encoder", err)
						return
					}

					enc, err := NewEncoder(EncoderConfig{
						SampleRate: 96000, Channels: channels, Application: ApplicationRestrictedCelt,
					})
					if err != nil {
						t.Fatal(err)
					}
					for _, set := range []func() error{
						func() error { return enc.SetBitrate(256000) },
						func() error { return enc.SetBitrateMode(BitrateModeCBR) },
						func() error { return enc.SetComplexity(10) },
						func() error { return enc.SetQEXT(true) },
					} {
						if err := set(); err != nil {
							t.Fatal(err)
						}
					}
					packet := make([]byte, 8000)
					n, err := enc.Encode(pcm, packet)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(packet[:n], want.Packets[0]) {
						t.Fatalf("packet differs: Go len=%d C len=%d", n, len(want.Packets[0]))
					}
					if got, wantRange := enc.FinalRange(), want.FinalRanges[0]; got != wantRange {
						t.Fatalf("final range=%08x want=%08x", got, wantRange)
					}
				})
			}
		})
	}
}

func TestQEXT96kAutoChannelTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize  = 1920
		maxPacket  = 4000
		complexity = 10
		lsbDepth   = 24
	)
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeQEXT96kAutoPCM(frameSize, len(bitrates))
	frames := make([]libopustest.QEXT96kAutoFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.QEXT96kAutoFrame{Bitrate: bitrate, PCM: pcm[lo : lo+frameSize*2]}
	}
	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("qext_%t", qext), func(t *testing.T) {
			want, err := libopustest.ProbeQEXT96kAutoChannels(frameSize, maxPacket, complexity, lsbDepth, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "QEXT 96 kHz auto-channel sequence", err)
				return
			}
			enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: 2, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetComplexity(complexity) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(lsbDepth) },
				func() error { return enc.SetQEXT(qext) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			if enc.ForceChannels() != -1 {
				t.Fatalf("force channels=%d, want auto (-1)", enc.ForceChannels())
			}
			packet := make([]byte, maxPacket)
			wantStereo := [...]bool{false, true, false, false, true}
			for frame := range frames {
				if err := enc.SetBitrate(frames[frame].Bitrate); err != nil {
					t.Fatalf("frame %d SetBitrate: %v", frame, err)
				}
				n, err := enc.Encode(pcm[frame*frameSize*2:(frame+1)*frameSize*2], packet)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", frame, err)
				}
				got := packet[:n]
				if want[frame].Status != 0 || n != len(want[frame].Packet) ||
					enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Fatalf("frame %d bitrate=%d packet diff=%s range=%08x/%08x",
						frame, bitrates[frame], qext96AutoPacketDiff(got, want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
				}
				if len(want[frame].Packet) == 0 || (want[frame].Packet[0]&0x04 != 0) != wantStereo[frame] {
					t.Fatalf("C frame %d bitrate=%d stereo TOC=%t, want %t", frame, bitrates[frame],
						len(want[frame].Packet) > 0 && want[frame].Packet[0]&0x04 != 0, wantStereo[frame])
				}
				channels := uint32(1)
				if wantStereo[frame] {
					channels = 2
				}
				if want[frame].CELTCalls != 1 || want[frame].CELTFrameSize != frameSize ||
					want[frame].CELTStream != channels || want[frame].CELTBitrate != int32(bitrates[frame]) ||
					want[frame].CELTLSBDepth != lsbDepth || want[frame].CELTMaxBytes < 2 ||
					len(want[frame].CELTInput) != frameSize*2 {
					t.Fatalf("C frame %d CELT trace: calls=%d size=%d channels=%d bitrate=%d lsb=%d max=%d samples=%d",
						frame, want[frame].CELTCalls, want[frame].CELTFrameSize, want[frame].CELTStream,
						want[frame].CELTBitrate, want[frame].CELTLSBDepth, want[frame].CELTMaxBytes, len(want[frame].CELTInput))
				}
			}
		})
	}
}

func qext96AutoPacketDiff(got, want []byte) string {
	limit := min(len(got), len(want))
	diff := limit
	for i := range limit {
		if got[i] != want[i] {
			diff = i
			break
		}
	}
	return fmt.Sprintf("len=%d/%d first=%d", len(got), len(want), diff)
}

func makeQEXT96kAutoPCM(frameSize, frameCount int) []float32 {
	pcm := make([]float32, frameSize*2*frameCount)
	for frame := range frameCount {
		for i := range frameSize {
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/96000)
			high := 0.21 * math.Sin(2*math.Pi*30000*float64(i)/96000)
			for ch := range 2 {
				v := low + high*float64(ch+1)/2
				sample := int16(math.Round(v * 32767))
				pcm[(frame*frameSize+i)*2+ch] = float32(sample) / 32768
			}
		}
	}
	return pcm
}

func selectedQEXTDecodeSequenceReference(outputChannels, frameSize int, packets [][]byte) ([]float32, []uint32, error) {
	binPath, err := libopusQEXTDecodeSingleHelper.Path(buildLibopusQEXTDecodeSingleHelper)
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 8, 0, 48000, 0, uint32(outputChannels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0)
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "qext stateful float decoder", "GOSO", 3)
	if err != nil {
		return nil, nil, err
	}
	samplesPerPacket := frameSize * outputChannels
	pcm := make([]float32, len(packets)*samplesPerPacket)
	if got := reader.Count(len(pcm)); got != len(pcm) {
		return nil, nil, fmt.Errorf("C PCM count %d, want %d", got, len(pcm))
	}
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if got := reader.Count(len(packets)); got != len(packets) {
		return nil, nil, fmt.Errorf("C record count %d, want %d", got, len(packets))
	}
	ranges := make([]uint32, len(packets))
	for i := range packets {
		status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
		if status != 0 || samples != uint32(frameSize) || offset != uint32(i*samplesPerPacket) {
			return nil, nil, fmt.Errorf("C frame %d: status=%d samples=%d offset=%d", i, status, samples, offset)
		}
		ranges[i] = finalRange
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	if err := reader.Err(); err != nil {
		return nil, nil, err
	}
	return pcm, ranges, nil
}
