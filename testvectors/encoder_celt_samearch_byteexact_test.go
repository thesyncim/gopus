package testvectors

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// celtSameArchByteExactCase enumerates CELT-mode encode cases that gopus
// reproduces byte-for-byte against the feature- and ISA-matched opus_demo
// encoder on this host. Both encoders receive the same int24 input samples.
type celtSameArchByteExactCase struct {
	name      string
	mode      string
	bw        string
	bwE       types.Bandwidth
	frameSize int
	channels  int
	bitrate   int
	variant   string
}

// celtSameArchByteExactCases are (case, signal-variant) pairs that are byte-exact
// against the selected same-architecture libopus build. They lock in CELT
// forward-encode parity across signals, durations, channel counts, and rates.
func celtSameArchByteExactCases() []celtSameArchByteExactCase {
	mk := func(name string, fs, ch, br int, variant string) celtSameArchByteExactCase {
		return celtSameArchByteExactCase{
			name: name, mode: "celt",
			bw: "fb", bwE: types.BandwidthFullband,
			frameSize: fs, channels: ch, bitrate: br, variant: variant,
		}
	}
	return []celtSameArchByteExactCase{
		mk("CELT-FB-20ms-mono-32k", 960, 1, 32000, "am_multisine_v1"),
		mk("CELT-FB-20ms-mono-32k", 960, 1, 32000, "chirp_sweep_v1"),
		mk("CELT-FB-20ms-mono-32k", 960, 1, 32000, "impulse_train_v1"),
		mk("CELT-FB-20ms-mono-32k", 960, 1, 32000, "speech_like_v1"),
		mk("CELT-FB-20ms-mono-64k", 960, 1, 64000, "impulse_train_v1"),
		mk("CELT-FB-20ms-mono-64k", 960, 1, 64000, "speech_like_v1"),
		mk("CELT-FB-20ms-mono-48k", 960, 1, 48000, "am_multisine_v1"),
		mk("CELT-FB-20ms-mono-48k", 960, 1, 48000, "impulse_train_v1"),
		mk("CELT-FB-20ms-mono-48k", 960, 1, 48000, "speech_like_v1"),
		mk("CELT-FB-10ms-mono-64k", 480, 1, 64000, "impulse_train_v1"),
		mk("CELT-FB-10ms-mono-64k", 480, 1, 64000, "speech_like_v1"),
		mk("CELT-FB-5ms-mono-64k", 240, 1, 64000, "am_multisine_v1"),
		mk("CELT-FB-2.5ms-mono-64k", 120, 1, 64000, "impulse_train_v1"),
		mk("CELT-FB-2.5ms-mono-64k", 120, 1, 64000, "speech_like_v1"),
		mk("CELT-FB-20ms-stereo-128k", 960, 2, 128000, "impulse_train_v1"),
		mk("CELT-FB-20ms-stereo-128k", 960, 2, 128000, "speech_like_v1"),
		mk("CELT-FB-20ms-mono-96k", 960, 1, 96000, "impulse_train_v1"),
		mk("CELT-FB-20ms-mono-96k", 960, 1, 96000, "speech_like_v1"),
		mk("CELT-FB-20ms-mono-128k", 960, 1, 128000, "impulse_train_v1"),
	}
}

// TestEncoderCELTSameArchByteExact compares every CELT packet and final range
// with the selected libopus opus_demo build, including its zero-padded EOF frame.
func TestEncoderCELTSameArchByteExact(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "public API opus_demo", err)
		return
	}

	cases := celtSameArchByteExactCases()
	if len(cases) == 0 {
		t.Fatal("empty CELT same-architecture parity corpus")
	}
	tmpDir := t.TempDir()
	for _, c := range cases {
		t.Run(c.name+"/"+c.variant, func(t *testing.T) {
			if c.frameSize <= 0 || c.channels <= 0 {
				t.Fatalf("invalid CELT case geometry frame=%d channels=%d", c.frameSize, c.channels)
			}
			signalFrames := 48000 / c.frameSize
			if signalFrames == 0 {
				t.Fatalf("invalid frame size %d", c.frameSize)
			}
			totalSamples := signalFrames * c.frameSize * c.channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.variant, 48000, totalSamples, c.channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			if len(signal) != totalSamples {
				t.Fatalf("signal samples=%d want %d", len(signal), totalSamples)
			}

			rawPath := filepath.Join(tmpDir, c.name+"_"+c.variant+".f32")
			bitPath := filepath.Join(tmpDir, c.name+"_"+c.variant+".bit")
			if err := writeFloat32LEFile(rawPath, signal); err != nil {
				t.Fatalf("write raw: %v", err)
			}
			app, err := modeToOpusDemoApp(c.mode)
			if err != nil {
				t.Fatalf("map mode: %v", err)
			}
			bwArg, err := bandwidthToOpusDemoArg(c.bw)
			if err != nil {
				t.Fatalf("map bandwidth: %v", err)
			}
			frameArg, err := frameSizeSamplesToArg(c.frameSize)
			if err != nil {
				t.Fatalf("map frame size: %v", err)
			}
			libPackets, libRanges, err := runOpusDemoCELTEncode(opusDemo, app, bwArg, frameArg, c.bitrate, c.channels, rawPath, bitPath)
			if err != nil {
				t.Fatalf("opus_demo encode: %v", err)
			}

			enc, err := newVBRCVBREncoder(gopus.ApplicationLowDelay, 48000,
				c.channels, c.frameSize, c.bitrate, c.bwE, true,
				types.SignalAuto, false, true)
			if err != nil {
				t.Fatalf("gopus encoder setup: %v", err)
			}
			enc.SetVBR(false)

			samplesPerFrame := c.frameSize * c.channels
			// opus_demo -f32 quantizes to int24, calls opus_encode24, then
			// encodes one zero-padded frame after reading the exact-frame EOF.
			goPackets := make([][]byte, 0, signalFrames+1)
			goRanges := make([]uint32, 0, signalFrames+1)
			buf := make([]byte, 4000)
			for i := range signalFrames + 1 {
				frame := make([]int32, samplesPerFrame)
				if i < signalFrames {
					frame = quantizeOpusDemoFloatInputToInt24(signal[i*samplesPerFrame : (i+1)*samplesPerFrame])
				}
				n, err := enc.EncodeInt24(frame, buf)
				if err != nil {
					t.Fatalf("gopus encode frame %d: %v", i, err)
				}
				if n == 0 {
					t.Fatalf("gopus encode frame %d: empty packet", i)
				}
				goPackets = append(goPackets, append([]byte(nil), buf[:n]...))
				goRanges = append(goRanges, enc.FinalRange())
			}

			if len(libPackets) != signalFrames+1 || len(libRanges) != signalFrames+1 ||
				len(goPackets) != signalFrames+1 || len(goRanges) != signalFrames+1 {
				t.Fatalf("frame records: gopus packets/ranges=%d/%d libopus packets/ranges=%d/%d, want %d each",
					len(goPackets), len(goRanges), len(libPackets), len(libRanges), signalFrames+1)
			}
			var diffFrames []int
			for i := range libPackets {
				if len(libPackets[i]) == 0 {
					t.Fatalf("libopus encode frame %d: empty packet", i)
				}
				if !bytes.Equal(goPackets[i], libPackets[i]) || goRanges[i] != libRanges[i] {
					diffFrames = append(diffFrames, i)
				}
			}
			if len(diffFrames) == 0 {
				return
			}

			// Report the first few diffs for diagnosis on either tier.
			for i, fi := range diffFrames {
				if i >= 3 {
					t.Logf("  ... and %d more differing frames", len(diffFrames)-3)
					break
				}
				byteDiff := firstByteDiff(goPackets[fi], libPackets[fi])
				t.Logf("frame %d diverges (arch=%s): goLen=%d libLen=%d firstByteDiff=%d goRange=%08x libRange=%08x\n  go =%x\n  lib=%x",
					fi, runtime.GOARCH, len(goPackets[fi]), len(libPackets[fi]), byteDiff,
					goRanges[fi], libRanges[fi], goPackets[fi], libPackets[fi])
			}

			t.Fatalf("CELT same-arch packet/range parity FAIL: %d/%d frames differ (arch=%s)",
				len(diffFrames), signalFrames+1, runtime.GOARCH)
		})
	}
}

func firstByteDiff(a, b []byte) int {
	m := min(len(b), len(a))
	for i := 0; i < m; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return m
	}
	return -1
}

func runOpusDemoCELTEncode(opusDemo, app, bwArg, frameArg string, bitrate, channels int, rawPath, bitPath string) ([][]byte, []uint32, error) {
	cmd := exec.Command(opusDemo,
		"-e", app, "48000", strconv.Itoa(channels), strconv.Itoa(bitrate),
		"-f32", "-cbr", "-complexity", "10", "-bandwidth", bwArg, "-framesize", frameArg,
		rawPath, bitPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("%v (%s)", err, out)
	}
	return parseOpusDemoEncodeBitstream(bitPath)
}
