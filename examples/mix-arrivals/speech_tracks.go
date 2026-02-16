package main

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	fsddRepository = "https://github.com/Jakobovski/free-spoken-digit-dataset"
	fsddLicense    = "CC BY 4.0"
)

type SpeechSourceInfo struct {
	Dataset       string
	RepositoryURL string
	License       string
	ClipCount     int
}

type speechTrackPlan struct {
	Name        string
	StartOffset time.Duration
	Gain        float32
	Pan         float64
	ClipIDs     []string
}

func LoadOpenSourceSpeechTracks(cacheDir string) ([]TimedTrack, SpeechSourceInfo, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, SpeechSourceInfo{}, fmt.Errorf("create cache dir %q: %w", cacheDir, err)
	}

	plans := []speechTrackPlan{
		{
			Name:        "speaker-george",
			StartOffset: 0,
			Gain:        0.92,
			Pan:         -0.35,
			ClipIDs: []string{
				"0_george_0",
				"1_george_0",
				"2_george_0",
				"3_george_0",
			},
		},
		{
			Name:        "speaker-jackson",
			StartOffset: 550 * time.Millisecond,
			Gain:        0.90,
			Pan:         0.05,
			ClipIDs: []string{
				"4_jackson_0",
				"5_jackson_0",
				"6_jackson_0",
				"7_jackson_0",
			},
		},
		{
			Name:        "speaker-nicolas",
			StartOffset: 1100 * time.Millisecond,
			Gain:        0.94,
			Pan:         0.35,
			ClipIDs: []string{
				"8_nicolas_0",
				"9_nicolas_0",
				"0_nicolas_0",
				"1_nicolas_0",
			},
		},
	}

	const clipGap = 120 * time.Millisecond
	silenceGapSamples := durationToSamples(clipGap, sampleRate)
	silenceGap := make([]float32, silenceGapSamples*channels)

	tracks := make([]TimedTrack, 0, len(plans))
	clipCount := 0
	for _, plan := range plans {
		var trackPCM []float32
		for i, clipID := range plan.ClipIDs {
			url := fsddClipURL(clipID)
			clipPCM, err := loadSpeechClipStereo(url, plan.Pan, cacheDir)
			if err != nil {
				return nil, SpeechSourceInfo{}, fmt.Errorf("load clip %q: %w", clipID, err)
			}
			trackPCM = append(trackPCM, clipPCM...)
			clipCount++
			if i < len(plan.ClipIDs)-1 {
				trackPCM = append(trackPCM, silenceGap...)
			}
		}
		NormalizePeakInPlace(trackPCM, 0.90)

		tracks = append(tracks, TimedTrack{
			Name:        plan.Name,
			StartSample: durationToSamples(plan.StartOffset, sampleRate),
			Gain:        plan.Gain,
			PCM:         trackPCM,
		})
	}

	info := SpeechSourceInfo{
		Dataset:       "Free Spoken Digit Dataset",
		RepositoryURL: fsddRepository,
		License:       fsddLicense,
		ClipCount:     clipCount,
	}
	return tracks, info, nil
}

func fsddClipURL(clipID string) string {
	return "https://raw.githubusercontent.com/Jakobovski/free-spoken-digit-dataset/master/recordings/" + clipID + ".wav"
}

func loadSpeechClipStereo(url string, pan float64, cacheDir string) ([]float32, error) {
	data, err := downloadCached(url, cacheDir)
	if err != nil {
		return nil, err
	}

	interleaved, srcRate, srcChannels, err := decodePCM16WAV(data)
	if err != nil {
		return nil, err
	}
	mono := interleavedToMono(interleaved, srcChannels)
	mono = trimSilenceMono(mono, 0.01, durationToSamples(15*time.Millisecond, srcRate))
	mono = resampleLinearMono(mono, srcRate, sampleRate)
	if len(mono) == 0 {
		return nil, fmt.Errorf("clip %q decoded to empty audio", url)
	}
	applyFadeInOutMono(mono, durationToSamples(8*time.Millisecond, sampleRate))
	return monoToStereoPan(mono, pan), nil
}

func downloadCached(url, cacheDir string) ([]byte, error) {
	cachePath := filepath.Join(cacheDir, cacheFileName(url))
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		return data, nil
	}

	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "gopus-mix-arrivals/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %s", url, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("download %s returned empty body", url)
	}

	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write cache %s: %w", cachePath, err)
	}
	return data, nil
}

func cacheFileName(url string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(url))
	base := filepath.Base(url)
	base = strings.ReplaceAll(base, "/", "_")
	return fmt.Sprintf("%08x_%s", h.Sum32(), base)
}

func decodePCM16WAV(data []byte) ([]float32, int, int, error) {
	if len(data) < 44 {
		return nil, 0, 0, fmt.Errorf("wav too short")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("unsupported container (want RIFF/WAVE)")
	}

	var (
		audioFormat uint16
		channels    uint16
		sampleRate  uint32
		bitsPerSamp uint16
		pcmData     []byte
		gotFmt      bool
		gotData     bool
	)

	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			return nil, 0, 0, fmt.Errorf("invalid chunk size for %q", chunkID)
		}
		chunk := data[offset : offset+chunkSize]

		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, 0, 0, fmt.Errorf("wav fmt chunk too short")
			}
			audioFormat = binary.LittleEndian.Uint16(chunk[0:2])
			channels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bitsPerSamp = binary.LittleEndian.Uint16(chunk[14:16])
			gotFmt = true
		case "data":
			pcmData = chunk
			gotData = true
		}

		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if !gotFmt || !gotData {
		return nil, 0, 0, fmt.Errorf("wav missing fmt or data chunk")
	}
	if audioFormat != 1 {
		return nil, 0, 0, fmt.Errorf("unsupported wav format %d (only PCM=1)", audioFormat)
	}
	if channels == 0 {
		return nil, 0, 0, fmt.Errorf("invalid channel count 0")
	}
	if bitsPerSamp != 16 {
		return nil, 0, 0, fmt.Errorf("unsupported bits-per-sample %d (only 16)", bitsPerSamp)
	}
	if len(pcmData)%2 != 0 {
		return nil, 0, 0, fmt.Errorf("wav data length is not 16-bit aligned")
	}

	samples := make([]float32, len(pcmData)/2)
	for i := 0; i < len(samples); i++ {
		v := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
		samples[i] = float32(v) / 32768
	}
	return samples, int(sampleRate), int(channels), nil
}

func interleavedToMono(interleaved []float32, channels int) []float32 {
	if channels <= 1 {
		out := make([]float32, len(interleaved))
		copy(out, interleaved)
		return out
	}
	frames := len(interleaved) / channels
	out := make([]float32, frames)
	inv := 1 / float32(channels)
	for i := 0; i < frames; i++ {
		var sum float32
		base := i * channels
		for ch := 0; ch < channels; ch++ {
			sum += interleaved[base+ch]
		}
		out[i] = sum * inv
	}
	return out
}

func trimSilenceMono(samples []float32, threshold float32, keepSamples int) []float32 {
	if len(samples) == 0 {
		return samples
	}

	start := -1
	end := -1
	for i := range samples {
		if abs32(samples[i]) > threshold {
			start = i
			break
		}
	}
	if start == -1 {
		return samples
	}
	for i := len(samples) - 1; i >= 0; i-- {
		if abs32(samples[i]) > threshold {
			end = i
			break
		}
	}

	if keepSamples < 0 {
		keepSamples = 0
	}
	start -= keepSamples
	if start < 0 {
		start = 0
	}
	end += keepSamples
	if end >= len(samples) {
		end = len(samples) - 1
	}

	out := make([]float32, end-start+1)
	copy(out, samples[start:end+1])
	return out
}

func resampleLinearMono(src []float32, srcRate, dstRate int) []float32 {
	if len(src) == 0 || srcRate <= 0 || dstRate <= 0 {
		return make([]float32, 0)
	}
	if srcRate == dstRate {
		out := make([]float32, len(src))
		copy(out, src)
		return out
	}

	dstLen := int(math.Round(float64(len(src)) * float64(dstRate) / float64(srcRate)))
	if dstLen < 1 {
		dstLen = 1
	}
	out := make([]float32, dstLen)
	maxIdx := len(src) - 1
	scale := float64(srcRate) / float64(dstRate)
	for i := 0; i < dstLen; i++ {
		pos := float64(i) * scale
		idx := int(pos)
		if idx >= maxIdx {
			out[i] = src[maxIdx]
			continue
		}
		frac := float32(pos - float64(idx))
		a := src[idx]
		b := src[idx+1]
		out[i] = a + (b-a)*frac
	}
	return out
}

func monoToStereoPan(mono []float32, pan float64) []float32 {
	left, right := equalPowerPan(pan)
	out := make([]float32, len(mono)*2)
	for i, v := range mono {
		out[i*2] = v * left
		out[i*2+1] = v * right
	}
	return out
}

func applyFadeInOutMono(samples []float32, fadeSamples int) {
	if len(samples) == 0 || fadeSamples <= 0 {
		return
	}
	if fadeSamples*2 > len(samples) {
		fadeSamples = len(samples) / 2
	}
	if fadeSamples < 1 {
		return
	}

	for i := 0; i < fadeSamples; i++ {
		g := float32(i+1) / float32(fadeSamples)
		samples[i] *= g
		samples[len(samples)-1-i] *= g
	}
}
