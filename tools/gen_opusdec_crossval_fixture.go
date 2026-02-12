package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"

	"github.com/thesyncim/gopus/celt"
)

const (
	defaultOpusdecCrossvalFixturePath = "celt/testdata/opusdec_crossval_fixture.json"
	opusdecCrossvalFixtureOutEnv      = "GOPUS_OPUSDEC_CROSSVAL_FIXTURE_OUT"
)

type fixtureFile struct {
	Version int            `json:"version"`
	Entries []fixtureEntry `json:"entries"`
}

type fixtureEntry struct {
	Name             string `json:"name"`
	SHA256           string `json:"sha256"`
	SampleRate       int    `json:"sample_rate"`
	Channels         int    `json:"channels"`
	DecodedF32Base64 string `json:"decoded_f32le_base64"`
}

type scenario struct {
	name       string
	channels   int
	numFrames  int
	oggPayload []byte
}

func main() {
	opusdec, err := exec.LookPath("opusdec")
	if err != nil {
		fmt.Fprintf(os.Stderr, "opusdec not found in PATH: %v\n", err)
		os.Exit(1)
	}

	scenarios, err := buildScenarios()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build scenarios: %v\n", err)
		os.Exit(1)
	}

	entries := make([]fixtureEntry, 0, len(scenarios))
	for _, sc := range scenarios {
		decoded, err := decodeWithOpusdec(opusdec, sc.oggPayload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: opusdec decode failed: %v\n", sc.name, err)
			os.Exit(1)
		}
		hash := sha256.Sum256(sc.oggPayload)
		entries = append(entries, fixtureEntry{
			Name:             sc.name,
			SHA256:           hex.EncodeToString(hash[:]),
			SampleRate:       48000,
			Channels:         sc.channels,
			DecodedF32Base64: encodeFloat32LEBase64(decoded),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	out := fixtureFile{
		Version: 1,
		Entries: entries,
	}
	js, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal fixture: %v\n", err)
		os.Exit(1)
	}
	js = append(js, '\n')

	targetPath := outputFixturePath()
	if err := os.WriteFile(targetPath, js, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write fixture: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("wrote %d entries to %s\n", len(entries), targetPath)
}

func outputFixturePath() string {
	if path := os.Getenv(opusdecCrossvalFixtureOutEnv); path != "" {
		return path
	}
	return defaultOpusdecCrossvalFixturePath
}

func buildScenarios() ([]scenario, error) {
	makeScenario := func(name string, channels int, packets [][]byte) (scenario, error) {
		var ogg bytes.Buffer
		if err := writeOggOpus(&ogg, packets, 48000, channels); err != nil {
			return scenario{}, err
		}
		return scenario{
			name:       name,
			channels:   channels,
			numFrames:  len(packets),
			oggPayload: ogg.Bytes(),
		}, nil
	}

	encodeMonoFrame := func(name string, bitrate int, input []float64) (scenario, error) {
		enc := celt.NewEncoder(1)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(input, 960)
		if err != nil {
			return scenario{}, err
		}
		return makeScenario(name, 1, [][]byte{cloneBytes(packet)})
	}

	encodeStereoFrame := func(name string, bitrate int, input []float64) (scenario, error) {
		enc := celt.NewEncoder(2)
		enc.SetBitrate(bitrate)
		packet, err := enc.EncodeFrame(input, 960)
		if err != nil {
			return scenario{}, err
		}
		return makeScenario(name, 2, [][]byte{cloneBytes(packet)})
	}

	encodeMonoFrames := func(name string, bitrate int, frames [][]float64) (scenario, error) {
		enc := celt.NewEncoder(1)
		enc.SetBitrate(bitrate)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(frame, 960)
			if err != nil {
				return scenario{}, err
			}
			packets[i] = cloneBytes(packet)
		}
		return makeScenario(name, 1, packets)
	}

	encodeStereoFramesCBR := func(name string, bitrate int, frames [][]float64) (scenario, error) {
		enc := celt.NewEncoder(2)
		enc.SetBitrate(bitrate)
		enc.SetVBR(false)
		packets := make([][]byte, len(frames))
		for i, frame := range frames {
			packet, err := enc.EncodeFrame(frame, 960)
			if err != nil {
				return scenario{}, err
			}
			packets[i] = cloneBytes(packet)
		}
		return makeScenario(name, 2, packets)
	}

	out := make([]scenario, 0, 11)
	var err error

	add := func(sc scenario, e error) {
		if err == nil && e != nil {
			err = e
			return
		}
		if e == nil {
			out = append(out, sc)
		}
	}

	add(encodeMonoFrames("mono_20ms_single", 64000, buildMonoSineFrames(440.0, 960, 3)))
	add(encodeStereoFrame("stereo_20ms_single", 128000, generateStereoSineWave(440.0, 880.0, 960)))
	add(encodeMonoFrame("mono_20ms_silence", 64000, make([]float64, 960)))
	add(encodeMonoFrames("mono_20ms_multiframe", 64000, [][]float64{
		generateSineWave(440.0, 960),
		generateSineWave(540.0, 960),
		generateSineWave(640.0, 960),
		generateSineWave(740.0, 960),
		generateSineWave(840.0, 960),
	}))
	add(encodeMonoFrames("mono_20ms_chirp", 64000, buildMonoChirpFrames(960, 3, 180.0, 5200.0)))
	add(encodeMonoFrame("mono_20ms_impulse", 48000, buildMonoFrameImpulse(960)))
	add(encodeMonoFrame("mono_20ms_noise", 32000, buildMonoFramePseudoNoise(960)))
	add(encodeMonoFrame("mono_20ms_lowamp", 24000, scaleSignal(generateSineWave(880.0, 960), 0.12)))
	add(encodeStereoFrame("stereo_20ms_chirp", 96000, buildStereoFrameChirp(960)))
	add(encodeStereoFrame("stereo_20ms_silence", 96000, make([]float64, 960*2)))
	add(encodeStereoFramesCBR("stereo_20ms_multiframe", 96000, [][]float64{
		buildStereoFrameDualTone(960, 300.0, 500.0),
		buildStereoFrameDualTone(960, 520.0, 920.0),
		buildStereoFrameDualTone(960, 760.0, 1240.0),
		buildStereoFrameDualTone(960, 990.0, 1670.0),
	}))

	if err != nil {
		return nil, err
	}
	return out, nil
}

func cloneBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func generateSineWave(freqHz float64, numSamples int) []float64 {
	samples := make([]float64, numSamples)
	sampleRate := 48000.0
	for i := 0; i < numSamples; i++ {
		t := float64(i) / sampleRate
		samples[i] = 0.5 * math.Sin(2*math.Pi*freqHz*t)
	}
	return samples
}

func generateStereoSineWave(freqL, freqR float64, samplesPerChannel int) []float64 {
	samples := make([]float64, samplesPerChannel*2)
	sampleRate := 48000.0
	for i := 0; i < samplesPerChannel; i++ {
		t := float64(i) / sampleRate
		samples[i*2] = 0.5 * math.Sin(2*math.Pi*freqL*t)
		samples[i*2+1] = 0.5 * math.Sin(2*math.Pi*freqR*t)
	}
	return samples
}

func buildMonoFrameChirp(samples int, startHz, endHz float64) []float64 {
	out := make([]float64, samples)
	for i := 0; i < samples; i++ {
		t := float64(i) / 48000.0
		progress := float64(i) / float64(samples-1)
		freq := startHz + (endHz-startHz)*progress
		amp := 0.48 * (0.7 + 0.3*math.Sin(2*math.Pi*2.0*t))
		out[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	return out
}

func buildMonoSineFrames(freqHz float64, frameSize, numFrames int) [][]float64 {
	total := frameSize * numFrames
	all := make([]float64, total)
	for i := 0; i < total; i++ {
		t := float64(i) / 48000.0
		all[i] = 0.5 * math.Sin(2*math.Pi*freqHz*t)
	}
	frames := make([][]float64, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		frame := make([]float64, frameSize)
		copy(frame, all[start:end])
		frames[i] = frame
	}
	return frames
}

func buildMonoChirpFrames(frameSize, numFrames int, startHz, endHz float64) [][]float64 {
	total := frameSize * numFrames
	all := make([]float64, total)
	for i := 0; i < total; i++ {
		t := float64(i) / 48000.0
		progress := float64(i) / float64(total-1)
		freq := startHz + (endHz-startHz)*progress
		amp := 0.48 * (0.7 + 0.3*math.Sin(2*math.Pi*2.0*t))
		all[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	frames := make([][]float64, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		frame := make([]float64, frameSize)
		copy(frame, all[start:end])
		frames[i] = frame
	}
	return frames
}

func buildMonoFrameImpulse(samples int) []float64 {
	out := make([]float64, samples)
	for i := 0; i < samples; i++ {
		if i%120 == 0 {
			out[i] = 0.9
			continue
		}
		decay := math.Exp(-float64(i%120) / 22.0)
		out[i] = 0.25 * decay * math.Sin(2*math.Pi*2200.0*float64(i)/48000.0)
	}
	return out
}

func buildMonoFramePseudoNoise(samples int) []float64 {
	out := make([]float64, samples)
	var x uint32 = 0x1badf00d
	for i := 0; i < samples; i++ {
		x = 1664525*x + 1013904223
		v := float64((x>>9)&0x7fffff) / float64(0x7fffff)
		out[i] = 0.42 * (2.0*v - 1.0)
	}
	return out
}

func buildStereoFrameChirp(samples int) []float64 {
	out := make([]float64, samples*2)
	left := buildMonoFrameChirp(samples, 220.0, 4200.0)
	right := buildMonoFrameChirp(samples, 4200.0, 220.0)
	for i := 0; i < samples; i++ {
		out[i*2] = left[i]
		out[i*2+1] = right[i] * 0.95
	}
	return out
}

func buildStereoFrameDualTone(samples int, fL, fR float64) []float64 {
	out := make([]float64, samples*2)
	for i := 0; i < samples; i++ {
		t := float64(i) / 48000.0
		out[i*2] = 0.5*math.Sin(2*math.Pi*fL*t) + 0.18*math.Sin(2*math.Pi*3.0*fL*t)
		out[i*2+1] = 0.5*math.Sin(2*math.Pi*fR*t) + 0.15*math.Sin(2*math.Pi*2.0*fR*t)
	}
	return out
}

func scaleSignal(in []float64, gain float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * gain
	}
	return out
}

func writeOggPage(w io.Writer, pageSeq uint32, headerType byte, granulePos int64, serial uint32, data []byte) error {
	var page bytes.Buffer
	page.WriteString("OggS")
	page.WriteByte(0)
	page.WriteByte(headerType)
	if err := binary.Write(&page, binary.LittleEndian, granulePos); err != nil {
		return err
	}
	if err := binary.Write(&page, binary.LittleEndian, serial); err != nil {
		return err
	}
	if err := binary.Write(&page, binary.LittleEndian, pageSeq); err != nil {
		return err
	}
	if err := binary.Write(&page, binary.LittleEndian, uint32(0)); err != nil {
		return err
	}
	page.WriteByte(byte(1))
	if len(data) > 255 {
		numSegs := (len(data) + 254) / 255
		page.Truncate(page.Len() - 1)
		page.WriteByte(byte(numSegs))
		remaining := len(data)
		for remaining > 0 {
			segLen := remaining
			if segLen > 255 {
				segLen = 255
			}
			page.WriteByte(byte(segLen))
			remaining -= segLen
		}
	} else {
		page.WriteByte(byte(len(data)))
	}
	page.Write(data)

	pageData := page.Bytes()
	crc := computeOggCRC(pageData)
	pageData[22] = byte(crc)
	pageData[23] = byte(crc >> 8)
	pageData[24] = byte(crc >> 16)
	pageData[25] = byte(crc >> 24)
	_, err := w.Write(pageData)
	return err
}

func writeOggOpus(w io.Writer, packets [][]byte, sampleRate, channels int) error {
	serial := uint32(0x12345678)
	pageSeq := uint32(0)

	var opusHead bytes.Buffer
	opusHead.WriteString("OpusHead")
	opusHead.WriteByte(1)
	opusHead.WriteByte(byte(channels))
	if err := binary.Write(&opusHead, binary.LittleEndian, uint16(312)); err != nil {
		return err
	}
	if err := binary.Write(&opusHead, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(&opusHead, binary.LittleEndian, int16(0)); err != nil {
		return err
	}
	opusHead.WriteByte(0)

	if err := writeOggPage(w, pageSeq, 0x02, 0, serial, opusHead.Bytes()); err != nil {
		return err
	}
	pageSeq++

	var opusTags bytes.Buffer
	opusTags.WriteString("OpusTags")
	vendorStr := "gopus"
	if err := binary.Write(&opusTags, binary.LittleEndian, uint32(len(vendorStr))); err != nil {
		return err
	}
	opusTags.WriteString(vendorStr)
	if err := binary.Write(&opusTags, binary.LittleEndian, uint32(0)); err != nil {
		return err
	}
	if err := writeOggPage(w, pageSeq, 0x00, 0, serial, opusTags.Bytes()); err != nil {
		return err
	}
	pageSeq++

	granulePos := int64(0)
	for i, packet := range packets {
		packet = addCELTTOCForTest(packet, channels)
		granulePos += int64(960)
		headerType := byte(0x00)
		if i == len(packets)-1 {
			headerType = 0x04
		}
		if err := writeOggPage(w, pageSeq, headerType, granulePos, serial, packet); err != nil {
			return err
		}
		pageSeq++
	}
	return nil
}

func addCELTTOCForTest(packet []byte, channels int) []byte {
	toc := byte(0xF8)
	if channels == 2 {
		toc = 0xFC
	}
	out := make([]byte, len(packet)+1)
	out[0] = toc
	copy(out[1:], packet)
	return out
}

func decodeWithOpusdec(opusdecPath string, oggData []byte) ([]float32, error) {
	in, err := os.CreateTemp("", "gopus-crossval-*.opus")
	if err != nil {
		return nil, err
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(oggData); err != nil {
		_ = in.Close()
		return nil, err
	}
	_ = in.Close()
	_ = exec.Command("xattr", "-c", in.Name()).Run()

	out, err := os.CreateTemp("", "gopus-crossval-*.wav")
	if err != nil {
		return nil, err
	}
	_ = out.Close()
	defer os.Remove(out.Name())

	cmd := exec.Command(opusdecPath, "--float", in.Name(), out.Name())
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opusdec failed: %v (%s)", err, string(b))
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		return nil, err
	}
	samples, _, _, err := parseWAV(data)
	return samples, err
}

func parseWAV(data []byte) ([]float32, int, int, error) {
	if len(data) < 44 {
		return nil, 0, 0, fmt.Errorf("wav too short")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("invalid wav header")
	}
	offset := 12
	var audioFormat uint16
	var numChannels uint16
	var sampleRate uint32
	var bitsPerSample uint16
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if offset+8+chunkSize > len(data) {
			chunkSize = len(data) - (offset + 8)
		}
		switch chunkID {
		case "fmt ":
			if chunkSize >= 16 {
				audioFormat = binary.LittleEndian.Uint16(data[offset+8 : offset+10])
				numChannels = binary.LittleEndian.Uint16(data[offset+10 : offset+12])
				sampleRate = binary.LittleEndian.Uint32(data[offset+12 : offset+16])
				bitsPerSample = binary.LittleEndian.Uint16(data[offset+22 : offset+24])
				if audioFormat == 0xFFFE && chunkSize >= 40 {
					subFormat := binary.LittleEndian.Uint16(data[offset+32 : offset+34])
					if subFormat == 1 || subFormat == 3 {
						audioFormat = subFormat
					}
				}
			}
		case "data":
			raw := data[offset+8 : offset+8+chunkSize]
			var samples []float32
			if audioFormat == 3 && bitsPerSample == 32 {
				samples = make([]float32, len(raw)/4)
				for i := range samples {
					bits := binary.LittleEndian.Uint32(raw[i*4 : i*4+4])
					samples[i] = math.Float32frombits(bits)
				}
			} else if audioFormat == 1 && bitsPerSample == 16 {
				samples = make([]float32, len(raw)/2)
				for i := range samples {
					s := int16(binary.LittleEndian.Uint16(raw[i*2 : i*2+2]))
					samples[i] = float32(s) / 32768.0
				}
			} else {
				return nil, 0, 0, fmt.Errorf("unsupported wav format: audioFormat=%d bits=%d", audioFormat, bitsPerSample)
			}
			return samples, int(sampleRate), int(numChannels), nil
		}
		offset += 8 + chunkSize
		if chunkSize%2 != 0 {
			offset++
		}
	}
	return nil, 0, 0, fmt.Errorf("wav data chunk not found")
}

func encodeFloat32LEBase64(samples []float32) string {
	raw := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(s))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func computeOggCRC(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCLookup[((crc>>24)&0xff)^uint32(b)]
	}
	return crc
}

var oggCRCLookup [256]uint32

func init() {
	poly := uint32(0x04C11DB7)
	for i := 0; i < 256; i++ {
		crc := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
		oggCRCLookup[i] = crc
	}
}
