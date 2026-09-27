//go:build gopus_osce

package gopus

// TestOSCEEndToEndSampleParity compares every float32 PCM bit from the public
// LACE/NoLACE/BWE decoder with the scalar OSCE-enabled libopus build, using
// identical packets, model weights, and controls. The quality gate also runs.

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

// libopusOSCEDecodeSingleHelper caches the lazily-built oracle binary.
var libopusOSCEDecodeSingleHelper libopustest.HelperCache

// getLibopusOSCEDecodeSingleHelperPath lazily builds the OSCE decode oracle.
func getLibopusOSCEDecodeSingleHelperPath() (string, error) {
	return cachedLibopusOSCEHelperPath(
		&libopusOSCEDecodeSingleHelper,
		"libopus_osce_decode_single.c",
		"gopus_libopus_osce_decode_single",
		false, // no internal libopus header access needed
	)
}

// runLibopusOSCEDecodeSingle invokes the libopus OSCE decode oracle on
// `packets` and returns the flat float32 PCM from libopus.
// The helper uses the LACE/NoLACE/BWE weights that are compiled statically
// into the OSCE-enabled libopus build; no runtime blob loading is needed.
func runLibopusOSCEDecodeSingle(
	binPath string,
	sampleRate, channels, frameSize, complexity int,
	enableBWE bool,
	packets [][]byte,
) ([]float32, error) {
	var bweFlag uint32
	if enableBWE {
		bweFlag = 1
	}

	// Build the binary input payload.
	var buf []byte
	put32 := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		buf = append(buf, b[:]...)
	}
	putBytes := func(b []byte) { buf = append(buf, b...) }

	putBytes([]byte("GSOI"))
	put32(1) // version
	put32(uint32(sampleRate))
	put32(uint32(channels))
	put32(uint32(frameSize))
	put32(uint32(complexity))
	put32(bweFlag)
	put32(uint32(len(packets)))
	for _, pkt := range packets {
		put32(uint32(len(pkt)))
		putBytes(pkt)
	}

	out, err := libopustest.RunHelper(binPath, buf)
	if err != nil {
		return nil, fmt.Errorf("libopus OSCE decode oracle: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE decode", "GSOO", out)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("OSCE decode oracle version=%d want 1", version)
	}
	totalSamples := int(reader.U32())
	if reader.Err() != nil {
		return nil, reader.Err()
	}
	reader.ExpectRemaining(totalSamples * 4)
	pcm := make([]float32, totalSamples)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return pcm, nil
}

func TestOSCEEndToEndSampleParity(t *testing.T) {
	libopustest.RequireOracle(t)

	binPath, err := getLibopusOSCEDecodeSingleHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE decode single", err)
	}

	laceBlob := requireLibopusOSCELACEModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)

	// Merged model blob for gopus: core + LACE + BWE.
	mergedAll := make([]byte, 0, len(coreBlob)+len(laceBlob)+len(bweBlob))
	mergedAll = append(mergedAll, coreBlob...)
	mergedAll = append(mergedAll, laceBlob...)
	mergedAll = append(mergedAll, bweBlob...)

	// Merged blob without BWE (for LACE/NoLACE-only subtests).
	mergedLACE := make([]byte, 0, len(coreBlob)+len(laceBlob))
	mergedLACE = append(mergedLACE, coreBlob...)
	mergedLACE = append(mergedLACE, laceBlob...)

	const (
		frameSize  = 960 // 20 ms @ 48 kHz
		numPackets = 24  // fade-in and recurrent state across varied packets
		sampleRate = 48000
	)

	// Encode a mono SILK WB test sequence (same signal used by forward-pass tests).
	encodeMonoSILKWB := func(t *testing.T, n int) [][]byte {
		t.Helper()
		enc := internalenc.NewEncoder(48000, 1)
		enc.SetMode(internalenc.ModeSILK)
		enc.SetBandwidth(types.BandwidthWideband)
		enc.SetBitrate(40000)
		var packets [][]byte
		for i := 0; i < n; i++ {
			pcm := make([]float32, frameSize)
			for j := 0; j < frameSize; j++ {
				tm := float64(i*frameSize+j) / 48000.0
				pcm[j] = float32(0.3*math.Sin(2*math.Pi*197*tm) +
					0.12*math.Sin(2*math.Pi*389*tm+0.23))
			}
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode mono SILK WB packet %d: %v", i, err)
			}
			if len(pkt) == 0 {
				t.Fatalf("Encode mono SILK WB packet %d: empty", i)
			}
			packets = append(packets, append([]byte(nil), pkt...))
		}
		return packets
	}

	// Encode a stereo SILK WB test sequence.
	encodeStereoSILKWB := func(t *testing.T, n int) [][]byte {
		t.Helper()
		enc := internalenc.NewEncoder(48000, 2)
		enc.SetMode(internalenc.ModeSILK)
		enc.SetBandwidth(types.BandwidthWideband)
		enc.SetBitrate(48000)
		enc.SetForceChannels(2)
		var packets [][]byte
		for i := 0; i < n; i++ {
			pcm := make([]float32, frameSize*2)
			for j := 0; j < frameSize; j++ {
				tm := float64(i*frameSize+j) / 48000.0
				l := 0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23)
				r := 0.27*math.Sin(2*math.Pi*263*tm+0.41) + 0.14*math.Sin(2*math.Pi*431*tm+0.07)
				pcm[2*j] = float32(l)
				pcm[2*j+1] = float32(r)
			}
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode stereo SILK WB packet %d: %v", i, err)
			}
			if len(pkt) == 0 {
				t.Fatalf("Encode stereo SILK WB packet %d: empty", i)
			}
			packets = append(packets, append([]byte(nil), pkt...))
		}
		return packets
	}

	// decodeGopus decodes packets with gopus, armed with the given merged blob
	// and controls.
	decodeGopus := func(t *testing.T, packets [][]byte, channels, complexity int,
		enableBWE, enableLACE bool, mergedBlob []byte) []float32 {
		t.Helper()
		dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
		if err != nil {
			t.Fatalf("NewDecoder(%d ch): %v", channels, err)
		}
		if err := dec.SetComplexity(complexity); err != nil {
			t.Fatalf("SetComplexity(%d): %v", complexity, err)
		}
		if err := dec.SetOSCEBWE(enableBWE); err != nil {
			t.Fatalf("SetOSCEBWE(%v): %v", enableBWE, err)
		}
		if err := dec.SetOSCELACE(enableLACE); err != nil {
			t.Fatalf("SetOSCELACE(%v): %v", enableLACE, err)
		}
		if err := dec.SetDNNBlob(mergedBlob); err != nil {
			t.Fatalf("SetDNNBlob: %v", err)
		}
		totalSamples := frameSize * channels * len(packets)
		out := make([]float32, totalSamples)
		offset := 0
		for i, pkt := range packets {
			pcm := out[offset : offset+frameSize*channels]
			n, err := dec.Decode(pkt, pcm)
			if err != nil {
				t.Fatalf("Decode packet %d: %v", i, err)
			}
			if n != frameSize {
				t.Fatalf("Decode packet %d count=%d, want %d", i, n, frameSize)
			}
			offset += n * channels
		}
		// The same decoder and caller-owned PCM exercise active recurrent state.
		warmPCM := make([]float32, frameSize*channels)
		idx := 0
		if allocs := testing.AllocsPerRun(20, func() {
			n, err := dec.Decode(packets[idx], warmPCM)
			if err != nil || n != frameSize {
				t.Fatalf("warm Decode returned %d, %v", n, err)
			}
			idx = (idx + 1) % len(packets)
		}); allocs != 0 {
			t.Fatalf("warm decode allocations=%g, want 0", allocs)
		}
		if rmsOfFloat32(warmPCM) == 0 {
			t.Fatal("warm decoder output has zero energy")
		}
		for i, v := range warmPCM {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("warm PCM[%d]=%v is not finite", i, v)
			}
		}
		// A malformed multiframe SILK packet must leave no callback installed
		// after Decode returns. Reset then replays a complete valid history.
		badPacket := []byte{0x4b, 0x83, 0x02, 0x01, 0, 0}
		if channels == 2 {
			badPacket[0] |= 4
		}
		errorPCM := make([]float32, 2880*channels)
		if _, err := dec.Decode(badPacket, errorPCM); err != ErrInvalidPacket {
			t.Fatalf("malformed Decode error=%v, want ErrInvalidPacket", err)
		}
		featuresBefore := dec.osceLACE.osceLACEFeatures
		if channels == 2 {
			_, err = dec.silkDecoder.DecodeStereo(packets[0][1:], silk.BandwidthWideband, frameSize, true)
		} else {
			_, err = dec.silkDecoder.Decode(packets[0][1:], silk.BandwidthWideband, frameSize, true)
		}
		if err != nil {
			t.Fatalf("SILK decode after public error: %v", err)
		}
		if dec.osceLACE.osceLACEFeatures != featuresBefore {
			t.Fatal("OSCE callback remains active after public Decode error")
		}
		dec.Reset()
		for frame, pkt := range packets {
			n, err := dec.Decode(pkt, warmPCM)
			if err != nil || n != frameSize {
				t.Fatalf("reset Decode frame=%d returned %d, %v", frame, n, err)
			}
			for i, v := range warmPCM {
				want := out[frame*len(warmPCM)+i]
				if math.Float32bits(v) != math.Float32bits(want) {
					t.Fatalf("reset frame=%d sample=%d Go=%08x initial=%08x", frame, i, math.Float32bits(v), math.Float32bits(want))
				}
			}
		}
		return out[:offset]
	}

	subtests := []struct {
		name       string
		channels   int
		complexity int
		enableBWE  bool
		enableLACE bool
		mergedBlob func() []byte
		qualBar    qualitycompare.QualityBar
	}{
		{
			name: "lace_mono", channels: 1, complexity: 6,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "nolace_mono", channels: 1, complexity: 7,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "bwe_mono", channels: 1, complexity: 4,
			enableBWE: true, enableLACE: false,
			mergedBlob: func() []byte { return mergedAll },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "lace_stereo", channels: 2, complexity: 6,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "nolace_stereo", channels: 2, complexity: 7,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "bwe_stereo", channels: 2, complexity: 4,
			enableBWE: true, enableLACE: false,
			mergedBlob: func() []byte { return mergedAll },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "nolace_bwe_stereo", channels: 2, complexity: 7,
			enableBWE: true, enableLACE: true,
			mergedBlob: func() []byte { return mergedAll },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
	}

	for _, tc := range subtests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var packets [][]byte
			if tc.channels == 1 {
				packets = encodeMonoSILKWB(t, numPackets)
			} else {
				packets = encodeStereoSILKWB(t, numPackets)
			}

			// Verify encoded packets are the right mode/bandwidth.
			for i, pkt := range packets {
				toc := ParseTOC(pkt[0])
				if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
					t.Fatalf("packet %d: want SILK WB, got mode=%v bw=%v", i, toc.Mode, toc.Bandwidth)
				}
				if tc.channels == 2 && !toc.Stereo {
					t.Fatalf("packet %d: want stereo, got mono TOC", i)
				}
			}

			// Get libopus reference decode (OSCE-enabled build uses static weights).
			libopusPCM, err := runLibopusOSCEDecodeSingle(
				binPath,
				sampleRate, tc.channels, frameSize, tc.complexity, tc.enableBWE,
				packets,
			)
			if err != nil {
				t.Fatalf("libopus OSCE decode: %v", err)
			}
			if len(libopusPCM) == 0 {
				t.Fatal("libopus OSCE decode: empty PCM")
			}

			// Get gopus decode.
			gopusPCM := decodeGopus(t, packets, tc.channels, tc.complexity,
				tc.enableBWE, tc.enableLACE, tc.mergedBlob())
			if len(gopusPCM) == 0 {
				t.Fatal("gopus OSCE decode: empty PCM")
			}

			// Verify both sides have non-zero energy.
			if rmsOfFloat32(gopusPCM) == 0 {
				t.Fatal("gopus OSCE decode: zero energy output")
			}
			if rmsOfFloat32(libopusPCM) == 0 {
				t.Fatal("libopus OSCE decode: zero energy output")
			}

			// Confirm no NaN/Inf in either output.
			for i, v := range gopusPCM {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("gopus PCM[%d]=%v is not finite", i, v)
				}
			}
			for i, v := range libopusPCM {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("libopus PCM[%d]=%v is not finite", i, v)
				}
			}

			if len(gopusPCM) != len(libopusPCM) {
				t.Fatalf("PCM length Go=%d C=%d", len(gopusPCM), len(libopusPCM))
			}
			n := len(gopusPCM)
			for i := range gopusPCM {
				if math.Float32bits(gopusPCM[i]) != math.Float32bits(libopusPCM[i]) {
					t.Fatalf("frame=%d sample=%d Go=%08x C=%08x", i/(frameSize*tc.channels), i%(frameSize*tc.channels), math.Float32bits(gopusPCM[i]), math.Float32bits(libopusPCM[i]))
				}
			}

			// Primary oracle: opus_compare Q metric via qualitycompare.
			cmp, err := qualitycompare.CompareDecodedFloat32(
				gopusPCM[:n], libopusPCM[:n],
				sampleRate, tc.channels, 96,
			)
			if err != nil {
				// opus_compare unavailable: fall back to waveform correlation
				// only and log the gap so it can be investigated later.
				t.Logf("opus_compare unavailable, using waveform-only fallback: %v", err)
				var corr float64
				sumA, sumB, sumASq, sumBSq, cov := float64(0), float64(0), float64(0), float64(0), float64(0)
				for i := 0; i < n; i++ {
					fa, fb := float64(gopusPCM[i]), float64(libopusPCM[i])
					sumA += fa
					sumB += fb
					sumASq += fa * fa
					sumBSq += fb * fb
				}
				meanA := sumA / float64(n)
				meanB := sumB / float64(n)
				varA, varB := float64(0), float64(0)
				for i := 0; i < n; i++ {
					da := float64(gopusPCM[i]) - meanA
					db := float64(libopusPCM[i]) - meanB
					cov += da * db
					varA += da * da
					varB += db * db
				}
				if varA > 0 && varB > 0 {
					corr = cov / math.Sqrt(varA*varB)
				}
				t.Logf("%s: corr=%.6f (waveform-only; bar corr>=%.3f)", tc.name, corr, tc.qualBar.MinCorr)
				if tc.qualBar.MinCorr > 0 && corr < tc.qualBar.MinCorr {
					t.Fatalf("%s: waveform correlation %.6f < %.6f", tc.name, corr, tc.qualBar.MinCorr)
				}
				return
			}

			qualitycompare.AssertQuality(t, cmp,
				tc.qualBar,
				fmt.Sprintf("OSCE end-to-end %s", tc.name),
			)
		})
	}
}
