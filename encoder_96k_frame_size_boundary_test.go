//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"testing"
)

func TestNative96kEncoderFrameSizeBoundariesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, size := range []int{240, 480, 960, 1920, 3840, 5760, 7680, 9600, 11520} {
			t.Run(fmt.Sprintf("ch%d/samples%d", channels, size), func(t *testing.T) {
				params := libopustest.EncodeDiffParams{
					SampleRate: 96000, Channels: channels,
					Application: libopustest.EncodeDiffApplicationAudio,
					Bitrate:     64000, Complexity: 0, Signal: libopustest.EncodeDiffSignalAuto,
					VBR: true, VBRConstraint: true, FrameSize: size, FrameCount: 2,
					PCM: make([]float32, 2*size*channels),
				}
				for i := range params.PCM {
					params.PCM[i] = float32((i*37)%8192-4096) / 32768
				}
				want, err := libopustest.ProbeEncodeDiff(params)
				if err != nil {
					libopustest.HelperUnavailable(t, "96 kHz frame boundary", err)
				}
				if len(want) != 2 {
					t.Fatalf("oracle frames=%d, want 2", len(want))
				}
				enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: channels, Application: ApplicationAudio})
				if err != nil {
					t.Fatal(err)
				}
				for _, err := range []error{enc.SetBitrate(64000), enc.SetComplexity(0), enc.SetFrameSize(size)} {
					if err != nil {
						t.Fatal(err)
					}
				}
				enc.SetVBR(true)
				enc.SetVBRConstraint(true)
				out := make([]byte, 4000)
				for frame := range 2 {
					if frame == 1 {
						for _, invalid := range []int{size - 1, size + 1} {
							bad := params
							bad.FrameSize, bad.FrameCount = invalid, 1
							bad.PCM = make([]float32, invalid*channels)
							records, err := libopustest.ProbeEncodeDiff(bad)
							if err != nil {
								t.Fatal(err)
							}
							if len(records) != 1 || records[0].Ret != -1 {
								t.Fatalf("libopus frame size %d: records=%+v, want OPUS_BAD_ARG", invalid, records)
							}
							rng := enc.FinalRange()
							if err := enc.SetFrameSize(invalid); err != ErrInvalidFrameSize {
								t.Fatalf("SetFrameSize(%d)=%v, want ErrInvalidFrameSize", invalid, err)
							}
							if enc.FrameSize() != size || enc.FinalRange() != rng {
								t.Fatal("rejected frame size changes encoder state")
							}
						}
					}
					pcm := params.PCM[frame*size*channels : (frame+1)*size*channels]
					n, err := enc.Encode(pcm, out)
					if err != nil || n != want[frame].Ret || !bytes.Equal(out[:n], want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
						t.Fatalf("frame%d: Go bytes=%d range=%08x error=%v; C bytes=%d range=%08x", frame, n, enc.FinalRange(), err, want[frame].Ret, want[frame].FinalRange)
					}
				}
			})
		}
	}
}

type hd96kPacketLayout struct {
	toc         byte
	code        int
	countByte   byte
	hasPadding  bool
	nbFrames    int
	paddingLen  int // decoded total padding (= qext_bytes for the native path)
	padFieldLen int // bytes consumed by the padding-length field
	main        []byte
	extIDByte   byte // first byte of the extension region (0xF8 for QEXT)
	extID       int  // extIDByte >> 1
	qextPayload []byte
}

func parseHD96kLayout(t *testing.T, pkt []byte) hd96kPacketLayout {
	t.Helper()
	if len(pkt) < 2 {
		t.Fatalf("packet too short: %d", len(pkt))
	}
	var l hd96kPacketLayout
	l.toc = pkt[0]
	l.code = int(pkt[0] & 0x03)
	if l.code != 3 {
		// Code-0 (no QEXT) packet: main is everything after TOC.
		l.main = pkt[1:]
		return l
	}
	l.countByte = pkt[1]
	l.hasPadding = l.countByte&0x40 != 0
	l.nbFrames = int(l.countByte & 0x3F)
	offset := 2
	if l.hasPadding {
		start := offset
		for {
			if offset >= len(pkt) {
				t.Fatalf("padding overran packet")
			}
			b := int(pkt[offset])
			offset++
			if b == 255 {
				l.paddingLen += 254
			} else {
				l.paddingLen += b
				break
			}
		}
		l.padFieldLen = offset - start
	}
	end := len(pkt) - l.paddingLen
	if end < offset {
		t.Fatalf("bad framing: dataStart=%d end=%d", offset, end)
	}
	l.main = pkt[offset:end]
	if l.paddingLen > 0 {
		ext := pkt[end:]
		l.extIDByte = ext[0]
		l.extID = int(ext[0] >> 1)
		l.qextPayload = ext[1:]
	}
	return l
}

// TestHD96kFramingByteParityMatchesLibopus drives the public 96 kHz QEXT encode
// path and checks the assembled Opus packet framing against the libopus
// --enable-qext oracle: TOC byte, frame-count byte, padding-length field, the
// extension-ID byte (0xF8) and the byte offsets/lengths of the main and QEXT
// payload regions must all match exactly.
func TestHD96kFramingByteParityMatchesLibopus(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000

	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       bitrate,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 8000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			ref := parseHD96kLayout(t, res.Packets[0])

			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    ch,
				Application: ApplicationRestrictedCelt,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}

			out, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("EncodeFloat32: %v", err)
			}
			got := parseHD96kLayout(t, out)

			// TOC byte (config 31, stereo bit, code 3) must be identical.
			if got.toc != ref.toc {
				t.Errorf("TOC byte: got 0x%02x want 0x%02x", got.toc, ref.toc)
			}
			if got.code != ref.code {
				t.Errorf("frame code: got %d want %d", got.code, ref.code)
			}
			// Frame-count byte (padding flag | 1 frame) must match.
			if got.countByte != ref.countByte {
				t.Errorf("frame-count byte: got 0x%02x want 0x%02x", got.countByte, ref.countByte)
			}
			if got.hasPadding != ref.hasPadding {
				t.Errorf("padding flag: got %v want %v", got.hasPadding, ref.hasPadding)
			}
			if got.nbFrames != ref.nbFrames {
				t.Errorf("frame count: got %d want %d", got.nbFrames, ref.nbFrames)
			}
			// Padding-length field (qext_bytes) must be byte-identical.
			if got.paddingLen != ref.paddingLen {
				t.Errorf("padding length (qext_bytes): got %d want %d", got.paddingLen, ref.paddingLen)
			}
			if got.padFieldLen != ref.padFieldLen {
				t.Errorf("padding-length field bytes: got %d want %d", got.padFieldLen, ref.padFieldLen)
			}
			// Main CELT payload byte budget must match.
			if len(got.main) != len(ref.main) {
				t.Errorf("main payload length: got %d want %d", len(got.main), len(ref.main))
			}
			// Extension-ID byte (QEXT_EXTENSION_ID<<1 = 0xF8) must match.
			if got.extIDByte != ref.extIDByte {
				t.Errorf("extension-ID byte: got 0x%02x want 0x%02x", got.extIDByte, ref.extIDByte)
			}
			if got.extID != qextPacketExtensionID {
				t.Errorf("extension ID: got %d want %d", got.extID, qextPacketExtensionID)
			}
			// QEXT payload byte count must match.
			if len(got.qextPayload) != len(ref.qextPayload) {
				t.Errorf("QEXT payload length: got %d want %d", len(got.qextPayload), len(ref.qextPayload))
			}
			// Total packet length must match.
			if len(out) != len(res.Packets[0]) {
				t.Errorf("total packet length: got %d want %d", len(out), len(res.Packets[0]))
			}

			t.Logf("ch=%d framing: toc=0x%02x count=0x%02x qext_bytes=%d main=%d qext=%d total=%d",
				ch, got.toc, got.countByte, got.paddingLen, len(got.main), len(got.qextPayload), len(out))

			// Report how far the full-packet bytes match (diagnostic for the
			// upstream comb / band-data residual; framing above is the contract).
			refPkt := res.Packets[0]
			firstDiff := firstByteDiff(out, refPkt)
			if firstDiff < 0 {
				t.Logf("ch=%d: FULL PACKET byte-exact (%d bytes)", ch, len(out))
			} else {
				t.Logf("ch=%d: full packet diverges at byte %d (framing exact; main-payload residual)", ch, firstDiff)
			}
		})
	}
}

// TestHD96kFramingExtensionRoundtrip checks that the gopus native 96 kHz QEXT
// packet decodes through the gopus decoder without error and that the QEXT
// extension is correctly recovered from the padding region (framing is
// self-consistent on the decode side).
func TestHD96kFramingExtensionRoundtrip(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000

	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(fmt.Sprintf("%dch", ch), func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    ch,
				Application: ApplicationRestrictedCelt,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			_ = enc.SetBitrate(bitrate)
			_ = enc.SetBitrateMode(BitrateModeCBR)
			_ = enc.SetComplexity(10)
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}
			out, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("EncodeFloat32: %v", err)
			}

			// The QEXT extension must be recoverable from the padding region.
			l := parseHD96kLayout(t, out)
			if l.code != 3 {
				t.Fatalf("expected code-3 packet, got code %d", l.code)
			}
			if l.extID != qextPacketExtensionID {
				t.Fatalf("QEXT extension ID not recovered: got %d", l.extID)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, ch))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcmOut := make([]float32, frameSize*ch)
			n, err := dec.Decode(out, pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != frameSize {
				t.Errorf("decoded %d samples want %d", n, frameSize)
			}
		})
	}
}

const (
	rootHD96kInputMagic  = "G96M"
	rootHD96kOutputMagic = "G96O"
)

var rootHD96kModeHelper libopustest.HelperCache

type rootHD96kRecord struct {
	packet  []byte
	range32 uint32
}

func buildRootHD96kModeHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "native 96 kHz encoder mode sequence",
		OutputBase:  "gopus_libopus_encoder_96k_mode_sequence",
		SourceFile:  "libopus_encoder_96k_mode_sequence.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	})
}

func encodeRootHD96kWithLibopus(t *testing.T, qext bool, budgets []int, frames [][]float32) []rootHD96kRecord {
	t.Helper()
	if len(budgets) != len(frames) {
		t.Fatalf("oracle input has %d budgets and %d frames", len(budgets), len(frames))
	}
	if len(frames) == 0 {
		t.Fatal("oracle input has no frames")
	}
	frameSize := len(frames[0])
	qextFlag := uint32(0)
	if qext {
		qextFlag = 1
	}
	payload := libopustest.NewOraclePayloadVersion(rootHD96kInputMagic, 1,
		uint32(frameSize), uint32(len(frames)), qextFlag, 10, 24)
	for i, frame := range frames {
		if len(frame) != frameSize {
			t.Fatalf("frame %d has %d samples, want %d", i, len(frame), frameSize)
		}
		payload.U32(uint32(budgets[i]))
		payload.U32(256000)
		payload.Float32s(frame...)
	}
	bin, err := rootHD96kModeHelper.Path(buildRootHD96kModeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz mode sequence", err)
		return nil
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "native 96 kHz mode sequence", rootHD96kOutputMagic, 1)
	if err != nil {
		t.Fatalf("selected libopus encoder: %v", err)
	}
	count := reader.Count(len(frames))
	if count != len(frames) {
		t.Fatalf("selected C returned %d frames, want %d: %v", count, len(frames), reader.Err())
	}
	records := make([]rootHD96kRecord, count)
	for i := range records {
		n := int(reader.U32())
		records[i].range32 = reader.U32()
		if n < 1 || n > budgets[i] {
			t.Fatalf("selected C packet %d has length %d for budget %d", i, n, budgets[i])
		}
		records[i].packet = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestRootNative96kModeBudgetSequenceMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const packetCapacity = 3825
	for _, frameSize := range []int{1920, 3840} {
		for _, qext := range []bool{false, true} {
			for _, prime := range []bool{false, true} {
				for _, lowBudget := range []int{16, 32} {
					name := fmt.Sprintf("frame%d/qext%t/primed%t/budget%d", frameSize, qext, prime, lowBudget)
					t.Run(name, func(t *testing.T) {
						budgets := []int{lowBudget, packetCapacity}
						if prime {
							budgets = append([]int{packetCapacity}, budgets...)
						}
						frames := makeRootHD96kPCM(frameSize, len(budgets))
						want := encodeRootHD96kWithLibopus(t, qext, budgets, frames)
						lowBudgetFrame := 0
						if prime {
							lowBudgetFrame = 1
						}
						if frameSize == 1920 && want[lowBudgetFrame].packet[0] != 0x48 {
							t.Fatalf("selected C low-budget frame TOC=%02x, want native 96 kHz SILK WB TOC 48", want[lowBudgetFrame].packet[0])
						}
						enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: 1, Application: ApplicationAudio})
						if err != nil {
							t.Fatal(err)
						}
						if err := enc.SetBitrate(256000); err != nil {
							t.Fatal(err)
						}
						if err := enc.SetComplexity(10); err != nil {
							t.Fatal(err)
						}
						enc.SetVBR(true)
						enc.SetVBRConstraint(true)
						if err := enc.SetQEXT(qext); err != nil {
							t.Fatal(err)
						}
						if enc.FrameSize() != frameSize {
							if err := enc.SetFrameSize(frameSize); err != nil {
								t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
							}
						}
						out := make([]byte, packetCapacity)
						for step, budget := range budgets {
							if err := enc.SetBitrate(256000); err != nil {
								t.Fatal(err)
							}
							n, err := enc.Encode(frames[step], out[:budget])
							if err != nil {
								t.Fatalf("step%d encode: %v", step, err)
							}
							if !bytes.Equal(out[:n], want[step].packet) {
								t.Fatalf("step%d budget%d packet Go=%x C=%x", step, budget, out[:n], want[step].packet)
							}
							if got := enc.FinalRange(); got != want[step].range32 {
								t.Fatalf("step%d range Go=%08x C=%08x", step, got, want[step].range32)
							}
						}
						encodeWarm := func() {
							for step, budget := range budgets {
								if _, err := enc.Encode(frames[step], out[:budget]); err != nil {
									panic(err)
								}
							}
						}
						if allocs := testing.AllocsPerRun(10, encodeWarm); allocs != 0 {
							t.Fatalf("warm allocations=%g", allocs)
						}
					})
				}
			}
		}
	}
}

func makeRootHD96kPCM(frameSize, frames int) [][]float32 {
	pcm := make([][]float32, frames)
	for frame := range frames {
		pcm[frame] = make([]float32, frameSize)
		for i := range frameSize {
			n := frame*frameSize + i
			x := 0.36*math.Sin(2*math.Pi*5300*float64(n)/96000) +
				0.16*math.Sin(2*math.Pi*11900*float64(n)/96000) +
				0.21*math.Sin(2*math.Pi*30000*float64(n)/96000)
			pcm[frame][i] = float32(int16(math.Round(x*32767))) / 32768
		}
	}
	return pcm
}

func hd96kParitySine(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := 0.4*math.Sin(2*math.Pi*6000*float64(i)/96000) +
			0.2*math.Sin(2*math.Pi*30000*float64(i)/96000)
		pcm[i*channels] = float32(s)
		if channels == 2 {
			pcm[i*channels+1] = float32(0.3 * s)
		}
	}
	return pcm
}

// refMainCELTPayload extracts the main CELT payload and the QEXT extension
// payload from a native 96 kHz code-3 reference Opus packet carrying a single
// CBR frame. Returns (mainPayload, qextPayload).
func refMainCELTPayload(t *testing.T, pkt []byte) (main, qext []byte) {
	t.Helper()
	if len(pkt) < 2 {
		t.Fatalf("ref packet too short: %d", len(pkt))
	}
	if pkt[0]&0x03 != 3 {
		t.Fatalf("ref packet not code 3: toc=0x%02x", pkt[0])
	}
	fc := pkt[1]
	vbr := fc&0x80 != 0
	hasPad := fc&0x40 != 0
	m := int(fc & 0x3f)
	if m != 1 {
		t.Fatalf("expected single frame, got m=%d", m)
	}
	if vbr {
		t.Fatalf("expected CBR ref packet")
	}
	offset := 2
	padding := 0
	if hasPad {
		for {
			if offset >= len(pkt) {
				t.Fatalf("padding overran packet")
			}
			b := int(pkt[offset])
			offset++
			if b == 255 {
				padding += 254
			} else {
				padding += b
				break
			}
		}
	}
	// Single CBR frame: the frame data runs from offset to len-padding.
	end := len(pkt) - padding
	if end < offset {
		t.Fatalf("bad framing: offset=%d end=%d", offset, end)
	}
	main = pkt[offset:end]

	if hasPad && padding > 0 {
		var payloads [maxRepacketizerFrames][]byte
		collectQEXTPacketExtensions(pkt[len(pkt)-padding:], m, qextPacketExtensionID, &payloads)
		qext = payloads[0]
	}
	return main, qext
}

// TestHD96kNativeEncodeMainPayloadParity compares the native 96 kHz CELT main
// payload and QEXT side payload for one fullband CBR frame against the selected
// libopus reference. It checks mono and stereo and fails at the first differing
// byte.
func TestHD96kNativeEncodeMainPayloadParity(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       bitrate,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 8000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			refMain, refQext := refMainCELTPayload(t, res.Packets[0])

			e := celt.NewEncoder(ch)
			// QEXT path: libopus copies raw PCM into the CELT buffer (no dc_reject,
			// no delay compensation). RESTRICTED_LOWDELAY only forces CELT-only; it
			// does NOT disable the prefilter, so leave CELT prediction at default.
			e.SetDCRejectEnabled(false)
			e.SetLSBQuantizationEnabled(false)
			e.SetDelayCompensationEnabled(false)
			e.SetVBR(false)
			e.SetComplexity(10)
			e.SetBitrate(bitrate)
			e.SetLSBDepth(24)
			e.SetQEXTEnabled(true)
			e.EnableHD96kMode()

			gotMain, err := e.EncodeFrame(pcm, frameSize)
			if err != nil {
				t.Fatalf("ch=%d: EncodeFrame: %v", ch, err)
			}
			gotQext := e.LastQEXTPayload()

			if len(gotMain) != len(refMain) {
				t.Logf("ch=%d main byte budget got=%d ref=%d", ch, len(gotMain), len(refMain))
			}
			diagFlags(t, "got", gotMain)
			diagFlags(t, "ref", refMain)

			firstMainDiff := firstByteDiff(gotMain, refMain)
			firstQextDiff := firstByteDiff(gotQext, refQext)
			// Both mono and stereo native-96k encode are byte-exact (main + QEXT);
			// any mismatch is a real correctness regression.
			if firstMainDiff >= 0 {
				dumpAround(t, "main", gotMain, refMain, firstMainDiff)
				t.Fatalf("ch=%d main payload diverges at byte %d (got len=%d ref len=%d)",
					ch, firstMainDiff, len(gotMain), len(refMain))
			}
			if firstQextDiff >= 0 {
				dumpAround(t, "qext", gotQext, refQext, firstQextDiff)
				t.Fatalf("ch=%d qext payload diverges at byte %d (got len=%d ref len=%d)",
					ch, firstQextDiff, len(gotQext), len(refQext))
			}
		})
	}
}

func diagFlags(t *testing.T, label string, payload []byte) {
	t.Helper()
	var d rangecoding.Decoder
	d.Init(payload)
	silence := d.DecodeBit(15)
	pf := d.DecodeBit(1)
	transient := d.DecodeBit(3)
	intra := d.DecodeBit(3)
	t.Logf("%s flags: silence=%d postfilter=%d transient=%d intra=%d tell=%d",
		label, silence, pf, transient, intra, d.Tell())
}

func dumpAround(t *testing.T, label string, got, ref []byte, at int) {
	t.Helper()
	lo := at - 4
	if lo < 0 {
		lo = 0
	}
	hiG := at + 8
	if hiG > len(got) {
		hiG = len(got)
	}
	hiR := at + 8
	if hiR > len(ref) {
		hiR = len(ref)
	}
	t.Logf("%s got[%d:%d]=% x", label, lo, hiG, got[lo:hiG])
	t.Logf("%s ref[%d:%d]=% x", label, lo, hiR, ref[lo:hiR])
}
