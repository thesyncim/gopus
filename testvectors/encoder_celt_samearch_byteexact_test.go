package testvectors

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// celtSameArchByteExactCase enumerates CELT-mode encode cases that gopus
// reproduces byte-for-byte against the SAME-ARCH float libopus encoder
// (opus_demo, native build on this host). The comparison is live: opus_demo is
// driven on this machine, so it is gopus-arm64 vs libopus-arm64 (or amd64 vs
// amd64), not a cross-arch fixture.
type celtSameArchByteExactCase struct {
	name      string
	mode      string
	modeE     encoder.Mode
	bw        string
	bwE       types.Bandwidth
	frameSize int
	channels  int
	bitrate   int
	variant   string
}

// celtSameArchByteExactCases are (case, signal-variant) pairs that are byte-exact
// vs same-arch libopus on arm64. They lock in the established CELT-encode byte
// parity so a regression in the forward float path (MDCT, band energy, PVQ
// pre-search rcp/FMA order, allocation) is caught immediately.
func celtSameArchByteExactCases() []celtSameArchByteExactCase {
	mk := func(name string, fs, ch, br int, variant string) celtSameArchByteExactCase {
		return celtSameArchByteExactCase{
			name: name, mode: "celt", modeE: encoder.ModeCELT,
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

// TestEncoderCELTSameArchByteExact drives the native libopus encoder on this
// host and asserts gopus produces byte-identical CELT packets for the cases in
// celtSameArchByteExactCases. It demonstrates (not masks) genuine same-arch
// byte parity for the CELT forward float path.
func TestEncoderCELTSameArchByteExact(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	opusDemo, ok := getFixtureOpusDemoPathForEncoder()
	if !ok {
		t.Skip("opus_demo not found; same-arch byte-exact comparison unavailable")
	}

	tmpDir := t.TempDir()
	for _, c := range celtSameArchByteExactCases() {
		c := c
		t.Run(c.name+"/"+c.variant, func(t *testing.T) {
			signalFrames := 48000 / c.frameSize
			totalSamples := signalFrames * c.frameSize * c.channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.variant, 48000, totalSamples, c.channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
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
			libPackets, _, err := runOpusDemoCELTEncode(opusDemo, app, bwArg, frameArg, c.bitrate, c.channels, rawPath, bitPath)
			if err != nil {
				t.Fatalf("opus_demo encode: %v", err)
			}

			enc := encoder.NewEncoder(48000, c.channels)
			enc.SetLowDelay(true)
			enc.SetMode(c.modeE)
			enc.SetBandwidth(c.bwE)
			enc.SetBitrate(c.bitrate)
			enc.SetBitrateMode(encoder.ModeCBR)
			enc.SetComplexity(10)

			samplesPerFrame := c.frameSize * c.channels
			goPackets := make([][]byte, 0, signalFrames)
			for i := 0; i < signalFrames; i++ {
				frame := float32ToFloat64OpusDemoF32(signal[i*samplesPerFrame : (i+1)*samplesPerFrame])
				pkt, err := encodeTest(enc, frame, c.frameSize)
				if err != nil {
					t.Fatalf("gopus encode frame %d: %v", i, err)
				}
				goPackets = append(goPackets, append([]byte(nil), pkt...))
			}

			n := len(libPackets)
			if len(goPackets) < n {
				n = len(goPackets)
			}
			for i := 0; i < n; i++ {
				if !bytes.Equal(goPackets[i], libPackets[i]) {
					byteDiff := firstByteDiff(goPackets[i], libPackets[i])
					t.Fatalf("frame %d not byte-exact (arch=%s): goLen=%d libLen=%d firstByteDiff=%d\n  go =%x\n  lib=%x",
						i, runtime.GOARCH, len(goPackets[i]), len(libPackets[i]), byteDiff, goPackets[i], libPackets[i])
				}
			}
		})
	}
}

func firstByteDiff(a, b []byte) int {
	m := len(a)
	if len(b) < m {
		m = len(b)
	}
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
