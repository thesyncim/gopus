package gopus

import "testing"

func addPacketMutationFuzzSeeds(f *testing.F) {
	f.Add([]byte{0xF8, 0x11, 0x22, 0x33}, uint8(0), uint8(1))
	f.Add([]byte{GenerateTOC(31, false, 0), 0xAA, 0xBB, 0xCC}, uint8(8), uint8(1))

	if selfDelimited, err := makeSelfDelimitedPacket(testCELTPacket()); err == nil {
		f.Add(append([]byte(nil), selfDelimited...), uint8(12), uint8(2))

		multistream := append(append([]byte{}, selfDelimited...), testStereoCELTPacket()...)
		f.Add(multistream, uint8(16), uint8(2))
	}
}

func FuzzPacketMutationHelpers_NoPanic(f *testing.F) {
	addPacketMutationFuzzSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, extra uint8, streamHint uint8) {
		if len(data) == 0 {
			return
		}
		if len(data) > 4096 {
			data = data[:4096]
		}

		padDelta := int(extra % 32)
		if len(data)+padDelta > 4096 {
			padDelta = 4096 - len(data)
		}
		newLen := len(data) + padDelta
		numStreams := int(streamHint%4) + 1

		packetBuf := make([]byte, newLen)
		copy(packetBuf, data)
		_ = PacketPad(packetBuf, len(data), newLen)

		packetCopy := append([]byte(nil), data...)
		_, _ = PacketUnpad(packetCopy, len(packetCopy))

		multistreamBuf := make([]byte, newLen)
		copy(multistreamBuf, data)
		_ = MultistreamPacketPad(multistreamBuf, len(data), newLen, numStreams)

		multistreamCopy := append([]byte(nil), data...)
		_, _ = MultistreamPacketUnpad(multistreamCopy, len(multistreamCopy), numStreams)

		repacketizer := NewRepacketizer()
		_ = repacketizer.Cat(data)
		if frames := repacketizer.NumFrames(); frames > 0 {
			out := make([]byte, 4096)
			_, _ = repacketizer.Out(out)
			_, _ = repacketizer.OutRange(0, frames, out)
		}

		_, _, _, _, _, _ = parseSelfDelimitedPacketAndPadding(data)
	})
}
