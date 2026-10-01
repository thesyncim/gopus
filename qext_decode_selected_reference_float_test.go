//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func selectedQEXTDecodeSequenceReference(outputChannels, frameSize int, packets [][]byte) ([]float32, []uint32, error) {
	binPath, err := libopusQEXTDecodeSingleHelper.Path(buildLibopusQEXTDecodeSingleHelper)
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 8, 0, 48000, 0, uint32(outputChannels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0)
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "qext stateful float decoder", "GOSO", 3)
	if err != nil {
		return nil, nil, err
	}
	samplesPerPacket := frameSize * outputChannels
	pcm := make([]float32, len(packets)*samplesPerPacket)
	if got := reader.Count(len(pcm)); got != len(pcm) {
		return nil, nil, fmt.Errorf("C PCM count %d, want %d", got, len(pcm))
	}
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if got := reader.Count(len(packets)); got != len(packets) {
		return nil, nil, fmt.Errorf("C record count %d, want %d", got, len(packets))
	}
	ranges := make([]uint32, len(packets))
	for i := range packets {
		status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
		if status != 0 || samples != uint32(frameSize) || offset != uint32(i*samplesPerPacket) {
			return nil, nil, fmt.Errorf("C frame %d: status=%d samples=%d offset=%d", i, status, samples, offset)
		}
		ranges[i] = finalRange
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	if err := reader.Err(); err != nil {
		return nil, nil, err
	}
	return pcm, ranges, nil
}
