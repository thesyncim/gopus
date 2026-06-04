//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	encpkg "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	dredQualitySampleRate = 48000
	dredQualityFrameSize  = 960
	dredQualityChannels   = 1
)

type dredQualityRun struct {
	decoded        []float32
	lossReference  []float32
	lossDecoded    []float32
	lossFrames     int
	dredFrames     int
	fallbackFrames int
	dredPackets    int
}

type dredQualityMetrics struct {
	SNRDB       float64
	Correlation float64
	Envelope    float64
	OpusQ       float64
	OpusQOK     bool
}

func requireDREDAudioQualityGate(t *testing.T) {
	t.Helper()
	switch strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_DRED_AUDIO_QUALITY"))) {
	case "1", "true", "yes":
		return
	default:
		t.Skip("DRED long-sequence audio quality gate is experimental; set GOPUS_DRED_AUDIO_QUALITY=1 to run it")
	}
}

func TestExplicitDREDImprovesConcealedAudioQualityAtSixtyPercentLoss(t *testing.T) {
	requireDREDAudioQualityGate(t)
	libopustest.RequireOracle(t)
	encoderBlob := requireLibopusEncoderNeuralModelBlob(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredDecoderBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	decoderBlob = append(append([]byte(nil), decoderBlob...), dredDecoderBlob...)

	reference, packets := encodeDREDQualityPackets(t, encoderBlob)
	if len(packets) == 0 {
		t.Fatal("no packets encoded")
	}

	plc := decodeDREDQualityPackets(t, packets, reference, decoderBlob, false)
	dred := decodeDREDQualityPackets(t, packets, reference, decoderBlob, true)
	if dred.dredFrames == 0 {
		t.Fatal("explicit DRED did not recover any lost frames")
	}
	if plc.lossFrames != dred.lossFrames {
		t.Fatalf("loss frame count mismatch: plc=%d dred=%d", plc.lossFrames, dred.lossFrames)
	}
	if dred.lossFrames < 80 {
		t.Fatalf("loss pattern recovered too few frames: got %d", dred.lossFrames)
	}

	plcMetrics := measureDREDQuality(t, plc.lossReference, plc.lossDecoded)
	dredMetrics := measureDREDQuality(t, dred.lossReference, dred.lossDecoded)
	t.Logf("PLC loss quality:  snr=%.3f dB corr=%.5f env=%.5f opusQ=%s",
		plcMetrics.SNRDB, plcMetrics.Correlation, plcMetrics.Envelope, formatOptionalQuality(plcMetrics))
	t.Logf("DRED loss quality: snr=%.3f dB corr=%.5f env=%.5f opusQ=%s recovered=%d fallback=%d packets=%d",
		dredMetrics.SNRDB, dredMetrics.Correlation, dredMetrics.Envelope, formatOptionalQuality(dredMetrics),
		dred.dredFrames, dred.fallbackFrames, dred.dredPackets)

	if dredMetrics.Envelope < plcMetrics.Envelope+0.010 {
		t.Fatalf("DRED envelope quality did not improve enough: dred=%.5f plc=%.5f", dredMetrics.Envelope, plcMetrics.Envelope)
	}
	if !dredMetrics.OpusQOK || !plcMetrics.OpusQOK {
		t.Skip("opus_compare unavailable; envelope quality gate passed")
	}
	if dredMetrics.OpusQ < plcMetrics.OpusQ+20.0 {
		t.Fatalf("DRED opus_compare quality did not improve enough: dred=%.3f plc=%.3f", dredMetrics.OpusQ, plcMetrics.OpusQ)
	}
}

func encodeDREDQualityPackets(t *testing.T, encoderBlob []byte) ([]float32, [][]byte) {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  dredQualitySampleRate,
		Channels:    dredQualityChannels,
		Application: ApplicationLowDelay,
	})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(dredQualityFrameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetMaxBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetMaxBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(48000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatalf("SetComplexity error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(60); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("SetDNNBlob encoder error: %v", err)
	}
	if err := enc.SetDREDDuration(80); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	enc.enc.SetMode(encpkg.ModeCELT)

	const frames = 220
	packets := make([][]byte, 0, frames)
	reference := make([]float32, 0, frames*dredQualityFrameSize)
	pcm := make([]float32, dredQualityFrameSize*dredQualityChannels)
	packet := make([]byte, maxPacketBytesPerStream)
	for frame := 0; frame < frames; frame++ {
		fillDREDQualitySpeechFrame(pcm, frame)
		reference = append(reference, pcm...)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frame, err)
		}
		packets = append(packets, append([]byte(nil), packet[:n]...))
	}
	return reference, packets
}

func decodeDREDQualityPackets(t *testing.T, packets [][]byte, reference []float32, decoderBlob []byte, useDRED bool) dredQualityRun {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(dredQualitySampleRate, dredQualityChannels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob decoder error: %v", err)
	}
	probe := NewDREDDecoder()
	if err := probe.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("DREDDecoder SetDNNBlob error: %v", err)
	}
	dredState := NewDRED()

	var run dredQualityRun
	pcm := make([]float32, dec.maxPacketSamples*dredQualityChannels)
	expected := 0
	haveExpected := false
	for frame, packet := range packets {
		if !dredQualityPacketDelivered(frame) {
			continue
		}

		if haveExpected {
			missing := frame - expected
			if missing > 0 {
				available := 0
				dredReady := false
				if useDRED {
					var err error
					available, _, err = probe.Parse(dredState, packet, missing*dredQualityFrameSize, dredQualitySampleRate, false)
					dredReady = err == nil && available > 0 && dredState.Processed()
				}
				for lostAgo := missing; lostAgo >= 1; lostAgo-- {
					originalFrame := frame - lostAgo
					kindDRED := false
					var n int
					var err error
					if useDRED && dredReady && available >= lostAgo*dredQualityFrameSize {
						n, err = dec.DecodeDRED(dredState, lostAgo*dredQualityFrameSize, pcm, dredQualityFrameSize)
						if err == nil {
							kindDRED = true
						}
					}
					if !kindDRED {
						n, err = dec.Decode(nil, pcm)
					}
					if err != nil {
						t.Fatalf("decode loss frame=%d useDRED=%v: %v", originalFrame, useDRED, err)
					}
					run.appendDecodedFrame(reference, originalFrame, pcm[:n*dredQualityChannels], true, kindDRED)
				}
			}
		}

		n, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(frame=%d useDRED=%v) error: %v", frame, useDRED, err)
		}
		run.appendDecodedFrame(reference, frame, pcm[:n*dredQualityChannels], false, false)
		if payload, _, ok, err := findDREDPayload(packet); err != nil {
			t.Fatalf("findDREDPayload(frame=%d): %v", frame, err)
		} else if ok && len(payload) > 0 {
			run.dredPackets++
		}
		expected = frame + 1
		haveExpected = true
	}
	return run
}

func (r *dredQualityRun) appendDecodedFrame(reference []float32, frame int, decoded []float32, lost, dred bool) {
	r.decoded = append(r.decoded, decoded...)
	if !lost {
		return
	}
	start := frame * dredQualityFrameSize * dredQualityChannels
	end := start + dredQualityFrameSize*dredQualityChannels
	if start >= 0 && end <= len(reference) {
		r.lossReference = append(r.lossReference, reference[start:end]...)
		r.lossDecoded = append(r.lossDecoded, decoded...)
		r.lossFrames++
		if dred {
			r.dredFrames++
		} else {
			r.fallbackFrames++
		}
	}
}

func dredQualityPacketDelivered(frame int) bool {
	if frame < 20 {
		return true
	}
	switch (frame - 20) % 10 {
	case 0, 3, 6, 8:
		return true
	default:
		return false
	}
}

func fillDREDQualitySpeechFrame(dst []float32, frame int) {
	for i := range dst {
		n := frame*dredQualityFrameSize + i
		t := float64(n) / dredQualitySampleRate
		phase := 2 * math.Pi * (150.0*t -
			28.0*math.Cos(2*math.Pi*0.61*t)/(2*math.Pi*0.61) -
			9.0*math.Cos(2*math.Pi*2.3*t+0.4)/(2*math.Pi*2.3))
		voiced := 0.0
		for h := 1; h <= 6; h++ {
			amp := math.Exp(-0.34 * float64(h-1))
			voiced += amp * math.Sin(float64(h)*phase+0.17*float64(h*h))
		}
		formants := 0.16*math.Sin(2*math.Pi*(620.0+90.0*math.Sin(2*math.Pi*0.41*t))*t+0.4) +
			0.10*math.Sin(2*math.Pi*(1320.0+180.0*math.Sin(2*math.Pi*0.27*t))*t+0.9)
		syllable := 0.55 + 0.45*math.Pow(0.5+0.5*math.Sin(2*math.Pi*2.7*t+0.2), 2)
		onset := 1.0
		if n < dredQualitySampleRate/5 {
			onset = float64(n) / float64(dredQualitySampleRate/5)
		}
		dst[i] = float32(0.20 * onset * syllable * (0.68*voiced + formants))
	}
}

func measureDREDQuality(t *testing.T, reference, decoded []float32) dredQualityMetrics {
	t.Helper()
	if len(reference) == 0 || len(decoded) == 0 {
		t.Fatal("empty quality input")
	}
	n := len(reference)
	if len(decoded) < n {
		n = len(decoded)
	}
	reference = reference[:n]
	decoded = decoded[:n]
	metrics := dredQualityMetrics{
		SNRDB:       dredQualitySNR(reference, decoded),
		Correlation: dredQualityCorrelation(reference, decoded),
		Envelope:    dredQualityEnvelope(reference, decoded, dredQualitySampleRate),
	}
	if q, ok := runDREDOpusCompare(t, reference, decoded); ok {
		metrics.OpusQ = q
		metrics.OpusQOK = true
	}
	return metrics
}

func dredQualitySNR(reference, decoded []float32) float64 {
	var signal, errPower float64
	for i := range reference {
		r := float64(reference[i])
		d := float64(decoded[i])
		diff := r - d
		signal += r * r
		errPower += diff * diff
	}
	if errPower == 0 {
		return 120
	}
	return 10 * math.Log10(signal/errPower)
}

func dredQualityCorrelation(reference, decoded []float32) float64 {
	var dot, refPower, decPower float64
	for i := range reference {
		r := float64(reference[i])
		d := float64(decoded[i])
		dot += r * d
		refPower += r * r
		decPower += d * d
	}
	if refPower == 0 || decPower == 0 {
		return 0
	}
	return dot / math.Sqrt(refPower*decPower)
}

func dredQualityEnvelope(reference, decoded []float32, sampleRate int) float64 {
	const (
		window = 480
		hop    = 240
		bands  = 12
	)
	if len(reference) < window || len(decoded) < window {
		return 0
	}
	var sum float64
	var count int
	for band := 0; band < bands; band++ {
		center := 150.0 * math.Pow(2, float64(band)/3)
		refEnv := dredBandEnvelope(reference, sampleRate, center, window, hop)
		decEnv := dredBandEnvelope(decoded, sampleRate, center, window, hop)
		if len(refEnv) == len(decEnv) && len(refEnv) > 4 {
			if c := dredPearson(refEnv, decEnv); c > -2 {
				sum += c
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	score := sum / float64(count)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func dredBandEnvelope(samples []float32, sampleRate int, center float64, window, hop int) []float64 {
	frames := 1 + (len(samples)-window)/hop
	env := make([]float64, frames)
	lo := center / math.Pow(2, 1.0/6)
	hi := center * math.Pow(2, 1.0/6)
	binHz := float64(sampleRate) / float64(window)
	for frame := 0; frame < frames; frame++ {
		start := frame * hop
		var energy float64
		for bin := 1; bin <= window/2; bin++ {
			freq := float64(bin) * binHz
			if freq < lo || freq >= hi {
				continue
			}
			var re, im float64
			for i := 0; i < window; i++ {
				w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(window-1))
				v := float64(samples[start+i]) * w
				angle := -2 * math.Pi * float64(bin*i) / float64(window)
				re += v * math.Cos(angle)
				im += v * math.Sin(angle)
			}
			energy += re*re + im*im
		}
		env[frame] = math.Sqrt(energy)
	}
	return env
}

func dredPearson(x, y []float64) float64 {
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(len(x))
	my /= float64(len(y))
	var num, vx, vy float64
	for i := range x {
		dx := x[i] - mx
		dy := y[i] - my
		num += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx == 0 || vy == 0 {
		return -3
	}
	return num / math.Sqrt(vx*vy)
}

func runDREDOpusCompare(t *testing.T, reference, decoded []float32) (float64, bool) {
	t.Helper()
	opusCompare, ok := libopustooling.FindOrEnsureOpusCompare(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if !ok {
		t.Log("opus_compare unavailable; using in-process quality metrics only")
		return 0, false
	}
	tmpDir := t.TempDir()
	refPath := filepath.Join(tmpDir, "reference.sw")
	decPath := filepath.Join(tmpDir, "decoded.sw")
	if err := writeDREDQualityPCM16(refPath, duplicateDREDQualityMono(float32ToDREDPCM16(reference))); err != nil {
		t.Fatalf("write opus_compare reference: %v", err)
	}
	if err := writeDREDQualityPCM16(decPath, float32ToDREDPCM16(decoded)); err != nil {
		t.Fatalf("write opus_compare decoded: %v", err)
	}
	cmd := exec.Command(opusCompare, refPath, decPath)
	out, err := cmd.CombinedOutput()
	q, parseErr := parseDREDOpusCompareQuality(out)
	if parseErr != nil {
		t.Logf("opus_compare output unavailable: %v (%s)", parseErr, bytes.TrimSpace(out))
		return 0, false
	}
	if err != nil {
		t.Logf("opus_compare exited non-zero but returned quality: %v", err)
	}
	return q, true
}

func parseDREDOpusCompareQuality(out []byte) (float64, error) {
	const prefix = "Opus quality metric:"
	text := string(out)
	idx := bytes.Index([]byte(text), []byte(prefix))
	if idx >= 0 {
		rest := text[idx+len(prefix):]
		var q float64
		if _, err := fmt.Sscanf(rest, "%f", &q); err != nil {
			return 0, err
		}
		return q, nil
	}
	const errorPrefix = "Internal weighted error is"
	idx = bytes.Index(bytes.ToLower(out), []byte("internal weighted error is"))
	if idx < 0 {
		return 0, fmt.Errorf("quality metric not found")
	}
	rest := text[idx+len(errorPrefix):]
	var internalErr float64
	if _, err := fmt.Sscanf(rest, "%f", &internalErr); err != nil {
		return 0, err
	}
	return 100.0 * (1.0 - 0.5*math.Log(1.0+internalErr)/math.Log(1.13)), nil
}

func float32ToDREDPCM16(samples []float32) []int16 {
	out := make([]int16, len(samples))
	for i, sample := range samples {
		if sample >= 1 {
			out[i] = math.MaxInt16
			continue
		}
		if sample <= -1 {
			out[i] = math.MinInt16
			continue
		}
		out[i] = int16(math.Round(float64(sample) * 32767))
	}
	return out
}

func duplicateDREDQualityMono(samples []int16) []int16 {
	out := make([]int16, len(samples)*2)
	for i, sample := range samples {
		out[2*i] = sample
		out[2*i+1] = sample
	}
	return out
}

func writeDREDQualityPCM16(path string, samples []int16) error {
	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		u := uint16(sample)
		data[2*i] = byte(u)
		data[2*i+1] = byte(u >> 8)
	}
	return os.WriteFile(path, data, 0o600)
}

func formatOptionalQuality(m dredQualityMetrics) string {
	if !m.OpusQOK {
		return "unavailable"
	}
	return fmt.Sprintf("%.3f", m.OpusQ)
}
