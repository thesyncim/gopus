//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
)

type postfilterParams struct {
	silence bool
	hasFlag bool
	octave  int
	qg      int
	period  int
	tapset  int
}

func decodePostfilterParams(packet []byte) (postfilterParams, error) {
	if len(packet) < 2 {
		return postfilterParams{}, fmt.Errorf("packet too short")
	}
	toc := gopus.ParseTOC(packet[0])
	if toc.Mode != gopus.ModeCELT {
		return postfilterParams{}, fmt.Errorf("not CELT mode")
	}
	celtData := packet[1:]
	if len(celtData) == 0 {
		return postfilterParams{}, fmt.Errorf("empty CELT payload")
	}

	rd := &rangecoding.Decoder{}
	rd.Init(celtData)
	totalBits := len(celtData) * 8
	tell := rd.Tell()

	// Silence flag: only encoded when tell==1 in encoder.
	silence := false
	if tell >= totalBits {
		return postfilterParams{silence: true}, nil
	}
	if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		return postfilterParams{silence: true}, nil
	}

	// Postfilter flag and params.
	if rd.Tell()+16 > totalBits {
		return postfilterParams{}, fmt.Errorf("insufficient bits for postfilter header")
	}

	postfilterFlag := rd.DecodeBit(1)
	if postfilterFlag == 0 {
		return postfilterParams{hasFlag: false}, nil
	}

	octave := int(rd.DecodeUniform(6))
	pitchOffset := int(rd.DecodeRawBits(uint(4 + octave)))
	period := (16 << octave) + pitchOffset - 1
	qg := int(rd.DecodeRawBits(3))

	// tapset_icdf = {2, 1, 0}
	tapsetICDF := []uint8{2, 1, 0}
	tapset := rd.DecodeICDF(tapsetICDF, 2)

	return postfilterParams{
		hasFlag: true,
		octave:  octave,
		qg:      qg,
		period:  period,
		tapset:  tapset,
	}, nil
}

func convertPCMToFloat32(pcm []int16) []float32 {
	out := make([]float32, len(pcm))
	const scale = 1.0 / 32768.0
	for i, v := range pcm {
		out[i] = float32(v) * scale
	}
	return out
}

func resolveVectorDecPath(vector string) (string, error) {
	filename := vector + ".dec"
	candidates := []string{
		filepath.Join("..", "..", "testvectors", "testdata", "opus_testvectors", filename),
		filepath.Join("..", "..", "..", "internal", "testvectors", "testdata", "opus_testvectors", filename),
		filepath.Join("testvectors", "testdata", "opus_testvectors", filename),
		filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", filename),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("vector %s not found in known testdata locations", vector)
}

func TestPostfilterParamsVsLibopusLowBitrate(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		maxFrames  = 200
	)

	vectors := []string{
		"testvector01",
		"testvector07",
		"testvector08",
		"testvector12",
	}
	bitrates := []int{12000, 16000, 24000, 32000}

	for _, vector := range vectors {
		decFile, err := resolveVectorDecPath(vector)
		if err != nil {
			t.Fatalf("resolve %s: %v", vector, err)
		}

		raw, err := readPCMFile(decFile)
		if err != nil {
			t.Fatalf("read %s: %v", decFile, err)
		}

		totalSamples := len(raw)
		frameSamples := frameSize * channels
		maxSamples := maxFrames * frameSamples
		if totalSamples > maxSamples {
			totalSamples = maxSamples
		}
		totalSamples = (totalSamples / frameSamples) * frameSamples
		if totalSamples == 0 {
			t.Fatalf("no complete frames in %s", decFile)
		}
		raw = raw[:totalSamples]

		pcm := convertPCMToFloat32(raw)

		for _, bitrate := range bitrates {
			t.Run(fmt.Sprintf("%s_%dk", vector, bitrate/1000), func(t *testing.T) {
				goEnc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationLowDelay)
				if err != nil {
					t.Fatalf("gopus encoder: %v", err)
				}
				goEnc.SetBitrate(bitrate)
				if err := goEnc.SetFrameSize(frameSize); err != nil {
					t.Fatalf("gopus frame size: %v", err)
				}
				_ = goEnc.SetSignal(gopus.SignalMusic)
				_ = goEnc.SetComplexity(10)

				libEnc, err := NewLibopusEncoder(sampleRate, channels, OpusApplicationRestrictedDelay)
				if err != nil {
					t.Fatalf("libopus encoder: %v", err)
				}
				defer libEnc.Destroy()
				libEnc.SetBitrate(bitrate)
				libEnc.SetBandwidth(OpusBandwidthFullband)
				libEnc.SetSignal(OpusSignalMusic)
				libEnc.SetComplexity(10)

				var (
					totalFrames     int
					flagMismatch    int
					periodMismatch  int
					tapsetMismatch  int
					qgMismatch      int
					silenceMismatch int
					goOn            int
					libOn           int
				)

				buf := make([]byte, 1275)
				for off := 0; off+frameSamples <= len(pcm); off += frameSamples {
					frame := pcm[off : off+frameSamples]

					n, err := goEnc.Encode(frame, buf)
					if err != nil {
						t.Fatalf("gopus encode frame %d: %v", totalFrames, err)
					}
					goPkt := make([]byte, n)
					copy(goPkt, buf[:n])

					libPkt, libLen := libEnc.EncodeFloat(frame, frameSize)
					if libLen <= 0 {
						t.Fatalf("libopus encode frame %d: len=%d", totalFrames, libLen)
					}
					libPkt = libPkt[:libLen]

					goPF, err := decodePostfilterParams(goPkt)
					if err != nil {
						t.Fatalf("gopus parse frame %d: %v", totalFrames, err)
					}
					libPF, err := decodePostfilterParams(libPkt)
					if err != nil {
						t.Fatalf("libopus parse frame %d: %v", totalFrames, err)
					}

					if goPF.silence != libPF.silence {
						silenceMismatch++
						continue
					}
					if goPF.silence {
						continue
					}

					totalFrames++
					if goPF.hasFlag {
						goOn++
					}
					if libPF.hasFlag {
						libOn++
					}
					if goPF.hasFlag != libPF.hasFlag {
						flagMismatch++
						continue
					}
					if !goPF.hasFlag {
						continue
					}

					if absInt(goPF.period-libPF.period) > 1 {
						periodMismatch++
					}
					if goPF.tapset != libPF.tapset {
						tapsetMismatch++
					}
					if absInt(goPF.qg-libPF.qg) > 1 {
						qgMismatch++
					}
				}

				if totalFrames == 0 {
					t.Fatalf("no frames encoded")
				}

				flagMismatchRate := float64(flagMismatch) / float64(totalFrames)
				periodMismatchRate := float64(periodMismatch) / float64(max(1, goOn, libOn))
				tapsetMismatchRate := float64(tapsetMismatch) / float64(max(1, goOn, libOn))
				qgMismatchRate := float64(qgMismatch) / float64(max(1, goOn, libOn))

				t.Logf("Frames=%d, postfilter on: gopus=%d libopus=%d", totalFrames, goOn, libOn)
				t.Logf("Mismatches: flag=%d (%.2f%%) period=%d (%.2f%%) tapset=%d (%.2f%%) qg=%d (%.2f%%) silence=%d",
					flagMismatch, 100*flagMismatchRate,
					periodMismatch, 100*periodMismatchRate,
					tapsetMismatch, 100*tapsetMismatchRate,
					qgMismatch, 100*qgMismatchRate,
					silenceMismatch,
				)

				if silenceMismatch != 0 {
					t.Fatalf("silence mismatch: %d frames", silenceMismatch)
				}

				const maxFlagMismatch = 0.17
				const maxParamMismatch = 0.35
				const maxTapsetMismatch = 0.90
				if flagMismatchRate > maxFlagMismatch {
					t.Fatalf("postfilter flag mismatch rate %.2f%% exceeds %.2f%%", 100*flagMismatchRate, 100*maxFlagMismatch)
				}
				if math.Max(periodMismatchRate, qgMismatchRate) > maxParamMismatch {
					t.Fatalf("postfilter param mismatch rate exceeds %.2f%%", 100*maxParamMismatch)
				}
				if tapsetMismatchRate > maxTapsetMismatch {
					t.Fatalf("postfilter tapset mismatch rate %.2f%% exceeds %.2f%%", 100*tapsetMismatchRate, 100*maxTapsetMismatch)
				}
			})
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func max(a, b, c int) int {
	if a >= b && a >= c {
		return a
	}
	if b >= a && b >= c {
		return b
	}
	return c
}
