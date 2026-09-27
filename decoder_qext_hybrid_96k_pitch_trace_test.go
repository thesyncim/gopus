//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"math"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

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
			goHist := core.FieldByName("plcDecodeMem")
			if !goHist.IsValid() {
				t.Fatal("CELT decoder has no PLC decode history")
			}
			historyLen := goHist.Len() / channels
			oldHist := make([]float32, goHist.Len())
			ringActive := core.FieldByName("plcDecodeMemRingActive").Bool()
			ringStart := int(core.FieldByName("plcDecodeMemRingStart").Int())
			for channel := 0; channel < channels; channel++ {
				for i := 0; i < historyLen; i++ {
					src := i
					if ringActive {
						src = (ringStart + i) % historyLen
					}
					oldHist[channel*historyLen+i] = float32(goHist.Index(channel*historyLen + src).Float())
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
		sum = float32(math.FMA(float64(x), float64(x), float64(sum)))
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
