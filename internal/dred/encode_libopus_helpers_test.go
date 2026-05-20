//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package dred

import (
	"fmt"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustest"
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

	payload := libopustest.NewOraclePayload(libopusDREDEncodePayloadInputMagic,
		uint32(q0),
		uint32(dQ),
		uint32(qmax),
		uint32(maxChunks),
		uint32(maxBytes),
		uint32(latentsFill),
		uint32(dredOffset),
		uint32(latentOffset),
		uint32(lastExtraDREDOffset),
	)
	payload.Float32s(state[:MaxFrames*StateDim]...)
	payload.Float32s(latents[:MaxFrames*LatentDim]...)
	payload.Raw(activity[:])

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "dred encode", libopusDREDEncodePayloadOutputMagic)
	if err != nil {
		return libopusDREDEncodePayloadInfo{}, err
	}
	info := libopusDREDEncodePayloadInfo{
		LastExtraDREDOffset: int(reader.U32()),
	}
	n := int(reader.U32())
	info.Payload = append([]byte(nil), reader.Bytes(n)...)
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDEncodePayloadInfo{}, err
	}
	return info, nil
}
