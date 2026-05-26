//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func prepareDecoderForNeuralConcealmentParity(t *testing.T) (*Decoder, []float32, libopusDREDPacket, int) {
	t.Helper()
	return prepareDecoderForNeuralConcealmentParityForFrameSize(t, 480)
}

func prepareDecoderForNeuralConcealmentParityForFrameSize(t *testing.T, frameSize int) (*Decoder, []float32, libopusDREDPacket, int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	packetInfo, err := emitLibopusDREDPacketWithFrameSize(frameSize)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	if channels != 1 {
		t.Skipf("conceal parity test requires mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(16000, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(requireLibopusDecoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	setDREDDecoderBlobFromBytesForTest(t, dec, modelBlob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("Decode did not retain processed DRED state")
	}
	return dec, pcm, packetInfo, n
}

func setDREDDecoderBlobFromBytesForTest(t *testing.T, dec *Decoder, modelBlob []byte) {
	t.Helper()

	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
}

func TestDecoderFirstLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 0, 0, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "first-loss next-packet")
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
	}
	frameSize48 := n * 48000 / dec.SampleRate()
	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize48)

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil)=%d want %d", gotN, n)
	}
	assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "first-loss live-sequence pcm", pcmTol)

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
	}

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "first-loss next packet live-sequence pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "first-loss next packet live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "first-loss next packet live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "first-loss next packet live-sequence celt", celtTol)
}

func TestDecoderSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 1, 2*n, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "second-loss next-packet")
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
	}
	if want.step1.ret != n {
		t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
	}
	frameSize48 := n * 48000 / dec.SampleRate()
	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize48)

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, first)=%d want %d", gotN, n)
	}
	assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "second-loss warmup live-sequence pcm", pcmTol)

	gotN, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
	}
	assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second-loss live-sequence pcm", pcmTol)

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
	}

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "second-loss next packet live-sequence pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "second-loss next packet live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "second-loss next packet live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "second-loss next packet live-sequence celt", celtTol)
}

func TestDecoderFirstLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("first-loss next-packet carrier_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil)=%d want %d", gotN, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "first-loss frame-size live-sequence pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "first-loss frame-size next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "first-loss frame-size next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "first-loss frame-size next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "first-loss frame-size next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderSecondLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("second-loss next-packet carrier_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, first)=%d want %d", gotN, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "second-loss frame-size warmup live-sequence pcm", pcmTol)

			gotN, err = dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second-loss frame-size live-sequence pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "second-loss frame-size next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "second-loss frame-size next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "second-loss frame-size next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "second-loss frame-size next packet live-sequence celt", celtTol)
		})
	}
}
