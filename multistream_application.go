package gopus

// SetApplication updates the encoder application hint.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *MultistreamEncoder) SetApplication(application Application) error {
	if err := validateMutableApplication(e.application, e.encodedOnce, application); err != nil {
		return err
	}
	return e.applyApplication(application)
}

// Application returns the current encoder application hint.
func (e *MultistreamEncoder) Application() Application {
	return e.application
}

// applyApplication records the application hint and forwards per-stream policy.
//
// Match libopus multistream OPUS_SET_APPLICATION forwarding semantics while
// preserving bitrate/complexity controls.
func (e *MultistreamEncoder) applyApplication(app Application) error {
	settings, err := settingsForApplication(app)
	if err != nil {
		return err
	}
	e.application = app
	e.enc.SetLowDelay(settings.lowDelay)
	e.enc.SetVoIPApplication(settings.voip)
	e.enc.SetRestrictedSilkApplication(app == ApplicationRestrictedSilk)
	e.enc.SetMode(settings.mode)
	if settings.setBandwidth {
		e.enc.SetBandwidth(settings.bandwidth)
	}
	e.enc.SetSignal(settings.signal)
	return nil
}
