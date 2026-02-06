package encoder

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msSaveAndDecode saves Ogg files for manual inspection.
// Also tests decoding with our internal decoder vs opusdec.
func TestSILK10msSaveAndDecode(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
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

			numFrames := 20 // Short for manual inspection
			var packets [][]byte

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := enc.Encode(pcm, tc.frameSize)
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

			// Decode packets with internal decoder
			intDec := silk.NewDecoder()
			var intPeaks []float64
			for i, pkt := range packets {
				out, err := intDec.Decode(pkt[1:], tc.silkBW, tc.frameSize, true)
				if err != nil {
					t.Logf("Internal decode frame %d: %v", i, err)
					continue
				}
				var maxAbs float64
				for _, s := range out {
					v := math.Abs(float64(s))
					if v > maxAbs {
						maxAbs = v
					}
				}
				intPeaks = append(intPeaks, maxAbs)
			}
			t.Logf("Internal decoder peaks (first 10): %v", fmtPeaks(intPeaks, 10))

			// Write Ogg and decode with opusdec
			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)

			// Also save to a temp file for manual inspection
			tmpFile, _ := os.CreateTemp("", "silk_10ms_debug_*.opus")
			tmpFile.Write(oggBuf.Bytes())
			tmpFile.Close()
			t.Logf("Saved Ogg file: %s", tmpFile.Name())

			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())
			t.Logf("opusdec decoded: %d samples", len(decoded))

			// Strip pre-skip and check peaks per frame
			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}

			var opusPeaks []float64
			frameAt48k := tc.frameSize
			for i := 0; i < len(decoded)/frameAt48k && i < numFrames; i++ {
				start := i * frameAt48k
				end := start + frameAt48k
				if end > len(decoded) {
					end = len(decoded)
				}
				frameSamples := decoded[start:end]
				var maxAbs float64
				for _, s := range frameSamples {
					v := math.Abs(float64(s))
					if v > maxAbs {
						maxAbs = v
					}
				}
				opusPeaks = append(opusPeaks, maxAbs)
			}
			t.Logf("opusdec peaks (first 10): %v", fmtPeaks(opusPeaks, 10))

			// Compare internal vs opusdec for a specific frame
			if len(intPeaks) > 5 && len(opusPeaks) > 5 {
				t.Logf("Internal peak[5]=%.4f, opusdec peak[5]=%.4f",
					intPeaks[5], opusPeaks[5])
			}

			// Try decoding with opusdec using --no-resample flag
			tmpFile2, _ := os.CreateTemp("", "silk_10ms_noretrim_*.wav")
			tmpFile2.Close()
			defer os.Remove(tmpFile2.Name())

			cmd := exec.Command(opusdec, "--no-dither", tmpFile.Name(), tmpFile2.Name())
			out, err := cmd.CombinedOutput()
			if err == nil {
				wavData, _ := os.ReadFile(tmpFile2.Name())
				decoded2 := parseWAVFloat32(wavData)
				if len(decoded2) > preSkip {
					decoded2 = decoded2[preSkip:]
				}
				t.Logf("opusdec --no-dither decoded: %d samples", len(decoded2))

				var opusPeaks2 []float64
				for i := 0; i < len(decoded2)/frameAt48k && i < numFrames; i++ {
					start := i * frameAt48k
					end := start + frameAt48k
					if end > len(decoded2) {
						end = len(decoded2)
					}
					frameSamples := decoded2[start:end]
					var maxAbs float64
					for _, s := range frameSamples {
						v := math.Abs(float64(s))
						if v > maxAbs {
							maxAbs = v
						}
					}
					opusPeaks2 = append(opusPeaks2, maxAbs)
				}
				t.Logf("opusdec --no-dither peaks (first 10): %v", fmtPeaks(opusPeaks2, 10))
			} else {
				t.Logf("opusdec --no-dither failed: %s", out)
			}

			os.Remove(tmpFile.Name())
		})
	}
}

func fmtPeaks(peaks []float64, n int) string {
	if n > len(peaks) {
		n = len(peaks)
	}
	s := "["
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ", "
		}
		s += fmtFloat(peaks[i])
	}
	if len(peaks) > n {
		s += ", ..."
	}
	return s + "]"
}

func fmtFloat(v float64) string {
	if v < 0.001 {
		return "0.0000"
	}
	return fmtF(v)
}

func fmtF(v float64) string {
	s := ""
	if v < 0 {
		s = "-"
		v = -v
	}
	whole := int(v)
	frac := int((v - float64(whole)) * 10000)
	return s + itoa(whole) + "." + pad4(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func pad4(n int) string {
	s := itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
