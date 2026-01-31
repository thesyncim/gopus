// Package main demonstrates decoding an Ogg Opus file to WAV with optional playback.
//
// Usage:
//
//	go run . -in input.opus -out output.wav
//	go run . -in input.opus -play
//	go run . -url https://example.com/file.opus -pipe
//	go run . -pipe         # downloads the default sample and plays via ffplay
//	go run . -pipe -ffplay-first
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

const sampleRate = 48000

var sampleURLs = map[string]string{
	"stereo": "https://opus-codec.org/static/examples/ehren-paper_lights-96.opus",
	"speech": "https://upload.wikimedia.org/wikipedia/commons/6/6a/Hussain_Ahmad_Madani%27s_Voice.ogg",
}

type decodeStats struct {
	Packets     int
	Samples     int // per channel, after pre-skip
	Channels    int
	PreSkip     int
	Peak        float32
	DurationSec float64
}

func main() {
	input := flag.String("in", "", "Input Ogg Opus file")
	url := flag.String("url", "", "Download Ogg Opus file from URL (overrides -sample)")
	sample := flag.String("sample", "stereo", "Preset sample to download: stereo or speech")
	output := flag.String("out", "", "Output WAV file (16-bit PCM). Defaults to decoded.wav if not set")
	play := flag.Bool("play", false, "Play the decoded WAV (uses ffplay/afplay/aplay/paplay if available)")
	pipe := flag.Bool("pipe", false, "Stream raw PCM directly to ffplay (no temp files)")
	ffplayFirst := flag.Bool("ffplay-first", false, "Play the source first with ffplay, then decode with gopus")
	flag.Parse()

	inputPath := strings.TrimSpace(*input)
	urlValue := strings.TrimSpace(*url)
	if inputPath == "" && urlValue == "" {
		var err error
		urlValue, err = resolveSampleURL(*sample)
		if err != nil {
			log.Fatalf("Resolve sample failed: %v", err)
		}
	}

	if *ffplayFirst {
		if err := playSourceWithFFplay(inputPath, urlValue); err != nil {
			log.Fatalf("ffplay source failed: %v", err)
		}
	}

	source, sourceLabel, sourceClose, err := openSource(inputPath, urlValue)
	if err != nil {
		log.Fatalf("Open source failed: %v", err)
	}
	defer sourceClose()

	if *pipe {
		if *output != "" {
			log.Fatalf("Cannot use -out with -pipe (raw PCM streaming does not write a WAV file).")
		}

		stats, err := decodeOggToPipe(source)
		if err != nil {
			log.Fatalf("Decode failed: %v", err)
		}
		printStats(sourceLabel, "", stats, true)
		return
	}

	outPath := strings.TrimSpace(*output)
	tempOutput := false
	if outPath == "" {
		if *play {
			tmp, err := os.CreateTemp("", "gopus_decode_*.wav")
			if err != nil {
				log.Fatalf("Create temp WAV: %v", err)
			}
			outPath = tmp.Name()
			tempOutput = true
			_ = tmp.Close()
		} else {
			outPath = "decoded.wav"
		}
	}

	if tempOutput {
		defer os.Remove(outPath)
	}

	stats, err := decodeOggToWav(source, outPath)
	if err != nil {
		log.Fatalf("Decode failed: %v", err)
	}

	printStats(sourceLabel, outPath, stats, false)

	if *play {
		if err := playWav(outPath); err != nil {
			log.Printf("Playback failed: %v", err)
			fmt.Println("Try playing the WAV with a media player or install ffmpeg for ffplay.")
		}
	}
}

func printStats(sourceLabel, outputPath string, stats decodeStats, piped bool) {
	fmt.Printf("Decoded: %s\n", sourceLabel)
	if outputPath != "" {
		fmt.Printf("  Output: %s\n", outputPath)
	}
	if piped {
		fmt.Printf("  Output: ffplay (raw PCM)\n")
	}
	fmt.Printf("  Channels: %d\n", stats.Channels)
	fmt.Printf("  Pre-skip dropped: %d samples\n", stats.PreSkip)
	fmt.Printf("  Packets: %d\n", stats.Packets)
	fmt.Printf("  Samples: %d (%.2f seconds)\n", stats.Samples, stats.DurationSec)
	fmt.Printf("  Peak level: %.4f (%.1f dBFS)\n", stats.Peak, 20*math.Log10(float64(stats.Peak)))
}

func resolveSampleURL(name string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = "stereo"
	}
	url, ok := sampleURLs[key]
	if !ok {
		return "", fmt.Errorf("unknown sample %q (valid: stereo, speech)", name)
	}
	return url, nil
}

func openSource(inputPath, url string) (io.Reader, string, func(), error) {
	inputPath = strings.TrimSpace(inputPath)
	url = strings.TrimSpace(url)

	if inputPath != "" {
		f, err := os.Open(inputPath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("open input: %w", err)
		}
		return f, inputPath, func() { _ = f.Close() }, nil
	}

	if url == "" {
		return nil, "", nil, errors.New("no input provided")
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("download: %w", err)
	}
	req.Header.Set("User-Agent", "gopus-example/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", nil, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return nil, "", nil, fmt.Errorf("download: unexpected status %s", resp.Status)
	}

	return resp.Body, url, func() { _ = resp.Body.Close() }, nil
}

func decodeOggToWav(r io.Reader, outputPath string) (decodeStats, error) {
	var stats decodeStats

	oggReader, err := ogg.NewReader(r)
	if err != nil {
		return stats, fmt.Errorf("create ogg reader: %w", err)
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return stats, errors.New("invalid channel count in OpusHead")
	}

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return stats, fmt.Errorf("create decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	writer, err := newWavWriter(outputPath, sampleRate, channels)
	if err != nil {
		return stats, fmt.Errorf("create wav: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = writer.Close()
		}
	}()

	preSkip := int(oggReader.PreSkip())
	remainingSkip := preSkip

	var totalSamples int
	var peak float32
	var totalPackets int

	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return stats, fmt.Errorf("read packet: %w", err)
		}

		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			log.Printf("Warning: decode error on packet %d: %v", totalPackets, err)
			continue
		}

		samples := pcmOut[:n*channels]
		frameSamples := n
		if remainingSkip > 0 {
			if frameSamples <= remainingSkip {
				remainingSkip -= frameSamples
				continue
			}
			samples = samples[remainingSkip*channels:]
			remainingSkip = 0
		}

		if len(samples) == 0 {
			continue
		}

		for _, s := range samples {
			if s > peak {
				peak = s
			} else if -s > peak {
				peak = -s
			}
		}

		if err := writer.WriteSamples(samples); err != nil {
			return stats, fmt.Errorf("write wav: %w", err)
		}

		totalSamples += len(samples) / channels
		totalPackets++
	}

	if err := writer.Close(); err != nil {
		return stats, fmt.Errorf("finalize wav: %w", err)
	}
	closed = true

	stats = decodeStats{
		Packets:     totalPackets,
		Samples:     totalSamples,
		Channels:    channels,
		PreSkip:     preSkip,
		Peak:        peak,
		DurationSec: float64(totalSamples) / float64(sampleRate),
	}

	return stats, nil
}

func decodeOggToPipe(r io.Reader) (decodeStats, error) {
	var stats decodeStats

	oggReader, err := ogg.NewReader(r)
	if err != nil {
		return stats, fmt.Errorf("create ogg reader: %w", err)
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return stats, errors.New("invalid channel count in OpusHead")
	}

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return stats, fmt.Errorf("create decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	pipe, err := newPCMPlayer(channels)
	if err != nil {
		return stats, err
	}

	preSkip := int(oggReader.PreSkip())
	remainingSkip := preSkip

	var totalSamples int
	var peak float32
	var totalPackets int

	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return stats, fmt.Errorf("read packet: %w", err)
		}

		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			log.Printf("Warning: decode error on packet %d: %v", totalPackets, err)
			continue
		}

		samples := pcmOut[:n*channels]
		frameSamples := n
		if remainingSkip > 0 {
			if frameSamples <= remainingSkip {
				remainingSkip -= frameSamples
				continue
			}
			samples = samples[remainingSkip*channels:]
			remainingSkip = 0
		}

		if len(samples) == 0 {
			continue
		}

		for _, s := range samples {
			if s > peak {
				peak = s
			} else if -s > peak {
				peak = -s
			}
		}

		if err := pipe.WriteSamples(samples); err != nil {
			return stats, fmt.Errorf("playback write: %w", err)
		}

		totalSamples += len(samples) / channels
		totalPackets++
	}

	if err := pipe.Close(); err != nil {
		return stats, fmt.Errorf("playback finalize: %w", err)
	}

	stats = decodeStats{
		Packets:     totalPackets,
		Samples:     totalSamples,
		Channels:    channels,
		PreSkip:     preSkip,
		Peak:        peak,
		DurationSec: float64(totalSamples) / float64(sampleRate),
	}

	return stats, nil
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
	// Write placeholder header; sizes are filled on Close.
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
	case "linux":
		if player := lookup("aplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("paplay"); player != "" {
			return runPlayer(player, []string{path})
		}
	case "windows":
		if player := lookup("powershell"); player != "" {
			escaped := strings.ReplaceAll(path, "'", "''")
			script := fmt.Sprintf("(New-Object Media.SoundPlayer '%s').PlaySync()", escaped)
			return runPlayer(player, []string{"-NoProfile", "-Command", script})
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
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func playSourceWithFFplay(inputPath, url string) error {
	ffplay := lookup("ffplay")
	if ffplay == "" {
		return errors.New("ffplay not found in PATH")
	}
	source := strings.TrimSpace(inputPath)
	if source == "" {
		source = strings.TrimSpace(url)
	}
	if source == "" {
		return errors.New("no source to play")
	}
	if inputPath != "" {
		return runPlayer(ffplay, []string{"-autoexit", "-nodisp", source})
	}

	req, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	req.Header.Set("User-Agent", "gopus-example/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("download: unexpected status %s", resp.Status)
	}

	cmd := exec.Command(ffplay, "-autoexit", "-nodisp", "-i", "pipe:0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	_, copyErr := io.Copy(stdin, resp.Body)
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if copyErr != nil {
		return copyErr
	}
	return waitErr
}

type pcmPlayer struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func newPCMPlayer(channels int) (*pcmPlayer, error) {
	ffplay := lookup("ffplay")
	if ffplay == "" {
		return nil, errors.New("ffplay not found in PATH (install ffmpeg or use -play to play a WAV file)")
	}

	layout := "mono"
	if channels == 2 {
		layout = "stereo"
	}
	args := []string{
		"-autoexit",
		"-nodisp",
		"-f", "s16le",
		"-ar", strconv.Itoa(sampleRate),
		"-ch_layout", layout,
		"-i", "pipe:0",
	}

	cmd := exec.Command(ffplay, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}

	return &pcmPlayer{cmd: cmd, stdin: stdin}, nil
}

func (p *pcmPlayer) WriteSamples(samples []float32) error {
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
	_, err := p.stdin.Write(buf)
	return err
}

func (p *pcmPlayer) Close() error {
	if p == nil {
		return nil
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
}
