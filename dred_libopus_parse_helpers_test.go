//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDREDParseInputMagic  = "GODI"
	libopusDREDParseOutputMagic = "GODO"
)

type libopusDREDParseInfo struct {
	availableSamples int
	dredEndSamples   int
}

type libopusDREDDecodeInfo struct {
	availableSamples int
	dredEndSamples   int
	processStage     int
	nbLatents        int
	dredOffset       int
	state            [internaldred.StateDim]float32
	latents          []float32
}

var (
	libopusDREDParseHelper  libopustest.HelperCache
	libopusDREDDecodeHelper libopustest.HelperCache
)

func getLibopusDREDParseHelperPath() (string, error) {
	return libopusDREDParseHelper.Path(func() (string, error) {
		return buildLibopusDREDHelper("libopus_dred_parse_info.c", "gopus_libopus_dred_parse", false)
	})
}

func getLibopusDREDDecodeHelperPath() (string, error) {
	return libopusDREDDecodeHelper.Path(func() (string, error) {
		return buildLibopusDREDHelper("libopus_dred_decode_info.c", "gopus_libopus_dred_decode", true)
	})
}

func probeLibopusDREDParse(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDParseInfo, error) {
	binPath, err := getLibopusDREDParseHelperPath()
	if err != nil {
		return libopusDREDParseInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDParseInfo{}, fmt.Errorf("run dred helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred parse", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDParseInfo{}, err
	}
	reader.ExpectRemaining(8)
	ret := int(reader.I32())
	dredEnd := int(reader.I32())
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDParseInfo{}, err
	}
	return libopusDREDParseInfo{
		availableSamples: ret,
		dredEndSamples:   dredEnd,
	}, nil
}

func probeLibopusDREDDecode(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDDecodeInfo, error) {
	binPath, err := getLibopusDREDDecodeHelperPath()
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDDecodeInfo{}, fmt.Errorf("run dred decode helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred decode", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	info := libopusDREDDecodeInfo{
		availableSamples: int(reader.I32()),
		dredEndSamples:   int(reader.I32()),
		processStage:     int(reader.I32()),
		nbLatents:        int(reader.I32()),
		dredOffset:       int(reader.I32()),
	}

	for i := range info.state {
		info.state[i] = reader.Float32()
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		info.latents[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDDecodeInfo{}, err
	}
	return info, nil
}
