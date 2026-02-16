package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

func playEncodedOutput(path string) error {
	if player := lookup("ffplay"); player != "" {
		if err := runCommand(player, []string{"-autoexit", "-nodisp", "-hide_banner", "-loglevel", "error", path}); err == nil {
			return nil
		}
	}

	tmp, err := os.CreateTemp("", "gopus_mix_arrivals_*.wav")
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

	decChannels := int(oggReader.Channels())
	if decChannels < 1 {
		return errors.New("invalid channel count in OpusHead")
	}

	decCfg := gopus.DefaultDecoderConfig(sampleRate, decChannels)
	dec, err := gopus.NewDecoder(decCfg)
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}

	pcmOut := make([]float32, decCfg.MaxPacketSamples*decChannels)
	preSkip := int(oggReader.PreSkip())

	writer, err := newWavWriter(wavPath, sampleRate, decChannels)
	if err != nil {
		return fmt.Errorf("create wav writer: %w", err)
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
			if err := writer.WriteSamples(pcmOut[start*decChannels : n*decChannels]); err != nil {
				return err
			}
		}
	}

	return nil
}

type wavWriter struct {
	f          *os.File
	dataSize   uint32
	sampleRate int
	channels   int
}

func newWavWriter(path string, sampleRateHz, ch int) (*wavWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(make([]byte, 44)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &wavWriter{
		f:          f,
		sampleRate: sampleRateHz,
		channels:   ch,
	}, nil
}

func (w *wavWriter) WriteSamples(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}

	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		scaled := float64(s) * 32768
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		v := int16(math.RoundToEven(scaled))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	n, err := w.f.Write(buf)
	if err != nil {
		return err
	}
	w.dataSize += uint32(n)
	return nil
}

func (w *wavWriter) Close() error {
	f := w.f
	if f == nil {
		return nil
	}
	w.f = nil

	header := make([]byte, 44)
	writeWavHeader(header, w.dataSize, w.sampleRate, w.channels)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(header); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func writeWavHeader(dst []byte, dataSize uint32, sampleRateHz, ch int) {
	copy(dst[0:4], "RIFF")
	binary.LittleEndian.PutUint32(dst[4:8], 36+dataSize)
	copy(dst[8:12], "WAVE")
	copy(dst[12:16], "fmt ")
	binary.LittleEndian.PutUint32(dst[16:20], 16)
	binary.LittleEndian.PutUint16(dst[20:22], 1)
	binary.LittleEndian.PutUint16(dst[22:24], uint16(ch))
	binary.LittleEndian.PutUint32(dst[24:28], uint32(sampleRateHz))
	binary.LittleEndian.PutUint32(dst[28:32], uint32(sampleRateHz*ch*2))
	binary.LittleEndian.PutUint16(dst[32:34], uint16(ch*2))
	binary.LittleEndian.PutUint16(dst[34:36], 16)
	copy(dst[36:40], "data")
	binary.LittleEndian.PutUint32(dst[40:44], dataSize)
}

func playWav(path string) error {
	if player := lookup("ffplay"); player != "" {
		return runCommand(player, []string{"-autoexit", "-nodisp", "-hide_banner", "-loglevel", "error", path})
	}

	switch runtime.GOOS {
	case "darwin":
		if p := lookup("afplay"); p != "" {
			return runCommand(p, []string{path})
		}
		if p := lookup("open"); p != "" {
			return runCommand(p, []string{path})
		}
	case "linux":
		if p := lookup("aplay"); p != "" {
			return runCommand(p, []string{path})
		}
		if p := lookup("paplay"); p != "" {
			return runCommand(p, []string{path})
		}
		if p := lookup("xdg-open"); p != "" {
			return runCommand(p, []string{path})
		}
	case "windows":
		if p := lookup("powershell"); p != "" {
			escaped := strings.ReplaceAll(path, "'", "''")
			script := fmt.Sprintf("(New-Object Media.SoundPlayer '%s').PlaySync()", escaped)
			return runCommand(p, []string{"-NoProfile", "-Command", script})
		}
		if p := lookup("cmd"); p != "" {
			return runCommand(p, []string{"/c", "start", "", path})
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

func runCommand(binary string, args []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
