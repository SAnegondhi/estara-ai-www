package email

// Early Access Program email methods (ADR-085)

import "time"

// SendEarlyAccessReceived sends an acknowledgement email when an application is submitted.
func (s *Service) SendEarlyAccessReceived(to, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderHTML("early_access_received.html", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("early_access_received.txt", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "We received your Estara Insight application",
		HTML:    html,
		Text:    text,
	})
}

// SendEarlyAccessWelcome sends the welcome email with the one-time password setup link.
func (s *Service) SendEarlyAccessWelcome(to, firstName, setupURL string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderHTML("early_access_welcome.html", map[string]interface{}{
		"FirstName": firstName,
		"SetupURL":  setupURL,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("early_access_welcome.txt", map[string]interface{}{
		"FirstName": firstName,
		"SetupURL":  setupURL,
		"Year":      time.Now().Year(),
	})

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "You're in — set up your Estara Insight account",
		HTML:    html,
		Text:    text,
	})
}

// SendEarlyAccessRejected sends a rejection email.
func (s *Service) SendEarlyAccessRejected(to, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderHTML("early_access_rejected.html", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("early_access_rejected.txt", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "Your Estara Insight application",
		HTML:    html,
		Text:    text,
	})
}

// SendEarlyAccessSuspended sends a suspension notice email.
func (s *Service) SendEarlyAccessSuspended(to, firstName, reason string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderHTML("early_access_suspended.html", map[string]interface{}{
		"FirstName": firstName,
		"Reason":    reason,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("early_access_suspended.txt", map[string]interface{}{
		"FirstName": firstName,
		"Reason":    reason,
		"Year":      time.Now().Year(),
	})

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "Your Estara Insight access has been suspended",
		HTML:    html,
		Text:    text,
	})
}

// SendEarlyAccessRestored sends a restoration email.
func (s *Service) SendEarlyAccessRestored(to, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderHTML("early_access_restored.html", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("early_access_restored.txt", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "Your Estara Insight access has been restored",
		HTML:    html,
		Text:    text,
	})
}

// SendEarlyAccessTerminated sends a termination confirmation email.
func (s *Service) SendEarlyAccessTerminated(to, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderHTML("early_access_terminated.html", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("early_access_terminated.txt", map[string]interface{}{
		"FirstName": firstName,
		"Year":      time.Now().Year(),
	})

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "Your Estara Insight account has been closed",
		HTML:    html,
		Text:    text,
	})
}
