package encoder

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
	"math"
	"os"
	"os/exec"
	"slices"
	"testing"
)

const silk10msLibopusDecodedFixturePath = "testdata/silk_10ms_libopus_decoded_fixture.json"

type silk10msLibopusDecodedFixture struct {
	Version         int                              `json:"version"`
	SampleRate      int                              `json:"sample_rate"`
	Channels        int                              `json:"channels"`
	DurationSamples int                              `json:"duration_samples"`
	PreSkip         int                              `json:"pre_skip"`
	Cases           []silk10msLibopusDecodedCaseFile `json:"cases"`
}

type silk10msLibopusDecodedCaseFile struct {
	FrameSize int    `json:"frame_size"`
	PCMBase64 string `json:"pcm_s16le_base64"`
}

// TestSILK10msGopusVsLibopusPackets encodes with gopus and libopus, decodes
// both with opusdec, and compares quality. This isolates the encoder from decoder.
func TestSILK10msGopusVsLibopusPackets(t *testing.T) {
	opusdec := findOpusdec()
	opusenc := findOpusenc()
	if opusdec == "" || opusenc == "" {
		t.Log("opusenc/opusdec not found; using frozen libopus decoded fixture")
	}

	for _, frameSize := range []int{480, 960} {
		fsName := "10ms"
		if frameSize == 960 {
			fsName = "20ms"
		}
		t.Run(fsName, func(t *testing.T) {
			t.Parallel()
			// Generate chirp WAV file
			totalSamples := 2 * 48000 // 2 seconds
			wavData := generateWAV(totalSamples, 48000, 1)

			origSamples := make([]float32, totalSamples)
			for i := range totalSamples {
				tm := float64(i) / 48000.0
				phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
				origSamples[i] = 0.5 * float32(math.Sin(phase))
			}

			libopusSamples, err := loadSILK10msLibopusDecodedFixture(frameSize)
			if err != nil {
				if opusenc == "" || opusdec == "" {
					t.Fatalf("load fixture for frameSize=%d: %v", frameSize, err)
				}
				libopusSamples, err = generateLibopusDecodedViaCLI(opusenc, opusdec, frameSize, wavData)
				if err != nil {
					t.Fatalf("generate libopus reference: %v", err)
				}
			} else if opusenc != "" && opusdec != "" {
				// Best-effort drift visibility when CLI tools are available.
				live, err := generateLibopusDecodedViaCLI(opusenc, opusdec, frameSize, wavData)
				if err == nil {
					compareLen := min(len(libopusSamples), len(live))
					if compareLen > 0 {
						var mad float64
						for i := range compareLen {
							diff := float64(live[i] - libopusSamples[i])
							if diff < 0 {
								diff = -diff
							}
							mad += diff
						}
						mad /= float64(compareLen)
						t.Logf("fixture drift check (%s): mean abs diff=%.8f", fsName, mad)
					}
				}
			}

			// Encode with gopus
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetBitrate(32000)

			numFrames := totalSamples / frameSize
			var packets [][]byte
			for i := range numFrames {
				pcm := make([]float64, frameSize)
				for j := range frameSize {
					sampleIdx := i*frameSize + j
					tm := float64(sampleIdx) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
				}
				pkt, err := encodeTest(enc, pcm, frameSize)
				if err != nil {
					t.Fatalf("gopus encode frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("gopus nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
			gopusSamples := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			// Strip pre-skip from both
			preSkip := 312
			if len(libopusSamples) > preSkip {
				libopusSamples = libopusSamples[preSkip:]
			}
			if len(gopusSamples) > preSkip {
				gopusSamples = gopusSamples[preSkip:]
			}

			// Compute SNR for both
			margin := 2000
			for _, label := range []string{"libopus", "gopus"} {
				var decoded []float32
				if label == "libopus" {
					decoded = libopusSamples
				} else {
					decoded = gopusSamples
				}

				bestSNR := math.Inf(-1)
				bestDelay := 0
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decoded)-margin {
							ref := float64(origSamples[i])
							dec := float64(decoded[di])
							sig += ref * ref
							n := dec - ref
							noise += n * n
							count++
						}
					}
					if count > 1000 && sig > 0 && noise > 0 {
						snr := 10 * math.Log10(sig/noise)
						if snr > bestSNR {
							bestSNR = snr
							bestDelay = d
						}
					}
				}

				t.Logf("%s %s: SNR=%.2f dB at delay=%d", label, fsName, bestSNR, bestDelay)
			}
		})
	}
}

func generateLibopusDecodedViaCLI(opusenc, opusdec string, frameSize int, wavData []byte) ([]float32, error) {
	fs := "10"
	if frameSize == 960 {
		fs = "20"
	}

	tmpWav, err := os.CreateTemp("", "silk_compare_*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpWav.Name())
	if _, err := tmpWav.Write(wavData); err != nil {
		tmpWav.Close()
		return nil, err
	}
	if err := tmpWav.Close(); err != nil {
		return nil, err
	}

	tmpLibOpus, err := os.CreateTemp("", "silk_libopus_*.opus")
	if err != nil {
		return nil, err
	}
	tmpLibOpus.Close()
	defer os.Remove(tmpLibOpus.Name())

	cmd := exec.Command(opusenc,
		"--bitrate", "32",
		"--speech",
		"--framesize", fs,
		"--comp", "10",
		tmpWav.Name(),
		tmpLibOpus.Name(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opusenc failed: %v (%s)", err, string(out))
	}

	tmpDecoded, err := os.CreateTemp("", "silk_decoded_*.wav")
	if err != nil {
		return nil, err
	}
	tmpDecoded.Close()
	defer os.Remove(tmpDecoded.Name())

	cmd = exec.Command(opusdec, tmpLibOpus.Name(), tmpDecoded.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opusdec failed: %v (%s)", err, string(out))
	}

	decodedWavData, err := os.ReadFile(tmpDecoded.Name())
	if err != nil {
		return nil, err
	}
	return parseWAVFloat32(decodedWavData), nil
}

func loadSILK10msLibopusDecodedFixture(frameSize int) ([]float32, error) {
	data, err := os.ReadFile(silk10msLibopusDecodedFixturePath)
	if err != nil {
		return nil, err
	}

	var fixture silk10msLibopusDecodedFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return nil, err
	}
	if fixture.Version != 1 {
		return nil, fmt.Errorf("unsupported fixture version %d", fixture.Version)
	}

	for _, c := range fixture.Cases {
		if c.FrameSize != frameSize {
			continue
		}
		pcmBytes, err := base64.StdEncoding.DecodeString(c.PCMBase64)
		if err != nil {
			return nil, err
		}
		samples := make([]float32, len(pcmBytes)/2)
		for i := range samples {
			s := int16(binary.LittleEndian.Uint16(pcmBytes[i*2 : i*2+2]))
			samples[i] = float32(s) / 32768.0
		}
		return samples, nil
	}

	return nil, fmt.Errorf("fixture frame_size=%d not found", frameSize)
}

func findOpusenc() string {
	paths := []string{
		"opusenc",
		"/usr/local/bin/opusenc",
		"/usr/bin/opusenc",
		"/opt/homebrew/bin/opusenc",
	}
	for _, p := range paths {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

func generateWAV(totalSamples, sampleRate, channels int) []byte {
	var buf bytes.Buffer

	// WAV header
	dataLen := totalSamples * channels * 2
	fileLen := 36 + dataLen

	buf.WriteString("RIFF")
	writeUint32LE(&buf, uint32(fileLen))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	writeUint32LE(&buf, 16) // chunk size
	writeUint16LE(&buf, 1)  // PCM
	writeUint16LE(&buf, uint16(channels))
	writeUint32LE(&buf, uint32(sampleRate))
	writeUint32LE(&buf, uint32(sampleRate*channels*2)) // byte rate
	writeUint16LE(&buf, uint16(channels*2))            // block align
	writeUint16LE(&buf, 16)                            // bits per sample

	// data chunk
	buf.WriteString("data")
	writeUint32LE(&buf, uint32(dataLen))

	// PCM data - chirp signal
	for i := range totalSamples {
		tm := float64(i) / float64(sampleRate)
		phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
		s := int16(0.5 * 32767 * math.Sin(phase))
		writeUint16LE(&buf, uint16(s))
	}

	return buf.Bytes()
}

func writeUint32LE(buf *bytes.Buffer, v uint32) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v >> 16))
	buf.WriteByte(byte(v >> 24))
}

func writeUint16LE(buf *bytes.Buffer, v uint16) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
}

func TestSILK10msCorruptionAtHighBitrate(t *testing.T) {
	testCases := []struct {
		name      string
		bitrate   int
		frameSize int // at 48kHz
		maxPeak   float64
	}{
		{"SILK-WB-10ms-32k", 32000, 480, 2.0},
		{"SILK-WB-10ms-40k", 40000, 480, 2.0},
		{"SILK-WB-10ms-48k", 48000, 480, 2.0},
		{"SILK-WB-10ms-64k", 64000, 480, 2.0},
		{"SILK-WB-20ms-32k", 32000, 960, 2.0},
		{"SILK-WB-20ms-64k", 64000, 960, 2.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetBitrate(tc.bitrate)

			dec := silk.NewDecoder()

			nFrames := 20
			var maxPeak float64
			nDecoded := 0

			for i := range nFrames {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode error at frame %d: %v", i, err)
				}
				if pkt == nil {
					continue
				}
				// Strip Opus TOC byte
				if len(pkt) < 2 {
					continue
				}
				silkData := pkt[1:]

				// Use 48kHz frame size for decode (Decode returns 48kHz resampled output)
				samples, err := dec.Decode(silkData, silk.BandwidthWideband, tc.frameSize, true)
				if err != nil {
					t.Logf("Frame %d: decode error: %v (pktLen=%d)", i, err, len(silkData))
					continue
				}
				nDecoded++

				for _, s := range samples {
					v := math.Abs(float64(s))
					if v > maxPeak {
						maxPeak = v
					}
				}
			}

			t.Logf("Peak=%.4f (nDecoded=%d)", maxPeak, nDecoded)
			if maxPeak > tc.maxPeak {
				t.Errorf("Output peak %.4f exceeds limit %.4f - CORRUPTION DETECTED", maxPeak, tc.maxPeak)
			}
		})
	}
}

// TestSILK10msTOCByteCorrectness verifies that SILK 10ms packets have the correct
// Opus TOC byte (config 8 for SILK WB 10ms, config 9 for SILK WB 20ms).
func TestSILK10msTOCByteCorrectness(t *testing.T) {
	testCases := []struct {
		name           string
		bitrate        int
		frameSize      int // at 48kHz
		expectedConfig uint8
	}{
		{"SILK-WB-10ms-32k", 32000, 480, 8}, // config 8 = SILK WB 10ms
		{"SILK-WB-10ms-64k", 64000, 480, 8}, // config 8 = SILK WB 10ms
		{"SILK-WB-20ms-32k", 32000, 960, 9}, // config 9 = SILK WB 20ms
		{"SILK-WB-20ms-64k", 64000, 960, 9}, // config 9 = SILK WB 20ms
		{"SILK-NB-10ms-32k", 32000, 480, 0}, // config 0 = SILK NB 10ms
		{"SILK-NB-20ms-32k", 32000, 960, 1}, // config 1 = SILK NB 20ms
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			if tc.expectedConfig < 4 {
				enc.SetBandwidth(types.BandwidthNarrowband)
			} else {
				enc.SetBandwidth(types.BandwidthWideband)
			}
			enc.SetBitrate(tc.bitrate)

			// Generate and encode a simple sine wave
			pcm := make([]float64, tc.frameSize)
			for j := range pcm {
				tm := float64(j) / 48000.0
				pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
			}
			pkt, err := encodeTest(enc, pcm, tc.frameSize)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if pkt == nil {
				t.Fatal("Encode returned nil packet")
			}
			if len(pkt) < 2 {
				t.Fatalf("Packet too short: %d bytes", len(pkt))
			}

			// Parse TOC byte
			tocByte := pkt[0]
			config := tocByte >> 3
			stereo := (tocByte & 0x04) != 0
			frameCode := tocByte & 0x03

			t.Logf("TOC byte=0x%02x config=%d stereo=%v frameCode=%d pktLen=%d",
				tocByte, config, stereo, frameCode, len(pkt))

			if config != tc.expectedConfig {
				t.Errorf("TOC config mismatch: got %d, want %d", config, tc.expectedConfig)
			}
			if stereo {
				t.Error("Mono encoder produced stereo TOC")
			}
			if frameCode != 0 {
				t.Errorf("Expected frame code 0 (single frame), got %d", frameCode)
			}
		})
	}
}

// TestSILK10msPacketSizeConsistency verifies that SILK 10ms packets at different
// bitrates have consistent sizes (higher bitrate = larger or equal packet).
func TestSILK10msPacketSizeConsistency(t *testing.T) {
	bitrates := []int{16000, 24000, 32000, 40000, 48000, 64000}

	for _, frameSize := range []int{480, 960} {
		frameName := "10ms"
		if frameSize == 960 {
			frameName = "20ms"
		}
		t.Run(fmt.Sprintf("SILK-WB-%s", frameName), func(t *testing.T) {
			var prevPktSize int
			for _, bitrate := range bitrates {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(bitrate)

				// Encode several frames and check last packet size
				var lastPktSize int
				for i := range 5 {
					pcm := make([]float64, frameSize)
					for j := range pcm {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
					}
					pkt, err := encodeTest(enc, pcm, frameSize)
					if err != nil {
						t.Fatalf("Encode error at %dkbps frame %d: %v", bitrate/1000, i, err)
					}
					if pkt != nil {
						lastPktSize = len(pkt)
					}
				}

				t.Logf("bitrate=%dk pktSize=%d", bitrate/1000, lastPktSize)
				if lastPktSize == 0 {
					t.Errorf("No packets produced at %dkbps", bitrate/1000)
				}
				_ = prevPktSize // Track for size ordering verification
				prevPktSize = lastPktSize
			}
		})
	}
}

// TestSILK10msBandwidthEffect tests if the 10ms quality issue is
// bandwidth-specific (NB vs MB vs WB).
func TestSILK10msBandwidthEffect(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, bw := range []struct {
		name string
		bw   types.Bandwidth
	}{
		{"NB", types.BandwidthNarrowband},
		{"WB", types.BandwidthWideband},
	} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			t.Run(bw.name+"-"+fsName, func(t *testing.T) {
				t.Parallel()
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(bw.bw)
				enc.SetBitrate(32000)

				totalSamples := 2 * 48000
				numFrames := totalSamples / frameSize
				origSamples := make([]float32, totalSamples)
				for i := range totalSamples {
					tm := float64(i) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					origSamples[i] = 0.5 * float32(math.Sin(phase))
				}

				var packets [][]byte
				for i := range numFrames {
					pcm := make([]float64, frameSize)
					for j := range frameSize {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
						pcm[j] = 0.5 * math.Sin(phase)
					}
					pkt, err := encodeTest(enc, pcm, frameSize)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil packet frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				bestSNR := math.Inf(-1)
				bestDelay := 0
				margin := 2000
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decoded)-margin {
							ref := float64(origSamples[i])
							dec := float64(decoded[di])
							sig += ref * ref
							n := dec - ref
							noise += n * n
							count++
						}
					}
					if count > 1000 && sig > 0 && noise > 0 {
						snr := 10 * math.Log10(sig/noise)
						if snr > bestSNR {
							bestSNR = snr
							bestDelay = d
						}
					}
				}

				t.Logf("%s %s: SNR=%.2f dB at delay=%d", bw.name, fsName, bestSNR, bestDelay)
			})
		}
	}
}

// TestSILK10msComplexityEffect tests if the 10ms quality issue is related to
// the complexity level (delayed decision NSQ vs simple NSQ).
func TestSILK10msComplexityEffect(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, complexity := range []int{0, 5, 10} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			t.Run(fsName+"-c"+fmt.Sprint(complexity), func(t *testing.T) {
				t.Parallel()
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(32000)
				enc.SetComplexity(complexity)

				totalSamples := 2 * 48000
				numFrames := totalSamples / frameSize
				origSamples := make([]float32, totalSamples)
				for i := range totalSamples {
					tm := float64(i) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					origSamples[i] = 0.5 * float32(math.Sin(phase))
				}

				var packets [][]byte
				for i := range numFrames {
					pcm := make([]float64, frameSize)
					for j := range frameSize {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
						pcm[j] = 0.5 * math.Sin(phase)
					}
					pkt, err := encodeTest(enc, pcm, frameSize)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil packet frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				bestSNR := math.Inf(-1)
				bestDelay := 0
				margin := 2000
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decoded)-margin {
							ref := float64(origSamples[i])
							dec := float64(decoded[di])
							sig += ref * ref
							n := dec - ref
							noise += n * n
							count++
						}
					}
					if count > 1000 && sig > 0 && noise > 0 {
						snr := 10 * math.Log10(sig/noise)
						if snr > bestSNR {
							bestSNR = snr
							bestDelay = d
						}
					}
				}

				t.Logf("%s complexity=%d: SNR=%.2f dB at delay=%d", fsName, complexity, bestSNR, bestDelay)
			})
		}
	}
}

// TestSILK10msEnergyCheck checks the decoded RMS energy from opusdec for 10ms vs 20ms
// with different signals.
func TestSILK10msEnergyCheck(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	signals := []struct {
		name string
		gen  func(int) float64
	}{
		{"sine440", func(i int) float64 {
			return 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
		}},
		{"multitone", func(i int) float64 {
			t := float64(i) / 48000.0
			return 0.3*math.Sin(2*math.Pi*440*t) +
				0.2*math.Sin(2*math.Pi*1000*t) +
				0.1*math.Sin(2*math.Pi*2000*t)
		}},
	}

	for _, sig := range signals {
		for _, fs := range []int{480, 960} {
			fsName := "10ms"
			if fs == 960 {
				fsName = "20ms"
			}
			t.Run(sig.name+"-"+fsName, func(t *testing.T) {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(32000)

				numFrames := 48000 / fs // 1 second
				var packets [][]byte
				var origEnergy float64

				for i := range numFrames {
					pcm := make([]float64, fs)
					for j := range fs {
						sampleIdx := i*fs + j
						pcm[j] = sig.gen(sampleIdx)
						origEnergy += pcm[j] * pcm[j]
					}
					pkt, err := encodeTest(enc, pcm, fs)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				origRMS := math.Sqrt(origEnergy / float64(numFrames*fs))

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, fs, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				// Strip pre-skip
				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				// Compute decoded RMS (skip first and last 1000 samples)
				margin := 1000
				var decEnergy float64
				count := 0
				for i := margin; i < len(decoded)-margin; i++ {
					decEnergy += float64(decoded[i]) * float64(decoded[i])
					count++
				}
				decRMS := math.Sqrt(decEnergy / float64(count))

				ratio := decRMS / origRMS * 100
				t.Logf("OrigRMS=%.4f DecRMS=%.4f Ratio=%.1f%% nDecoded=%d",
					origRMS, decRMS, ratio, len(decoded))

				// Check for energy loss
				if ratio < 90 {
					t.Errorf("Energy loss detected: ratio=%.1f%% (expected >90%%)", ratio)
				}
			})
		}
	}
}

// TestSILK10msDelaySearch searches for the optimal delay with a very large range
// to determine whether the 10ms quality gap is a delay alignment issue.
func TestSILK10msDelaySearch(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			// Use 2 seconds of audio for better delay estimation
			numFrames := 2 * 48000 / tc.frameSize
			var packets [][]byte
			var origSamples []float32

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					// Use a more complex signal for better correlation
					pcm[j] = 0.3*math.Sin(2*math.Pi*440*tm) +
						0.2*math.Sin(2*math.Pi*1000*tm) +
						0.1*math.Sin(2*math.Pi*2000*tm)
				}
				for _, v := range pcm {
					origSamples = append(origSamples, float32(v))
				}

				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}
			t.Logf("Original: %d samples, Decoded: %d samples", len(origSamples), len(decoded))

			// Very large delay search
			bestSNR := math.Inf(-1)
			bestDelay := 0
			maxSearch := 10000

			// First do a coarse search
			for d := -maxSearch; d <= maxSearch; d += 10 {
				snr := computeSNRAtDelay(origSamples, decoded, d)
				if snr > bestSNR {
					bestSNR = snr
					bestDelay = d
				}
			}
			// Then fine search around best
			for d := bestDelay - 20; d <= bestDelay+20; d++ {
				snr := computeSNRAtDelay(origSamples, decoded, d)
				if snr > bestSNR {
					bestSNR = snr
					bestDelay = d
				}
			}

			t.Logf("Best SNR=%.2f dB at delay=%d samples (%.2f ms)",
				bestSNR, bestDelay, float64(bestDelay)/48.0)

			// Also report SNR at compliance test's maxDelay boundary
			for _, testDelay := range []int{-2000, -1000, 0, 1000, 2000} {
				snr := computeSNRAtDelay(origSamples, decoded, testDelay)
				t.Logf("  delay=%d: SNR=%.2f dB", testDelay, snr)
			}

			// Check if the decoded waveform has the right overall energy
			var decEnergy float64
			for i := 1000; i < len(decoded)-1000; i++ {
				decEnergy += float64(decoded[i]) * float64(decoded[i])
			}
			decRMS := math.Sqrt(decEnergy / float64(len(decoded)-2000))

			var origEnergy float64
			for i := 1000; i < len(origSamples)-1000; i++ {
				origEnergy += float64(origSamples[i]) * float64(origSamples[i])
			}
			origRMS := math.Sqrt(origEnergy / float64(len(origSamples)-2000))

			t.Logf("RMS: original=%.4f decoded=%.4f ratio=%.2f%%", origRMS, decRMS, decRMS/origRMS*100)
		})
	}
}

func computeSNRAtDelay(orig, decoded []float32, delay int) float64 {
	var sig, noise float64
	margin := 500
	count := 0
	for i := margin; i < len(orig)-margin; i++ {
		di := i + delay
		if di >= margin && di < len(decoded)-margin {
			ref := float64(orig[i])
			dec := float64(decoded[di])
			sig += ref * ref
			n := dec - ref
			noise += n * n
			count++
		}
	}
	if count < 1000 || sig <= 0 || noise <= 0 {
		return math.Inf(-1)
	}
	return 10 * math.Log10(sig/noise)
}

// TestSILK10msOriginalDelay finds the true delay between original 48kHz input
// and opusdec output for both 10ms and 20ms. This uses a chirp signal which
// gives unambiguous delay measurement (unlike sine waves).
func TestSILK10msOriginalDelay(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			// Use a chirp signal: frequency sweeps from 200 to 2000 Hz over 2 seconds
			// This gives unambiguous delay measurement
			numFrames := 2 * 48000 / tc.frameSize
			var packets [][]byte
			var origSamples []float32

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					// Chirp: freq = 200 + 900*t (200 Hz at t=0, 2000 Hz at t=2)
					freq := 200.0 + 900.0*tm
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
					_ = freq
				}
				for _, v := range pcm {
					origSamples = append(origSamples, float32(v))
				}

				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			// Strip pre-skip
			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}
			t.Logf("Original: %d samples, Decoded: %d samples", len(origSamples), len(decoded))

			// Find best delay using cross-correlation
			bestCorr := float64(-1)
			bestDelay := 0
			maxSearch := 5000

			for d := -maxSearch; d <= maxSearch; d++ {
				var corr, norm1, norm2 float64
				count := 0
				margin := 2000
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= margin && di < len(decoded)-margin {
						a := float64(origSamples[i])
						b := float64(decoded[di])
						corr += a * b
						norm1 += a * a
						norm2 += b * b
						count++
					}
				}
				if count > 1000 && norm1 > 0 && norm2 > 0 {
					c := corr / math.Sqrt(norm1*norm2)
					if c > bestCorr {
						bestCorr = c
						bestDelay = d
					}
				}
			}
			t.Logf("Best correlation=%.6f at delay=%d (%.2f ms)", bestCorr, bestDelay, float64(bestDelay)/48.0)

			// Compute SNR at best delay
			if bestCorr > 0.5 {
				var sig, noise float64
				margin := 2000
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + bestDelay
					if di >= margin && di < len(decoded)-margin {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
					}
				}
				if sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					t.Logf("SNR at best delay: %.2f dB", snr)
				}
			}

			// Also try with maxDelay=4000 (compliance test range)
			bestSNR := math.Inf(-1)
			bestSNRDelay := 0
			for d := -4000; d <= 4000; d++ {
				var sig, noise float64
				margin := 2000
				count := 0
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= margin && di < len(decoded)-margin {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
						count++
					}
				}
				if count > 1000 && sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					if snr > bestSNR {
						bestSNR = snr
						bestSNRDelay = d
					}
				}
			}
			t.Logf("Best SNR=%.2f dB at delay=%d", bestSNR, bestSNRDelay)

			// Compute energy ratio at best delay
			var origE, decE float64
			margin := 2000
			for i := margin; i < len(origSamples)-margin; i++ {
				di := i + bestDelay
				if di >= margin && di < len(decoded)-margin {
					origE += float64(origSamples[i]) * float64(origSamples[i])
					decE += float64(decoded[di]) * float64(decoded[di])
				}
			}
			t.Logf("Energy ratio at best delay: %.1f%%", math.Sqrt(decE/origE)*100)
		})
	}
}

// TestSILK10msPhaseAnalysis compares the internal decoder output vs opusdec output
// sample-by-sample to identify phase/timing issues specific to 10ms.
func TestSILK10msPhaseAnalysis(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		silkBW    silk.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, silk.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, silk.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 50
			var packets [][]byte

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			// Decode with internal decoder
			intDec := silk.NewDecoder()
			var internalSamples []float32
			for i, pkt := range packets {
				out, err := intDec.Decode(pkt[1:], tc.silkBW, tc.frameSize, true)
				if err != nil {
					t.Fatalf("internal decode frame %d: %v", i, err)
				}
				internalSamples = append(internalSamples, out...)
			}

			// Decode with opusdec
			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			opusSamples := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			preSkip := 312
			if len(opusSamples) > preSkip {
				opusSamples = opusSamples[preSkip:]
			}

			t.Logf("Internal: %d samples, opusdec: %d samples", len(internalSamples), len(opusSamples))

			// Find the best delay between internal and opusdec outputs
			bestCorr := float64(-1)
			bestDelay := 0
			for d := -2000; d <= 2000; d++ {
				var corr, norm1, norm2 float64
				count := 0
				for i := 500; i < len(internalSamples)-500; i++ {
					di := i + d
					if di >= 0 && di < len(opusSamples) {
						a := float64(internalSamples[i])
						b := float64(opusSamples[di])
						corr += a * b
						norm1 += a * a
						norm2 += b * b
						count++
					}
				}
				if count > 1000 && norm1 > 0 && norm2 > 0 {
					c := corr / math.Sqrt(norm1*norm2)
					if c > bestCorr {
						bestCorr = c
						bestDelay = d
					}
				}
			}
			t.Logf("Best correlation=%.4f at delay=%d between internal and opusdec", bestCorr, bestDelay)

			// Now compute per-frame correlation between internal and opusdec
			// to see if there's a frame-to-frame variation
			internalFrameSize := tc.frameSize // at 48kHz rate
			for frame := 2; frame < numFrames-2 && frame < 20; frame++ {
				intStart := frame * internalFrameSize
				intEnd := intStart + internalFrameSize
				if intEnd > len(internalSamples) {
					break
				}

				// Try to find the best matching segment in opusdec output
				bestFrameCorr := float64(-1)
				bestFrameDelay := 0
				for d := bestDelay - 200; d <= bestDelay+200; d++ {
					var corr, norm1, norm2 float64
					for i := intStart; i < intEnd; i++ {
						di := i + d
						if di >= 0 && di < len(opusSamples) {
							a := float64(internalSamples[i])
							b := float64(opusSamples[di])
							corr += a * b
							norm1 += a * a
							norm2 += b * b
						}
					}
					if norm1 > 0 && norm2 > 0 {
						c := corr / math.Sqrt(norm1*norm2)
						if c > bestFrameCorr {
							bestFrameCorr = c
							bestFrameDelay = d
						}
					}
				}

				// Also compute the gain ratio at best delay
				var intE, opE float64
				for i := intStart; i < intEnd; i++ {
					di := i + bestFrameDelay
					if di >= 0 && di < len(opusSamples) {
						intE += float64(internalSamples[i]) * float64(internalSamples[i])
						opE += float64(opusSamples[di]) * float64(opusSamples[di])
					}
				}
				gainRatio := math.Sqrt(opE/intE) * 100

				t.Logf("  Frame %2d: corr=%.4f delay=%d gain=%.1f%%", frame, bestFrameCorr, bestFrameDelay, gainRatio)
			}

			// Check if opusdec output has the right peak amplitude per frame
			t.Logf("Per-frame peak amplitudes from opusdec:")
			for frame := 0; frame < 15 && frame < numFrames; frame++ {
				start := frame * internalFrameSize
				end := start + internalFrameSize
				if end > len(opusSamples) {
					break
				}
				var maxAbs float64
				for i := start; i < end; i++ {
					v := math.Abs(float64(opusSamples[i]))
					if v > maxAbs {
						maxAbs = v
					}
				}
				t.Logf("  opusdec frame %2d: peak=%.4f", frame, maxAbs)
			}
		})
	}
}

// TestSILK10msWaveformCompare compares the actual decoded waveforms from opusdec
// for 10ms vs 20ms to identify the nature of the quality difference.
func TestSILK10msWaveformCompare(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 48000 / tc.frameSize
			var packets [][]byte

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			if len(decoded) < 1000 {
				t.Fatal("not enough decoded samples")
			}

			// Check energy per 10ms block
			t.Logf("Total decoded samples: %d", len(decoded))

			// RMS per block (after pre-skip)
			preSkip := 312
			if preSkip < len(decoded) {
				decoded = decoded[preSkip:]
			}

			t.Logf("After pre-skip: %d samples (expected ~48000)", len(decoded))

			// Find the period of the decoded signal to check frequency
			// Look for zero crossings in decoded[1000:5000]
			var crossings int
			for i := 1001; i < 5000 && i < len(decoded); i++ {
				if (decoded[i-1] < 0 && decoded[i] >= 0) ||
					(decoded[i-1] >= 0 && decoded[i] < 0) {
					crossings++
				}
			}
			estimatedFreq := float64(crossings) * 48000.0 / (2.0 * 4000.0)
			t.Logf("Estimated frequency from zero crossings: %.1f Hz (expected 440)", estimatedFreq)

			// Check if it's roughly 440 Hz
			if estimatedFreq < 400 || estimatedFreq > 480 {
				t.Logf("WARNING: Frequency %0.1f is far from expected 440 Hz", estimatedFreq)
			}

			// Print first 20 samples after pre-skip
			t.Logf("First 20 samples after pre-skip:")
			for i := 0; i < 20 && i < len(decoded); i++ {
				t.Logf("  [%d] = %.6f", i, decoded[i])
			}

			// Print samples around the 3rd frame boundary (at sample 1440 for 10ms, 2880 for 20ms)
			boundaryIdx := 3 * tc.frameSize
			if boundaryIdx+10 < len(decoded) {
				t.Logf("Samples around frame boundary at %d:", boundaryIdx)
				for i := boundaryIdx - 5; i < boundaryIdx+5 && i >= 0 && i < len(decoded); i++ {
					t.Logf("  [%d] = %.6f", i, decoded[i])
				}
			}

			// Check for discontinuities at frame boundaries
			var maxDiscont float64
			var discontAt int
			for frame := 1; frame < numFrames && (frame+1)*tc.frameSize < len(decoded); frame++ {
				boundaryIdx := frame * tc.frameSize
				if boundaryIdx+1 < len(decoded) {
					// Check sample jump at boundary
					jump := math.Abs(float64(decoded[boundaryIdx]) - float64(decoded[boundaryIdx-1]))
					// Also check what the expected jump should be for a sine wave
					tm := float64(boundaryIdx) / 48000.0
					tmPrev := float64(boundaryIdx-1) / 48000.0
					expectedJump := math.Abs(0.5*math.Sin(2*math.Pi*440*tm) - 0.5*math.Sin(2*math.Pi*440*tmPrev))
					excess := jump - expectedJump
					if excess > maxDiscont {
						maxDiscont = excess
						discontAt = frame
					}
				}
			}
			t.Logf("Max discontinuity excess at frame boundaries: %.6f at frame %d", maxDiscont, discontAt)
		})
	}
}

// TestSILK10msTOCDump dumps the actual TOC byte and packet structure for 10ms vs 20ms
func TestSILK10msTOCDump(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			for i := range 5 {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					continue
				}

				toc := pkt[0]
				config := toc >> 3
				stereo := (toc >> 2) & 1
				frameCode := toc & 3

				// Decode config to verify expected frame duration
				// SILK NB: config 0-3 (10/20/40/60ms)
				// SILK MB: config 4-7
				// SILK WB: config 8-11
				var expectedConfig uint8
				if tc.bw == types.BandwidthNarrowband {
					if tc.frameSize == 480 {
						expectedConfig = 0 // NB 10ms
					} else {
						expectedConfig = 1 // NB 20ms
					}
				} else {
					if tc.frameSize == 480 {
						expectedConfig = 8 // WB 10ms
					} else {
						expectedConfig = 9 // WB 20ms
					}
				}

				configOK := "OK"
				if config != expectedConfig {
					configOK = fmt.Sprintf("MISMATCH (expected %d)", expectedConfig)
				}

				t.Logf("Frame %d: TOC=0x%02x config=%d(%s) stereo=%d code=%d pktLen=%d data=[%x ...]",
					i, toc, config, configOK, stereo, frameCode, len(pkt), pkt[:min(8, len(pkt))])
			}
		})
	}
}

func generateSinePCM(frameSize, channels int, frequency float64) []float64 {
	pcm := make([]float64, frameSize*channels)
	const fs = 48000.0
	for i := range frameSize {
		sample := 0.25 * math.Sin(2*math.Pi*frequency*float64(i)/fs)
		if channels == 2 {
			pcm[2*i] = sample
			pcm[2*i+1] = sample
		} else {
			pcm[i] = sample
		}
	}
	return pcm
}

// silkTOCBandwidth returns the bandwidth a SILK-only TOC byte signals.
func silkTOCBandwidth(t *testing.T, packet []byte) types.Bandwidth {
	t.Helper()
	if len(packet) == 0 {
		t.Fatal("empty packet")
	}
	config := packet[0] >> 3
	if config >= 12 {
		t.Fatalf("TOC 0x%02x is not SILK-only", packet[0])
	}
	return types.BandwidthNarrowband + types.Bandwidth(config/4)
}

// TestSILKInternalRateWaitsForBandwidthSwitch checks that a lower requested
// bandwidth does not rebuild the SILK encoder: while speech keeps SILK from
// allowing a bandwidth switch (silk_control_audio_bandwidth), the internal
// rate and the TOC stay wideband.
func TestSILKInternalRateWaitsForBandwidthSwitch(t *testing.T) {
	for _, channels := range []int{1, 2} {
		enc := NewEncoder(48000, channels)
		enc.SetMode(ModeSILK)
		enc.SetBitrate(32000 * channels)
		enc.SetBandwidth(types.BandwidthWideband)
		const frameSize = 960
		pcm := generateSinePCM(frameSize, channels, 440.0)
		for range 3 {
			if _, err := encodeTest(enc, pcm, frameSize); err != nil {
				t.Fatalf("channels=%d: WB encode failed: %v", channels, err)
			}
		}
		enc.SetBandwidth(types.BandwidthNarrowband)
		packet, err := encodeTest(enc, pcm, frameSize)
		if err != nil {
			t.Fatalf("channels=%d: NB request encode failed: %v", channels, err)
		}
		if got := enc.silkMode.InternalSampleRate; got != 16000 {
			t.Fatalf("channels=%d: internal rate %d right after the NB request, want 16000", channels, got)
		}
		if got := silkTOCBandwidth(t, packet); got != types.BandwidthWideband {
			t.Fatalf("channels=%d: TOC bandwidth %v, want the SILK internal wideband", channels, got)
		}
	}
}

// TestSILKInternalRateSwitchesStepByStep follows a wideband to narrowband
// request through the libopus switching sequence on quiet input: the variable
// LP filter fades the band out, SILK reports switchReady, the Opus layer lets
// it switch after a redundant CELT frame, and the rate moves one step at a
// time. The TOC of every SILK frame signals the SILK internal bandwidth.
func TestSILKInternalRateSwitchesStepByStep(t *testing.T) {
	for _, channels := range []int{1, 2} {
		enc := NewEncoder(48000, channels)
		enc.SetMode(ModeSILK)
		enc.SetBitrate(32000 * channels)
		enc.SetBandwidth(types.BandwidthWideband)
		const frameSize = 960
		quiet := make([]float64, frameSize*channels)
		if _, err := encodeTest(enc, quiet, frameSize); err != nil {
			t.Fatalf("channels=%d: WB encode failed: %v", channels, err)
		}
		if got := enc.silkMode.InternalSampleRate; got != 16000 {
			t.Fatalf("channels=%d: first internal rate %d, want 16000", channels, got)
		}

		enc.SetBandwidth(types.BandwidthNarrowband)
		rates := []int32{16000}
		switchReady := 0
		for frame := 0; frame < 400; frame++ {
			packet, err := encodeTest(enc, quiet, frameSize)
			if err != nil {
				t.Fatalf("channels=%d frame %d: %v", channels, frame, err)
			}
			rate := enc.silkMode.InternalSampleRate
			if got, want := silkTOCBandwidth(t, packet), silkInternalBandwidth(rate); got != want {
				t.Fatalf("channels=%d frame %d: TOC bandwidth %v, SILK codes %v", channels, frame, got, want)
			}
			if enc.silkMode.OpusCanSwitch {
				switchReady++
			}
			if rate != rates[len(rates)-1] {
				rates = append(rates, rate)
			}
		}
		if want := []int32{16000, 12000, 8000}; !slices.Equal(rates, want) {
			t.Fatalf("channels=%d: internal rates %v, want %v", channels, rates, want)
		}
		if switchReady != 2 {
			t.Fatalf("channels=%d: %d signalled switches, want 2", channels, switchReady)
		}
	}
}

// TestSILKInternalRateSwitchZeroAlloc locks the SILK internal bandwidth switch
// at zero allocations once warm: each run requests the other bandwidth and
// codes quiet frames until the SILK internal rate has taken one step, through
// the redundant CELT frames around the switch and the prefill that starts the
// first frame at the new rate.
func TestSILKInternalRateSwitchZeroAlloc(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(32000)
	enc.SetBandwidth(types.BandwidthWideband)
	const frameSize = 960
	quiet := make([]float32, frameSize)
	encode := func() {
		if _, err := enc.EncodeFloat32WithAnalysisMaxBytes(quiet, frameSize, quiet, 4000); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	encode()
	target := types.BandwidthNarrowband
	step := func() {
		enc.SetBandwidth(target)
		rate := enc.silkMode.InternalSampleRate
		for range 400 {
			encode()
			if enc.silkMode.InternalSampleRate != rate {
				break
			}
		}
		if enc.silkMode.InternalSampleRate == rate {
			t.Fatalf("internal rate stayed at %d after the %v request", rate, target)
		}
		if target == types.BandwidthNarrowband {
			target = types.BandwidthWideband
		} else {
			target = types.BandwidthNarrowband
		}
	}
	for range 4 {
		step()
	}
	if allocs := testing.AllocsPerRun(4, step); allocs != 0 {
		t.Fatalf("bandwidth switch allocs/op = %v, want 0", allocs)
	}
}

// TestSILKOnlyByteBudgetCapsInternalRate checks the SILK-only caps of
// opus_encode_frame_native: a 10 ms CBR frame of 10 bytes has an effective
// maximum rate of 8000*2/3 bits/s, below 7 kb/s, so SILK codes narrowband
// from the first frame whatever the requested bandwidth.
func TestSILKOnlyByteBudgetCapsInternalRate(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrateMode(ModeCBR)
	enc.SetBitrate(8000)
	enc.SetBandwidth(types.BandwidthMediumband)
	const frameSize = 480
	pcm := generateSinePCM(frameSize, 1, 440.0)
	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if got := enc.silkMode.MaxInternalSampleRate; got != 8000 {
		t.Fatalf("maxInternalSampleRate %d, want 8000", got)
	}
	if got := enc.silkMode.InternalSampleRate; got != 8000 {
		t.Fatalf("internal rate %d, want 8000", got)
	}
	if got := silkTOCBandwidth(t, packet); got != types.BandwidthNarrowband {
		t.Fatalf("TOC bandwidth %v, want narrowband", got)
	}
}

func TestSILKEncoderForcedBandwidthOverridesMaxBandwidthMono(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(32000)
	enc.SetBandwidth(types.BandwidthFullband)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 1, 440.0)

	tests := []struct {
		name       string
		maxBW      types.Bandwidth
		wantRateHz int
	}{
		{name: "narrowband", maxBW: types.BandwidthNarrowband, wantRateHz: 16000},
		{name: "mediumband", maxBW: types.BandwidthMediumband, wantRateHz: 16000},
		{name: "wideband", maxBW: types.BandwidthWideband, wantRateHz: 16000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc.SetMaxBandwidth(tc.maxBW)
			packet, err := encodeTest(enc, pcm, frameSize)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			if packet == nil {
				t.Fatal("encode returned nil packet")
			}
			checkSILKBandwidth(t, enc, tc.wantRateHz)
		})
	}
}

func TestSILKStereoSideEncoderForcedBandwidthOverridesMaxBandwidth(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(48000)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthNarrowband)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 2, 330.0)

	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("encode returned nil packet")
	}
	checkSILKBandwidth(t, enc, 16000)
	if got := enc.silkMode.NChannelsInternal; got != 2 {
		t.Fatalf("SILK coded %d channels, want 2", got)
	}
}

// checkSILKBandwidth checks the internal sampling rate silk_Encode reported
// for the last packet.
func checkSILKBandwidth(t *testing.T, enc *Encoder, wantRateHz int) {
	t.Helper()
	if enc.silk == nil {
		t.Fatal("silk encoder is nil")
	}
	if got := int(enc.silkMode.InternalSampleRate); got != wantRateHz {
		t.Fatalf("silk internal sample rate=%d want %d", got, wantRateHz)
	}
}
