package testvectors

func qualityDelaySearchWindow(frameSize int) int {
	if frameSize < 240 {
		return 240
	}
	if frameSize > 960 {
		return 960
	}
	return frameSize
}
