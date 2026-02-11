// Package main demonstrates encoding audio with gopus and playing the result.
//
// Usage:
//
//	go run . -play
//	go run . -signal sweep -duration 3 -bitrate 96000 -play
//	go run . -out output.opus
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

const (
	sampleRate = 48000
)

func main() {
	outPath := flag.String("out", "", "Output Ogg Opus file path (defaults to temp when -play is set)")
	duration := flag.Float64("duration", 2.0, "Duration in seconds")
	bitrate := flag.Int("bitrate", 128000, "Target bitrate in bps")
	channels := flag.Int("channels", 2, "Number of channels (1 or 2)")
	signal := flag.String("signal", "chord", "Signal type: sine, sweep, noise, chord, speech")
	frameSize := flag.Int("frame", 960, "Frame size in samples at 48kHz (e.g., 480, 960, 1920)")
	play := flag.Bool("play", true, "Play the encoded Opus file with ffplay if available")
	libopus := flag.Bool("libopus", false, "Use external libopus encoder (opusenc/ffmpeg) instead of gopus")
	flag.Parse()

	if *channels < 1 {
		*channels = 1
	}
	if *channels > 2 {
		*channels = 2
	}

	app := gopus.ApplicationAudio

	output := strings.TrimSpace(*outPath)
	tempOutput := false
	cleanup := func() {}
	if output == "" {
		if *play {
			tmp, err := os.CreateTemp("", "gopus_encode_*.opus")
			if err != nil {
				log.Fatalf("Create temp file: %v", err)
			}
			output = tmp.Name()
			tempOutput = true
			_ = tmp.Close()
			cleanup = func() { _ = os.Remove(output) }
		} else {
			output = "encoded.opus"
		}
	}

	var (
		stats encodeStats
		err   error
	)
	if *libopus {
		stats, err = encodeWithLibopus(output, *duration, *bitrate, *channels, *frameSize, *signal)
	} else {
		stats, err = encodeToOgg(output, *duration, *bitrate, *channels, *frameSize, app, *signal)
	}
	if err != nil {
		log.Fatalf("Encode failed: %v", err)
	}

	fmt.Printf("Encoded: %s\n", output)
	fmt.Printf("  Encoder: %s\n", stats.encoder)
	fmt.Printf("  Duration: %.2fs (%.2fs rendered)\n", stats.requestedDuration, stats.actualDuration)
	fmt.Printf("  Frames: %d\n", stats.frames)
	fmt.Printf("  Channels: %d\n", stats.channels)
	fmt.Printf("  Bitrate: %d kbps\n", stats.bitrate/1000)
	fmt.Printf("  Signal: %s\n", stats.signal)
	fmt.Printf("  Encoded bytes: %d\n", stats.encodedBytes)
	fmt.Printf("  Avg bitrate: %.1f kbps\n", float64(stats.encodedBytes*8)/stats.actualDuration/1000)

	if *play {
		if err := playEncoded(output); err != nil {
			log.Printf("Playback failed: %v", err)
			fmt.Printf("Play the .opus file in a media player: %s\n", output)
			cleanup = nil
		}
	}

	if tempOutput && cleanup != nil {
		cleanup()
	}
}

type encodeStats struct {
	requestedDuration float64
	actualDuration    float64
	frames            int
	channels          int
	bitrate           int
	signal            string
	encodedBytes      int
	encoder           string
}

func encodeToOgg(path string, duration float64, bitrate int, channels int, frameSize int, app gopus.Application, signal string) (encodeStats, error) {
	stats := encodeStats{
		requestedDuration: duration,
		channels:          channels,
		bitrate:           bitrate,
		signal:            signal,
		encoder:           "gopus",
	}

	enc, err := gopus.NewEncoder(sampleRate, channels, app)
	if err != nil {
		return stats, fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return stats, fmt.Errorf("set bitrate: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return stats, fmt.Errorf("set frame size: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return stats, fmt.Errorf("create output: %w", err)
	}
	defer f.Close()

	oggWriter, err := ogg.NewWriter(f, sampleRate, uint8(channels))
	if err != nil {
		return stats, fmt.Errorf("create ogg writer: %w", err)
	}

	totalSamples := int(math.Round(duration * float64(sampleRate)))
	if totalSamples < 1 {
		totalSamples = frameSize
	}
	frames := (totalSamples + frameSize - 1) / frameSize
	stats.frames = frames
	stats.actualDuration = float64(frames*frameSize) / float64(sampleRate)

	pcm := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)
	gen := newSignalGenerator(signal, totalSamples, channels)

	for frame := 0; frame < frames; frame++ {
		startSample := frame * frameSize
		gen.fillFrame(pcm, startSample, frameSize)

		n, err := enc.Encode(pcm, packet)
		if err != nil {
			return stats, fmt.Errorf("encode frame %d: %w", frame, err)
		}
		if n == 0 {
			continue
		}
		if err := oggWriter.WritePacket(packet[:n], frameSize); err != nil {
			return stats, fmt.Errorf("write packet %d: %w", frame, err)
		}
		stats.encodedBytes += n
	}

	if err := oggWriter.Close(); err != nil {
		return stats, fmt.Errorf("close ogg writer: %w", err)
	}

	return stats, nil
}

func encodeWithLibopus(path string, duration float64, bitrate int, channels int, frameSize int, signal string) (encodeStats, error) {
	stats := encodeStats{
		requestedDuration: duration,
		channels:          channels,
		bitrate:           bitrate,
		signal:            signal,
		encoder:           "libopus",
	}

	tmp, err := os.CreateTemp("", "gopus_encode_src_*.wav")
	if err != nil {
		return stats, fmt.Errorf("create temp wav: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	writer, err := newWavWriter(tmpPath, sampleRate, channels)
	if err != nil {
		return stats, fmt.Errorf("create wav: %w", err)
	}

	totalSamples := int(math.Round(duration * float64(sampleRate)))
	if totalSamples < 1 {
		totalSamples = frameSize
	}
	frames := (totalSamples + frameSize - 1) / frameSize
	stats.frames = frames
	stats.actualDuration = float64(frames*frameSize) / float64(sampleRate)

	pcm := make([]float32, frameSize*channels)
	gen := newSignalGenerator(signal, totalSamples, channels)
	for frame := 0; frame < frames; frame++ {
		startSample := frame * frameSize
		gen.fillFrame(pcm, startSample, frameSize)
		if err := writer.WriteSamples(pcm); err != nil {
			_ = writer.Close()
			return stats, fmt.Errorf("write wav: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return stats, fmt.Errorf("finalize wav: %w", err)
	}

	if err := runLibopusEncoder(tmpPath, path, bitrate, frameSize); err != nil {
		return stats, err
	}

	if st, err := os.Stat(path); err == nil {
		stats.encodedBytes = int(st.Size())
	}

	return stats, nil
}

func runLibopusEncoder(inputWav, outputOpus string, bitrate int, frameSize int) error {
	if opusenc := lookup("opusenc"); opusenc != "" {
		args := []string{"--bitrate", fmt.Sprintf("%d", bitrate/1000)}
		if frameMs, ok := frameSizeToMs(frameSize); ok {
			args = append(args, "--framesize", frameMs)
		}
		args = append(args, inputWav, outputOpus)
		return runCommand(opusenc, args)
	}

	if ffmpeg := lookup("ffmpeg"); ffmpeg != "" {
		args := []string{"-y", "-loglevel", "error", "-i", inputWav, "-c:a", "libopus", "-b:a", fmt.Sprintf("%dk", bitrate/1000), outputOpus}
		return runCommand(ffmpeg, args)
	}

	return errors.New("libopus encoder not found (install opusenc or ffmpeg)")
}

func frameSizeToMs(frameSize int) (string, bool) {
	switch frameSize {
	case 120:
		return "2.5", true
	case 240:
		return "5", true
	case 480:
		return "10", true
	case 960:
		return "20", true
	case 1920:
		return "40", true
	case 2880:
		return "60", true
	default:
		return "", false
	}
}

type signalGenerator struct {
	signal       string
	totalSamples int
	channels     int
	seed         uint32

	// Source-filter speech state.
	glottalPhase float64        // glottal oscillator phase [0,1)
	biquads      [5]biquadState // formant resonator states
	radHPState   [2]float64     // radiation HP filter state
	prevF0       float64        // previous pitch for smoothing
}

// biquadState holds the delay-line state for a second-order IIR resonator.
type biquadState struct {
	y1, y2 float64 // output history
}

// biquadResonator applies a single biquad formant resonator.
// H(z) = 1 / (1 - 2r·cos(θ)z⁻¹ + r²z⁻²)
// Unconditionally stable for bandwidth > 0.
func (b *biquadState) process(x float64, freq, bw float64) float64 {
	r := math.Exp(-math.Pi * bw / float64(sampleRate))
	theta := 2.0 * math.Pi * freq / float64(sampleRate)
	a1 := -2.0 * r * math.Cos(theta)
	a2 := r * r
	y := x - a1*b.y1 - a2*b.y2
	b.y2 = b.y1
	b.y1 = y
	return y
}

func newSignalGenerator(signal string, totalSamples int, channels int) *signalGenerator {
	return &signalGenerator{
		signal:       strings.ToLower(strings.TrimSpace(signal)),
		totalSamples: totalSamples,
		channels:     channels,
		seed:         12345,
	}
}

func (g *signalGenerator) fillFrame(pcm []float32, startSample int, frameSize int) {
	if len(pcm) == 0 {
		return
	}
	if g.channels < 1 {
		g.channels = 1
	}

	for i := 0; i < frameSize; i++ {
		sampleIndex := startSample + i
		if sampleIndex >= g.totalSamples {
			for ch := 0; ch < g.channels; ch++ {
				pcm[i*g.channels+ch] = 0
			}
			continue
		}

		t := float64(sampleIndex) / float64(sampleRate)
		progress := float64(sampleIndex) / float64(g.totalSamples)

		var left, right float32

		switch g.signal {
		case "sine":
			left = float32(0.5 * math.Sin(2*math.Pi*440*t))
			right = float32(0.5 * math.Sin(2*math.Pi*554.37*t+0.1))
		case "sweep":
			startHz := 100.0
			endHz := 8000.0
			freq := startHz + (endHz-startHz)*progress
			left = float32(0.5 * math.Sin(2*math.Pi*freq*t))
			right = float32(0.5 * math.Sin(2*math.Pi*(freq*1.05)*t))
		case "noise":
			left = g.nextNoiseSample(0.4)
			right = g.nextNoiseSample(0.4)
		case "speech":
			left = g.speechSample(t, sampleIndex)
			right = left
		case "chord":
			left, right = g.chordSample(t, progress)
		default:
			left = float32(0.5 * math.Sin(2*math.Pi*440*t))
			right = left
		}

		if g.channels == 1 {
			pcm[i] = left
			continue
		}

		pcm[i*g.channels] = left
		pcm[i*g.channels+1] = right
	}
}

func (g *signalGenerator) chordSample(t float64, progress float64) (float32, float32) {
	freqs := []float64{261.63, 329.63, 392.0}
	amp := 0.15 * math.Min(1.0, progress*5)
	vibrato := 1.0 + 0.05*math.Sin(2*math.Pi*5*t)
	var sample float64
	for i, freq := range freqs {
		detune := 1.0 + 0.002*math.Sin(2*math.Pi*0.1*t+float64(i))
		sample += amp * math.Sin(2*math.Pi*freq*detune*t)
	}
	sample *= vibrato
	pan := 0.5 + 0.4*math.Sin(2*math.Pi*0.2*t)
	left := float32(sample * pan)
	right := float32(sample * (1.0 - pan))
	return left, right
}

func (g *signalGenerator) nextNoiseSample(scale float32) float32 {
	g.seed = g.seed*1103515245 + 12345
	val := float32((g.seed>>16)&0x7FFF)/32768.0 - 0.5
	return val * scale
}

// speechSample generates speech using a source-filter model:
//  1. Glottal source: Rosenberg pulse (asymmetric open/close phases)
//  2. Formant filter: cascade of 5 biquad resonators (unconditionally stable)
//  3. Radiation: first-order high-pass (lip radiation effect)
func (g *signalGenerator) speechSample(t float64, _ int) float32 {
	// --- Pitch contour ---
	f0 := 120.0 + 15.0*math.Sin(2*math.Pi*0.35*t) + // intonation
		4.0*math.Sin(2*math.Pi*5.5*t) // vibrato
	if f0 < 60 {
		f0 = 60
	}
	// Smooth pitch to avoid clicks.
	if g.prevF0 == 0 {
		g.prevF0 = f0
	}
	f0 = g.prevF0 + 0.1*(f0-g.prevF0)
	g.prevF0 = f0

	// --- Syllable envelope (~3 syl/sec) ---
	syllableRate := 3.0
	syllablePhase := math.Mod(t*syllableRate, 1.0)
	var syllableAmp float64
	switch {
	case syllablePhase < 0.10:
		syllableAmp = 0.5 - 0.5*math.Cos(math.Pi*syllablePhase/0.10)
	case syllablePhase < 0.55:
		syllableAmp = 1.0
	case syllablePhase < 0.75:
		syllableAmp = 0.5 + 0.5*math.Cos(math.Pi*(syllablePhase-0.55)/0.20)
	default:
		syllableAmp = 0.0
	}

	// --- Glottal source (Rosenberg pulse model) ---
	// Advance glottal phase.
	g.glottalPhase += f0 / float64(sampleRate)
	if g.glottalPhase >= 1.0 {
		g.glottalPhase -= math.Floor(g.glottalPhase)
	}

	var source float64
	tp := 0.40 // open phase ratio
	tn := 0.16 // closing phase ratio
	phase := g.glottalPhase
	if phase < tp {
		// Opening phase: half-cosine rise.
		source = 0.5 - 0.5*math.Cos(math.Pi*phase/tp)
	} else if phase < tp+tn {
		// Closing phase: cosine fall.
		source = math.Cos(0.5 * math.Pi * (phase - tp) / tn)
	} else {
		// Closed phase.
		source = 0.0
	}

	// Add aspiration noise modulated by glottal open phase.
	if syllableAmp > 0.05 {
		aspiration := float64(g.nextNoiseSample(1.0))
		if phase < tp+tn {
			source += 0.04 * aspiration // more noise during open phase
		} else {
			source += 0.01 * aspiration
		}
	}

	source *= syllableAmp

	// --- Vowel formants (smooth interpolation) ---
	type fmtSet struct{ f, bw [5]float64 }
	vowels := [5]fmtSet{
		{f: [5]float64{730, 1090, 2440, 3300, 3750}, bw: [5]float64{90, 110, 170, 250, 300}}, // /a/
		{f: [5]float64{270, 2290, 3010, 3500, 4100}, bw: [5]float64{60, 100, 150, 200, 280}}, // /i/
		{f: [5]float64{300, 870, 2240, 3200, 3700}, bw: [5]float64{65, 100, 140, 220, 280}},  // /u/
		{f: [5]float64{530, 1840, 2480, 3300, 3900}, bw: [5]float64{70, 110, 150, 230, 290}}, // /e/
		{f: [5]float64{570, 840, 2410, 3250, 3750}, bw: [5]float64{80, 105, 155, 240, 300}},  // /o/
	}

	vowelPos := math.Mod(t*syllableRate, 5.0)
	idx0 := int(vowelPos) % 5
	idx1 := (idx0 + 1) % 5
	frac := vowelPos - math.Floor(vowelPos)
	alpha := 0.5 - 0.5*math.Cos(math.Pi*frac) // cosine interpolation

	// --- Apply cascade of 5 biquad resonators ---
	sample := source
	for i := 0; i < 5; i++ {
		freq := vowels[idx0].f[i] + alpha*(vowels[idx1].f[i]-vowels[idx0].f[i])
		bw := vowels[idx0].bw[i] + alpha*(vowels[idx1].bw[i]-vowels[idx0].bw[i])
		// Gain reduction for higher formants.
		gain := 1.0
		switch i {
		case 1:
			gain = 0.5
		case 2:
			gain = 0.25
		case 3:
			gain = 0.12
		case 4:
			gain = 0.06
		}
		sample = gain * g.biquads[i].process(sample, freq, bw)
	}

	// --- Radiation filter (first-order high-pass: y[n] = x[n] - x[n-1]) ---
	radiated := sample - g.radHPState[0]
	g.radHPState[0] = sample

	// Scale output.
	radiated *= 0.0003

	// Soft-clip.
	radiated = math.Tanh(radiated)
	return float32(radiated)
}

func playEncoded(path string) error {
	if player := lookup("ffplay"); player != "" {
		if err := runPlayer(player, []string{"-autoexit", "-nodisp", "-hide_banner", "-loglevel", "error", path}); err == nil {
			return nil
		}
	}

	tmp, err := os.CreateTemp("", "gopus_encode_*.wav")
	if err != nil {
		return fmt.Errorf("create temp wav: %w", err)
	}
	wavPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(wavPath)

	if err := decodeOpusToWav(path, wavPath); err != nil {
		return fmt.Errorf("decode to wav: %w", err)
	}

	return playWav(wavPath)
}

func decodeOpusToWav(opusPath, wavPath string) error {
	f, err := os.Open(opusPath)
	if err != nil {
		return err
	}
	defer f.Close()

	oggReader, err := ogg.NewReader(f)
	if err != nil {
		return fmt.Errorf("create ogg reader: %w", err)
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return errors.New("invalid channel count in OpusHead")
	}

	decCfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(decCfg)
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}

	pcmOut := make([]float32, decCfg.MaxPacketSamples*channels)
	preSkip := int(oggReader.PreSkip())

	writer, err := newWavWriter(wavPath, sampleRate, channels)
	if err != nil {
		return fmt.Errorf("create wav: %w", err)
	}
	defer writer.Close()

	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}

		start := 0
		if preSkip > 0 {
			if preSkip >= n {
				preSkip -= n
				continue
			}
			start = preSkip
			preSkip = 0
		}

		if start < n {
			if err := writer.WriteSamples(pcmOut[start*channels : n*channels]); err != nil {
				return err
			}
		}
	}

	return writer.Close()
}

type wavWriter struct {
	f          *os.File
	dataSize   uint32
	sampleRate int
	channels   int
}

func newWavWriter(path string, sampleRate, channels int) (*wavWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(make([]byte, 44)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &wavWriter{f: f, sampleRate: sampleRate, channels: channels}, nil
}

func (w *wavWriter) WriteSamples(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}

	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		scaled := float64(s) * 32768.0
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		val := int16(math.RoundToEven(scaled))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(val))
	}

	written, err := w.f.Write(buf)
	if err != nil {
		return err
	}
	w.dataSize += uint32(written)
	return nil
}

func (w *wavWriter) Close() error {
	if w.f == nil {
		return nil
	}

	header := make([]byte, 44)
	writeWavHeader(header, w.dataSize, w.sampleRate, w.channels)

	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		_ = w.f.Close()
		return err
	}
	if _, err := w.f.Write(header); err != nil {
		_ = w.f.Close()
		return err
	}
	return w.f.Close()
}

func writeWavHeader(dst []byte, dataSize uint32, sampleRate, channels int) {
	copy(dst[0:4], "RIFF")
	binary.LittleEndian.PutUint32(dst[4:8], 36+dataSize)
	copy(dst[8:12], "WAVE")
	copy(dst[12:16], "fmt ")
	binary.LittleEndian.PutUint32(dst[16:20], 16)
	binary.LittleEndian.PutUint16(dst[20:22], 1)
	binary.LittleEndian.PutUint16(dst[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(dst[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(dst[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(dst[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(dst[34:36], 16)
	copy(dst[36:40], "data")
	binary.LittleEndian.PutUint32(dst[40:44], dataSize)
}

func playWav(path string) error {
	if player := lookup("ffplay"); player != "" {
		return runPlayer(player, []string{"-autoexit", "-nodisp", path})
	}

	switch runtime.GOOS {
	case "darwin":
		if player := lookup("afplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("open"); player != "" {
			return runPlayer(player, []string{path})
		}
	case "linux":
		if player := lookup("aplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("paplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("xdg-open"); player != "" {
			return runPlayer(player, []string{path})
		}
	case "windows":
		if player := lookup("powershell"); player != "" {
			escaped := strings.ReplaceAll(path, "'", "''")
			script := fmt.Sprintf("(New-Object Media.SoundPlayer '%s').PlaySync()", escaped)
			return runPlayer(player, []string{"-NoProfile", "-Command", script})
		}
		if player := lookup("cmd"); player != "" {
			return runPlayer(player, []string{"/c", "start", "", path})
		}
	}

	return errors.New("no audio player found in PATH")
}

func lookup(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func runPlayer(binary string, args []string) error {
	return runCommand(binary, args)
}

func runCommand(binary string, args []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
