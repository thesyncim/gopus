package gopus

// ExpertFrameDuration mirrors libopus OPUS_SET/GET_EXPERT_FRAME_DURATION values.
type ExpertFrameDuration int

const (
	ExpertFrameDurationArg   ExpertFrameDuration = 5000
	ExpertFrameDuration2_5Ms ExpertFrameDuration = 5001
	ExpertFrameDuration5Ms   ExpertFrameDuration = 5002
	ExpertFrameDuration10Ms  ExpertFrameDuration = 5003
	ExpertFrameDuration20Ms  ExpertFrameDuration = 5004
	ExpertFrameDuration40Ms  ExpertFrameDuration = 5005
	ExpertFrameDuration60Ms  ExpertFrameDuration = 5006
	ExpertFrameDuration80Ms  ExpertFrameDuration = 5007
	ExpertFrameDuration100Ms ExpertFrameDuration = 5008
	ExpertFrameDuration120Ms ExpertFrameDuration = 5009
)

func validApplication(application Application) bool {
	switch application {
	case ApplicationVoIP, ApplicationAudio, ApplicationLowDelay, ApplicationRestrictedSilk, ApplicationRestrictedCelt:
		return true
	default:
		return false
	}
}

func validateBitrate(bitrate, max int) error {
	if bitrate < 6000 || bitrate > max {
		return ErrInvalidBitrate
	}
	return nil
}

func validateComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	return nil
}

func validateBitrateMode(mode BitrateMode) error {
	switch mode {
	case BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR:
		return nil
	default:
		return ErrInvalidBitrateMode
	}
}

func validSignal(signal Signal) bool {
	switch signal {
	case SignalAuto, SignalVoice, SignalMusic:
		return true
	default:
		return false
	}
}

func validateSignal(signal Signal) error {
	if !validSignal(signal) {
		return ErrInvalidSignal
	}
	return nil
}

func validBandwidth(bandwidth Bandwidth) bool {
	switch bandwidth {
	case BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperwideband, BandwidthFullband:
		return true
	default:
		return false
	}
}

func validateBandwidth(bandwidth Bandwidth) error {
	if !validBandwidth(bandwidth) {
		return ErrInvalidBandwidth
	}
	return nil
}

func validateForceChannels(channels int) error {
	if channels != -1 && channels != 1 && channels != 2 {
		return ErrInvalidForceChannels
	}
	return nil
}

func validatePacketLoss(lossPercent int) error {
	if lossPercent < 0 || lossPercent > 100 {
		return ErrInvalidPacketLoss
	}
	return nil
}

func validateLSBDepth(depth int) error {
	if depth < 8 || depth > 24 {
		return ErrInvalidLSBDepth
	}
	return nil
}

func validFrameSize(samples int) bool {
	switch samples {
	case 120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760:
		return true
	default:
		return false
	}
}

func validateFrameSize(samples int, application Application) error {
	if !validFrameSize(samples) {
		return ErrInvalidFrameSize
	}
	if application == ApplicationRestrictedSilk && samples < 480 {
		return ErrInvalidFrameSize
	}
	return nil
}

func validExpertFrameDuration(duration ExpertFrameDuration) bool {
	switch duration {
	case ExpertFrameDurationArg,
		ExpertFrameDuration2_5Ms,
		ExpertFrameDuration5Ms,
		ExpertFrameDuration10Ms,
		ExpertFrameDuration20Ms,
		ExpertFrameDuration40Ms,
		ExpertFrameDuration60Ms,
		ExpertFrameDuration80Ms,
		ExpertFrameDuration100Ms,
		ExpertFrameDuration120Ms:
		return true
	default:
		return false
	}
}

func expertFrameDurationFrameSize(duration ExpertFrameDuration) int {
	switch duration {
	case ExpertFrameDuration2_5Ms:
		return 120
	case ExpertFrameDuration5Ms:
		return 240
	case ExpertFrameDuration10Ms:
		return 480
	case ExpertFrameDuration20Ms:
		return 960
	case ExpertFrameDuration40Ms:
		return 1920
	case ExpertFrameDuration60Ms:
		return 2880
	case ExpertFrameDuration80Ms:
		return 3840
	case ExpertFrameDuration100Ms:
		return 4800
	case ExpertFrameDuration120Ms:
		return 5760
	default:
		return 0
	}
}

func setExpertFrameDuration(duration ExpertFrameDuration, current *ExpertFrameDuration, setFrameSize func(int) error) error {
	if !validExpertFrameDuration(duration) {
		return ErrInvalidArgument
	}
	*current = duration
	if duration == ExpertFrameDurationArg {
		return nil
	}
	return setFrameSize(expertFrameDurationFrameSize(duration))
}

func lookaheadSamples(sampleRate int, application Application) int {
	base := sampleRate / 400
	if application == ApplicationLowDelay || application == ApplicationRestrictedCelt {
		return base
	}
	return base + sampleRate/250
}
