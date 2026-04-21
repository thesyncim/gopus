package gopus

// SetApplication updates the encoder application hint.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *Encoder) SetApplication(application Application) error {
	if err := validateMutableApplication(e.application, e.encodedOnce, application); err != nil {
		return err
	}
	return e.applyApplication(application)
}

// Application returns the current encoder application hint.
func (e *Encoder) Application() Application {
	return e.application
}

// applyApplication configures the encoder based on the application hint.
func (e *Encoder) applyApplication(app Application) error {
	settings, err := settingsForApplication(app)
	if err != nil {
		return err
	}
	e.application = app
	e.enc.SetLowDelay(settings.lowDelay)
	e.enc.SetVoIPApplication(settings.voip)
	e.enc.SetMode(settings.mode)
	e.enc.SetBandwidth(settings.bandwidth)
	e.enc.SetSignalType(settings.signal)
	return nil
}
