package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	examplecleanup "github.com/thesyncim/gopus/examples/internal/cleanup"
	"github.com/thesyncim/gopus/examples/internal/wav"
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
	defer examplecleanup.OnReturn("remove temporary WAV", func() error { return os.Remove(wavPath) })

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
	defer examplecleanup.OnReturn("close Opus input", f.Close)

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

	writer, err := wav.NewWriter(wavPath, sampleRate, decChannels)
	if err != nil {
		return fmt.Errorf("create wav writer: %w", err)
	}
	defer examplecleanup.OnReturn("close WAV writer", writer.Close)

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

	if err := writer.Close(); err != nil {
		return fmt.Errorf("finalize WAV: %w", err)
	}
	return nil
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
