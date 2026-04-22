//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const (
	libopusDecoderDREDDecodeFloatInputMagic  = "GDDI"
	libopusDecoderDREDDecodeFloatOutputMagic = "GDDO"
)

type libopusDecoderDREDDecodeFloatInfo struct {
	parseRet int
	dredEnd  int
	warmupRet int
	ret      int
	channels int
	pcm      []float32
}

var (
	libopusDecoderDREDDecodeFloatHelperOnce sync.Once
	libopusDecoderDREDDecodeFloatHelperPath string
	libopusDecoderDREDDecodeFloatHelperErr  error
)

func getLibopusDecoderDREDDecodeFloatHelperPath() (string, error) {
	libopusDecoderDREDDecodeFloatHelperOnce.Do(func() {
		libopusDecoderDREDDecodeFloatHelperPath, libopusDecoderDREDDecodeFloatHelperErr = buildLibopusDREDHelper("libopus_decoder_dred_decode_float_info.c", "gopus_libopus_decoder_dred_decode_float", false)
	})
	if libopusDecoderDREDDecodeFloatHelperErr != nil {
		return "", libopusDecoderDREDDecodeFloatHelperErr
	}
	return libopusDecoderDREDDecodeFloatHelperPath, nil
}

func probeLibopusDecoderDREDDecodeFloat(packet []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	binPath, err := getLibopusDecoderDREDDecodeFloatHelperPath()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDecoderDREDDecodeFloatInputMagic)
	for _, v := range []uint32{
		2,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(warmupDREDOffsetSamples),
		uint32(dredOffsetSamples),
		uint32(frameSizeSamples),
		uint32(len(packet)),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper header: %w", err)
		}
	}
	if _, err := payload.Write(packet); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper packet: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("run decoder dred decode helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	if len(out) < 28 || string(out[:4]) != libopusDecoderDREDDecodeFloatOutputMagic {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("unexpected decoder dred decode helper output")
	}
	info := libopusDecoderDREDDecodeFloatInfo{
		parseRet:  int(int32(binary.LittleEndian.Uint32(out[8:12]))),
		dredEnd:   int(int32(binary.LittleEndian.Uint32(out[12:16]))),
		warmupRet: int(int32(binary.LittleEndian.Uint32(out[16:20]))),
		ret:       int(int32(binary.LittleEndian.Uint32(out[20:24]))),
		channels:  int(int32(binary.LittleEndian.Uint32(out[24:28]))),
	}
	offset := 28
	if info.ret > 0 && info.channels > 0 {
		info.pcm = make([]float32, info.ret*info.channels)
		for i := range info.pcm {
			if offset+4 > len(out) {
				return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("truncated decoder dred decode helper pcm")
			}
			info.pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
			offset += 4
		}
	}
	return info, nil
}

func prepareExplicitDREDDecodeParityState(t *testing.T) (*Decoder, *DRED, libopusDREDPacket, int) {
	t.Helper()

	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	if packetInfo.sampleRate != 16000 || channels != 1 {
		t.Skipf("explicit DRED decode parity requires 16 kHz mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	seedPCM := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, seedPCM)
	if err != nil {
		t.Fatalf("Decode(seed packet) error: %v", err)
	}
	if n != lpcnetplc.FrameSize {
		t.Skipf("explicit DRED decode parity is single-frame only, got frame size %d", n)
	}

	standalone := NewDREDDecoder()
	if err := standalone.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("standalone SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	if _, _, err := standalone.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true); err != nil {
		t.Fatalf("standalone Parse error: %v", err)
	}
	if err := standalone.Process(dred, dred); err != nil {
		t.Fatalf("standalone Process error: %v", err)
	}
	if !dred.Processed() {
		t.Fatal("standalone DRED did not reach processed state")
	}
	return dec, dred, packetInfo, n
}

func TestDecoderExplicitDREDDecodeMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}

	explicitDec, dred, packetInfo, n := prepareExplicitDREDDecodeParityState(t)
	explicitPCM := make([]float32, explicitDec.maxPacketSamples*explicitDec.channels)
	gotExplicit, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if gotExplicit != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", gotExplicit, n)
	}

	cachedDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
	if err != nil {
		t.Fatalf("NewDecoder(cached) error: %v", err)
	}
	if err := cachedDec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(cached) error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	cachedDec.setDREDDecoderBlob(blob)
	cachedSeed := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(packetInfo.packet, cachedSeed); err != nil {
		t.Fatalf("Decode(cached seed packet) error: %v", err)
	}
	cachedPCM := make([]float32, cachedDec.maxPacketSamples)
	gotCached, err := cachedDec.Decode(nil, cachedPCM)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotCached != n {
		t.Fatalf("Decode(nil)=%d want %d", gotCached, n)
	}

	assertFloat32ApproxEqual(t, explicitPCM[:n], cachedPCM[:n], "explicit vs cached pcm", 1e-4)
	gotState := explicitDec.dredPLC.Snapshot()
	wantState := cachedDec.dredPLC.Snapshot()
	if gotState.Blend != wantState.Blend ||
		gotState.LossCount != wantState.LossCount ||
		gotState.AnalysisGap != wantState.AnalysisGap ||
		gotState.AnalysisPos != wantState.AnalysisPos ||
		gotState.PredictPos != wantState.PredictPos ||
		gotState.FECFillPos != wantState.FECFillPos ||
		gotState.FECReadPos != wantState.FECReadPos ||
		gotState.FECSkip != wantState.FECSkip {
		t.Fatalf("explicit state header=%+v want %+v", gotState, wantState)
	}
	assertFloat32ApproxEqual(t, gotState.Features[:], wantState.Features[:], "explicit vs cached features", 1e-4)
	assertFloat32ApproxEqual(t, gotState.Cont[:], wantState.Cont[:], "explicit vs cached continuity", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PCM[:], wantState.PCM[:], "explicit vs cached pcm history", 1e-4)
}

func TestDecoderExplicitDREDDecodeMatchesLibopus(t *testing.T) {
	dec, dred, packetInfo, n := prepareExplicitDREDDecodeParityState(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit libopus pcm", 1e-4)
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesLibopus(t *testing.T) {
	dec, dred, packetInfo, n := prepareExplicitDREDDecodeParityState(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit second libopus pcm", 1e-4)
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}

	explicitDec, dred, packetInfo, n := prepareExplicitDREDDecodeParityState(t)
	explicitPCM0 := make([]float32, explicitDec.maxPacketSamples)
	if _, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	explicitPCM1 := make([]float32, explicitDec.maxPacketSamples)
	gotExplicit, err := explicitDec.decodeExplicitDREDFloat(dred, 2*n, explicitPCM1, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if gotExplicit != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", gotExplicit, n)
	}

	cachedDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
	if err != nil {
		t.Fatalf("NewDecoder(cached) error: %v", err)
	}
	if err := cachedDec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(cached) error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	cachedDec.setDREDDecoderBlob(blob)
	cachedSeed := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(packetInfo.packet, cachedSeed); err != nil {
		t.Fatalf("Decode(cached seed packet) error: %v", err)
	}
	cachedPCM0 := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(nil, cachedPCM0); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	cachedPCM1 := make([]float32, cachedDec.maxPacketSamples)
	gotCached, err := cachedDec.Decode(nil, cachedPCM1)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotCached != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotCached, n)
	}

	assertFloat32ApproxEqual(t, explicitPCM1[:n], cachedPCM1[:n], "explicit second vs cached second pcm", 1e-4)
	gotState := explicitDec.dredPLC.Snapshot()
	wantState := cachedDec.dredPLC.Snapshot()
	if gotState.Blend != wantState.Blend ||
		gotState.LossCount != wantState.LossCount ||
		gotState.AnalysisGap != wantState.AnalysisGap ||
		gotState.AnalysisPos != wantState.AnalysisPos ||
		gotState.PredictPos != wantState.PredictPos ||
		gotState.FECFillPos != wantState.FECFillPos ||
		gotState.FECReadPos != wantState.FECReadPos ||
		gotState.FECSkip != wantState.FECSkip {
		t.Fatalf("explicit second state header=%+v want %+v", gotState, wantState)
	}
	assertFloat32ApproxEqual(t, gotState.Features[:], wantState.Features[:], "explicit second vs cached second features", 1e-4)
	assertFloat32ApproxEqual(t, gotState.Cont[:], wantState.Cont[:], "explicit second vs cached second continuity", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PCM[:], wantState.PCM[:], "explicit second vs cached second pcm history", 1e-4)
}
