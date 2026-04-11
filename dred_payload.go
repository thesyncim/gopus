package gopus

import internaldred "github.com/thesyncim/gopus/internal/dred"

// findDREDPayload mirrors libopus dred_find_payload(): it scans packet
// extensions for the temporary DRED extension and returns the payload with the
// experimental prefix stripped. frameOffset is reported in 2.5 ms units.
func findDREDPayload(packet []byte) (payload []byte, frameOffset int, ok bool, err error) {
	info, _, padding, paddingFrameCount, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		return nil, 0, false, err
	}
	if len(padding) == 0 || paddingFrameCount <= 0 {
		return nil, 0, false, nil
	}

	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, padding, paddingFrameCount)
	for {
		var ext packetExtensionData
		more, iterErr := iter.next(&ext)
		if iterErr != nil {
			return nil, 0, false, iterErr
		}
		if !more {
			return nil, 0, false, nil
		}
		if ext.ID != internaldred.ExtensionID {
			continue
		}
		if !internaldred.ValidExperimentalPayload(ext.Data) {
			continue
		}
		return ext.Data[internaldred.ExperimentalHeaderBytes:], ext.Frame * info.TOC.FrameSize / 120, true, nil
	}
}
