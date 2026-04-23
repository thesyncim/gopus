//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package dred

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
)

const (
	libopusDREDEncodePayloadInputMagic  = "GDPI"
	libopusDREDEncodePayloadOutputMagic = "GDPO"
)

type libopusDREDEncodePayloadInfo struct {
	Payload             []byte
	LastExtraDREDOffset int
}

var (
	libopusDREDEncodePayloadHelperOnce sync.Once
	libopusDREDEncodePayloadHelperPath string
	libopusDREDEncodePayloadHelperErr  error
)

func getLibopusDREDEncodePayloadHelperPath() (string, error) {
	libopusDREDEncodePayloadHelperOnce.Do(func() {
		libopusDREDEncodePayloadHelperPath, libopusDREDEncodePayloadHelperErr = buildLibopusDREDHelper("libopus_dred_encode_payload_info.c", "gopus_libopus_dred_encode_payload")
	})
	if libopusDREDEncodePayloadHelperErr != nil {
		return "", libopusDREDEncodePayloadHelperErr
	}
	return libopusDREDEncodePayloadHelperPath, nil
}

func probeLibopusDREDEncodePayload(q0, dQ, qmax, maxChunks, maxBytes, latentsFill, dredOffset, latentOffset, lastExtraDREDOffset int, state, latents []float32, activity [ActivityHistorySize]byte) (libopusDREDEncodePayloadInfo, error) {
	binPath, err := getLibopusDREDEncodePayloadHelperPath()
	if err != nil {
		return libopusDREDEncodePayloadInfo{}, err
	}
	if len(state) < MaxFrames*StateDim || len(latents) < MaxFrames*LatentDim {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("insufficient state/latent history")
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDREDEncodePayloadInputMagic)
	for _, v := range []uint32{
		1,
		uint32(q0),
		uint32(dQ),
		uint32(qmax),
		uint32(maxChunks),
		uint32(maxBytes),
		uint32(latentsFill),
		uint32(dredOffset),
		uint32(latentOffset),
		uint32(lastExtraDREDOffset),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDREDEncodePayloadInfo{}, fmt.Errorf("encode helper header: %w", err)
		}
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeBits(state[:MaxFrames*StateDim]); err != nil {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("encode helper state: %w", err)
	}
	if err := writeBits(latents[:MaxFrames*LatentDim]); err != nil {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("encode helper latents: %w", err)
	}
	if _, err := payload.Write(activity[:]); err != nil {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("encode helper activity: %w", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("run encode helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	if len(out) < 16 || string(out[:4]) != libopusDREDEncodePayloadOutputMagic {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("unexpected encode helper output")
	}
	info := libopusDREDEncodePayloadInfo{
		LastExtraDREDOffset: int(binary.LittleEndian.Uint32(out[8:12])),
	}
	n := int(binary.LittleEndian.Uint32(out[12:16]))
	if len(out) < 16+n {
		return libopusDREDEncodePayloadInfo{}, fmt.Errorf("truncated encode helper payload")
	}
	info.Payload = append([]byte(nil), out[16:16+n]...)
	return info, nil
}
