//go:build gopus_dred || gopus_extra_controls

package encoder

import (
	"math"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDREDBitsTableMatchesLibopusReference(t *testing.T) {
	want := readLibopusDREDBitsTable(t)
	if len(want) != len(dredBitsTable) {
		t.Fatalf("dredBitsTable len=%d want %d", len(dredBitsTable), len(want))
	}
	for i, got := range dredBitsTable {
		if got != want[i] {
			t.Fatalf("dredBitsTable[%d]=%g want %g", i, got, want[i])
		}
	}
}

func TestComputeDREDEmissionPlanMatchesLibopusFormula(t *testing.T) {
	libopusBitsTable := readLibopusDREDBitsTable(t)

	rates := []struct {
		name       string
		sampleRate int
		frameSize  int
	}{
		{name: "8k_20ms", sampleRate: 8000, frameSize: 160},
		{name: "12k_20ms", sampleRate: 12000, frameSize: 240},
		{name: "16k_20ms", sampleRate: 16000, frameSize: 320},
		{name: "24k_20ms", sampleRate: 24000, frameSize: 480},
		{name: "48k_20ms", sampleRate: 48000, frameSize: 960},
		{name: "16k_60ms", sampleRate: 16000, frameSize: 960},
		{name: "48k_60ms", sampleRate: 48000, frameSize: 2880},
	}
	settings := []struct {
		name     string
		bitrate  int
		loss     int
		duration int
		fec      bool
	}{
		{name: "no_fec_low_loss", bitrate: 24000, loss: 3, duration: 8},
		{name: "no_fec_mid_loss", bitrate: 64000, loss: 10, duration: 8},
		{name: "no_fec_high_loss", bitrate: 96000, loss: 60, duration: 20},
		{name: "fec_mid_loss", bitrate: 64000, loss: 10, duration: 8, fec: true},
		{name: "fec_high_loss", bitrate: 96000, loss: 60, duration: 20, fec: true},
	}

	for _, rate := range rates {
		for _, setting := range settings {
			t.Run(rate.name+"/"+setting.name, func(t *testing.T) {
				enc := newDREDPlanTestEncoder(rate.sampleRate, setting.bitrate, setting.loss, setting.duration)
				enc.fecEnabled = setting.fec

				got, gotOK := enc.computeDREDEmissionPlan(rate.frameSize)
				want, wantOK := libopusDREDPlan(rate.sampleRate, rate.frameSize, setting.bitrate, setting.loss, setting.duration, setting.fec, libopusBitsTable)
				if gotOK != wantOK {
					t.Fatalf("enabled=%v want %v; got plan %+v want %+v", gotOK, wantOK, got, want)
				}
				if gotOK && got != want {
					t.Fatalf("plan=%+v want %+v", got, want)
				}
			})
		}
	}
}

func TestComputeDREDEmissionPlanUsesFECControlFlag(t *testing.T) {
	withoutLBRR := newDREDPlanTestEncoder(48000, 64000, 10, 8)
	withoutLBRR.fecEnabled = true
	withoutLBRR.lbrrCoded = false
	got, ok := withoutLBRR.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED with FEC enabled")
	}

	withLBRR := newDREDPlanTestEncoder(48000, 64000, 10, 8)
	withLBRR.fecEnabled = true
	withLBRR.lbrrCoded = true
	want, ok := withLBRR.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED with LBRR coded")
	}
	if got != want {
		t.Fatalf("plan with FEC enabled differs by LBRR state: got %+v want %+v", got, want)
	}

	noFEC := newDREDPlanTestEncoder(48000, 64000, 10, 8)
	noFEC.fecEnabled = false
	noFECPlan, ok := noFEC.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED without FEC")
	}
	if got == noFECPlan {
		t.Fatalf("FEC-enabled plan matched no-FEC plan: %+v", got)
	}
}

func TestDREDMaxChunksOnlyCapsVBR(t *testing.T) {
	enc := newDREDPlanTestEncoder(48000, 20000, 20, 80)
	plan, ok := enc.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED")
	}
	if plan.targetChunks != 2 {
		t.Fatalf("targetChunks=%d want 2", plan.targetChunks)
	}

	if got := maxDREDChunks(int(enc.dred.duration), int(plan.targetChunks), true); got != 2 {
		t.Fatalf("VBR maxDREDChunks()=%d want 2", got)
	}
	wantCBRChunks := int((enc.dred.duration + 5) / 4)
	if wantCBRChunks > internaldred.NumRedundancyFrames/2 {
		wantCBRChunks = internaldred.NumRedundancyFrames / 2
	}
	if got := maxDREDChunks(int(enc.dred.duration), int(plan.targetChunks), false); got != wantCBRChunks {
		t.Fatalf("CBR maxDREDChunks()=%d want %d", got, wantCBRChunks)
	}
}

func newDREDPlanTestEncoder(sampleRate, bitrate, packetLoss, duration int) *Encoder {
	return &Encoder{
		sampleRate: sampleRate,
		bitrate:    int32(bitrate),
		packetLoss: int32(packetLoss),
		encoderDREDFields: encoderDREDFields{
			dred: &dredEncoderExtras{
				duration: int32(duration),
				models: dredEncoderModels{
					encoder: &rdovae.EncoderModel{},
					pitch:   &lpcnetplc.PitchDNNModel{},
				},
			},
		},
	}
}

func libopusDREDPlan(sampleRate, frameSize, bitrate, packetLoss, duration int, fec bool, bitsTable []float32) (dredEmissionPlan, bool) {
	if sampleRate <= 0 || frameSize <= 0 || bitrate <= 0 || duration <= 0 {
		return dredEmissionPlan{}, false
	}
	if packetLoss < 0 {
		packetLoss = 0
	}
	if packetLoss > 100 {
		packetLoss = 100
	}

	var dredFrac float32
	bitrateOffset := 12000
	if fec {
		dredFrac = libopusMinFloat32(0.7, 3.0*float32(packetLoss)/100.0)
		bitrateOffset = 20000
	} else if packetLoss > 5 {
		dredFrac = libopusMinFloat32(0.8, 0.55+float32(packetLoss)/100.0)
	} else {
		dredFrac = 12.0 * float32(packetLoss) / 100.0
	}
	dredFrac = dredFrac / (dredFrac + (1-dredFrac)*(float32(frameSize*50)/float32(sampleRate)))

	rateBudget := bitrate - bitrateOffset
	if rateBudget < 1 {
		rateBudget = 1
	}
	q0 := 51 - 3*bits.Len(uint(rateBudget))
	if q0 < 4 {
		q0 = 4
	}
	if q0 > 15 {
		q0 = 15
	}
	dQ := 5
	if bitrate-bitrateOffset > 36000 {
		dQ = 3
	}
	qmax := 15
	targetDREDBitrate := int(dredFrac * float32(bitrate-bitrateOffset))
	if targetDREDBitrate < 0 {
		targetDREDBitrate = 0
	}
	targetBits := libopusDREDBitrateToBits(targetDREDBitrate, sampleRate, frameSize)
	maxBits, targetChunks := libopusEstimateDREDBits(q0, dQ, qmax, duration, targetBits, bitsTable)
	if targetChunks < 2 {
		return dredEmissionPlan{}, false
	}
	dredBitrate := libopusDREDBitsToBitrate(maxBits, sampleRate, frameSize)
	if targetDREDBitrate < dredBitrate {
		dredBitrate = targetDREDBitrate
	}
	if dredBitrate <= 0 {
		return dredEmissionPlan{}, false
	}
	return dredEmissionPlan{
		q0:           int32(q0),
		dQ:           int32(dQ),
		qmax:         int32(qmax),
		targetChunks: int32(targetChunks),
		bitrate:      int32(dredBitrate),
	}, true
}

func libopusEstimateDREDBits(q0, dQ, qmax, duration, targetBits int, bitsTable []float32) (int, int) {
	bitsUsed := float32(8 * (3 + internaldred.ExperimentalHeaderBytes))
	bitsUsed += 50.0 + bitsTable[q0]
	dredChunks := (duration + 5) / 4
	if dredChunks > internaldred.NumRedundancyFrames/2 {
		dredChunks = internaldred.NumRedundancyFrames / 2
	}
	targetChunks := 0
	for i := 0; i < dredChunks; i++ {
		q := libopusComputeDREDQuantizer(q0, dQ, qmax, i)
		bitsUsed += bitsTable[q]
		if bitsUsed < float32(targetBits) {
			targetChunks = i + 1
		}
	}
	return int(math.Floor(float64(float32(0.5) + bitsUsed))), targetChunks
}

func libopusComputeDREDQuantizer(q0, dQ, qmax, i int) int {
	dQTable := [...]int{0, 2, 3, 4, 6, 8, 12, 16}
	quant := q0 + (dQTable[dQ]*i+8)/16
	if quant > qmax {
		return qmax
	}
	return quant
}

func libopusDREDBitrateToBits(bitrate, sampleRate, frameSize int) int {
	if bitrate <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitrate * 6 / unitsPerFrame
}

func libopusDREDBitsToBitrate(bitCount, sampleRate, frameSize int) int {
	if bitCount <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitCount * unitsPerFrame / 6
}

func libopusMinFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func readLibopusDREDBitsTable(t *testing.T) []float32 {
	t.Helper()

	data := libopustest.ReadRefFileOrSkip(t, "opus_encoder.c", "src", "opus_encoder.c")

	re := regexp.MustCompile(`(?s)static\s+const\s+float\s+dred_bits_table\[16\]\s*=\s*\{([^}]*)\}`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatal("libopus dred_bits_table not found")
	}
	fields := strings.Split(string(m[1]), ",")
	values := make([]float32, 0, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(field)
		value = strings.TrimSuffix(value, "f")
		parsed, err := strconv.ParseFloat(value, 32)
		if err != nil {
			t.Fatalf("parse libopus dred_bits_table value %q: %v", field, err)
		}
		values = append(values, float32(parsed))
	}
	return values
}
