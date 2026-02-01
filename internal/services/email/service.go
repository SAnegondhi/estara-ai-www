// Package email provides email sending functionality using Mailjet
package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/estara-ai/www/internal/config"
)

// Service handles email sending via Mailjet
type Service struct {
	cfg    *config.Config
	client *http.Client
	logger *slog.Logger
}

// NewService creates a new email service
func NewService(cfg *config.Config) *Service {
	return &Service{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
		logger: slog.Default().With("component", "email_service"),
	}
}

// EmailParams represents parameters for sending an email
type EmailParams struct {
	To       string
	ToName   string
	Subject  string
	HTML     string
	Text     string
	ReplyTo  *EmailAddress
	CC       []string
	BCC      []string
}

// EmailAddress represents an email address with optional name
type EmailAddress struct {
	Email string
	Name  string
}

// Result represents the result of sending an email
type Result struct {
	Success   bool
	MessageID string
	Error     string
}

// mailjetRequest represents the Mailjet API request body
type mailjetRequest struct {
	Messages []mailjetMessage `json:"Messages"`
}

type mailjetMessage struct {
	From     mailjetAddress   `json:"From"`
	To       []mailjetAddress `json:"To"`
	CC       []mailjetAddress `json:"Cc,omitempty"`
	BCC      []mailjetAddress `json:"Bcc,omitempty"`
	ReplyTo  *mailjetAddress  `json:"ReplyTo,omitempty"`
	Subject  string           `json:"Subject"`
	TextPart string           `json:"TextPart"`
	HTMLPart string           `json:"HTMLPart"`
}

type mailjetAddress struct {
	Email string `json:"Email"`
	Name  string `json:"Name"`
}

// Send sends an email via Mailjet
func (s *Service) Send(params EmailParams) (*Result, error) {
	if s.cfg.Email.APIKey == "" || s.cfg.Email.APISecret == "" {
		s.logger.Warn("Mailjet not configured (MAILJET_API_KEY/MAILJET_SECRET_KEY missing)")
		return &Result{Success: false, Error: "Email service not configured"}, nil
	}

	// Build message
	msg := mailjetMessage{
		From: mailjetAddress{
			Email: s.cfg.Email.FromEmail,
			Name:  s.cfg.Email.FromName,
		},
		To: []mailjetAddress{
			{Email: params.To, Name: params.ToName},
		},
		Subject:  params.Subject,
		TextPart: params.Text,
		HTMLPart: params.HTML,
	}

	// Add CC recipients
	for _, cc := range params.CC {
		msg.CC = append(msg.CC, mailjetAddress{Email: cc, Name: cc})
	}

	// Add BCC recipients
	for _, bcc := range params.BCC {
		msg.BCC = append(msg.BCC, mailjetAddress{Email: bcc, Name: bcc})
	}

	// Add reply-to
	if params.ReplyTo != nil {
		name := params.ReplyTo.Name
		if name == "" {
			name = params.ReplyTo.Email
		}
		msg.ReplyTo = &mailjetAddress{Email: params.ReplyTo.Email, Name: name}
	}

	// Build request
	reqBody := mailjetRequest{Messages: []mailjetMessage{msg}}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.mailjet.com/v3.1/send", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(s.cfg.Email.APIKey, s.cfg.Email.APISecret)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var result struct {
		Messages []struct {
			Status string `json:"Status"`
			To     []struct {
				MessageID int64 `json:"MessageID"`
			} `json:"To"`
		} `json:"Messages"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return &Result{Success: false, Error: fmt.Sprintf("Mailjet error: %d", resp.StatusCode)}, nil
	}

	var messageID string
	if len(result.Messages) > 0 && len(result.Messages[0].To) > 0 {
		messageID = fmt.Sprintf("%d", result.Messages[0].To[0].MessageID)
	}

	s.logger.Info("email sent", "to", params.To, "subject", params.Subject)
	return &Result{Success: true, MessageID: messageID}, nil
}

// SendPasswordReset sends a password reset email
func (s *Service) SendPasswordReset(to, resetToken, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "User"
	}

	resetURL := fmt.Sprintf("%s/reset-password/%s", s.cfg.Server.MarketingURL, resetToken)

	html := s.renderPasswordResetHTML(firstName, resetURL)
	text := s.renderPasswordResetText(firstName, resetURL)

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "Reset Your Estara AI Password",
		HTML:    html,
		Text:    text,
	})
}

// SendVerificationCode sends an email verification code
func (s *Service) SendVerificationCode(to, code, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	html := s.renderVerificationCodeHTML(firstName, code)
	text := s.renderVerificationCodeText(firstName, code)

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: "Your Estara AI Verification Code",
		HTML:    html,
		Text:    text,
	})
}

// SendSubscriptionActivated sends a subscription activation confirmation email.
func (s *Service) SendSubscriptionActivated(to, firstName string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	subject := "Your Estara AI subscription is active"
	html := s.renderBillingHTML(firstName, subject, "Your subscription is now active. You can start using Estara AI immediately.")
	text := s.renderBillingText(firstName, subject, "Your subscription is now active. You can start using Estara AI immediately.")

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// SendSubscriptionCancelled sends a subscription cancellation email.
func (s *Service) SendSubscriptionCancelled(to, firstName string, endDate *time.Time) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	subject := "Your Estara AI subscription was cancelled"
	message := "Your subscription has been cancelled."
	if endDate != nil {
		message = fmt.Sprintf("Your subscription has been cancelled and will remain active until %s.", endDate.Format("January 2, 2006"))
	}

	html := s.renderBillingHTML(firstName, subject, message)
	text := s.renderBillingText(firstName, subject, message)

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// SendPaymentSucceeded sends a successful payment email.
func (s *Service) SendPaymentSucceeded(to, firstName string, amount int64, currency string, invoiceURL string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	subject := "Payment received for your Estara AI subscription"
	amountText := formatCurrency(amount, currency)
	message := fmt.Sprintf("We have received your payment of %s. Thank you!", amountText)
	if invoiceURL != "" {
		message = fmt.Sprintf("%s You can view your invoice here: %s", message, invoiceURL)
	}

	html := s.renderBillingHTML(firstName, subject, message)
	text := s.renderBillingText(firstName, subject, message)

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// SendPaymentFailed sends a payment failure email.
func (s *Service) SendPaymentFailed(to, firstName string, amount int64, currency string) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	subject := "Payment failed for your Estara AI subscription"
	amountText := formatCurrency(amount, currency)
	message := fmt.Sprintf("We could not process your payment of %s. Please update your billing details to avoid service interruption.", amountText)

	html := s.renderBillingHTML(firstName, subject, message)
	text := s.renderBillingText(firstName, subject, message)

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// SendTrialEnding sends a trial ending reminder email.
func (s *Service) SendTrialEnding(to, firstName string, endDate time.Time) (*Result, error) {
	if firstName == "" {
		firstName = "there"
	}

	subject := "Your Estara AI trial is ending soon"
	message := fmt.Sprintf("Your trial ends on %s. Upgrade now to keep uninterrupted access.", endDate.Format("January 2, 2006"))

	html := s.renderBillingHTML(firstName, subject, message)
	text := s.renderBillingText(firstName, subject, message)

	return s.Send(EmailParams{
		To:      to,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

func (s *Service) renderBillingHTML(firstName, headline, message string) string {
	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{.Headline}}</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Estara AI</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">{{.Headline}}</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hello {{.FirstName}},</h2>
    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">{{.Message}}</p>
    <p style="margin-bottom: 0; font-size: 14px; color: #666;">
      If you have questions, reply to this email and our team will help.
    </p>
  </div>
</body>
</html>`

	t, err := template.New("billing").Parse(tmpl)
	if err != nil {
		return message
	}

	data := struct {
		FirstName string
		Headline  string
		Message   string
	}{
		FirstName: firstName,
		Headline:  headline,
		Message:   message,
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return message
	}

	return buf.String()
}

func (s *Service) renderBillingText(firstName, headline, message string) string {
	return fmt.Sprintf("Hello %s,\n\n%s\n\n%s\n", firstName, headline, message)
}

func formatCurrency(amount int64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	return fmt.Sprintf("%.2f %s", float64(amount)/100, strings.ToUpper(currency))
}

func (s *Service) renderPasswordResetHTML(firstName, resetURL string) string {
	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Reset Your Password</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Estara AI</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">Reset Your Password</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hello {{.FirstName}},</h2>

    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">
      We received a request to reset your password for your Estara AI account. If you made this request, click the button below to reset your password.
    </p>

    <div style="text-align: center; margin: 30px 0;">
      <a href="{{.ResetURL}}"
         style="display: inline-block; padding: 15px 30px; background: #D4AF37; color: white; text-decoration: none; border-radius: 8px; font-size: 16px; font-weight: bold;">
        Reset Your Password
      </a>
    </div>

    <p style="margin-bottom: 20px; font-size: 14px; color: #666;">
      If the button doesn't work, you can copy and paste this link into your browser:
    </p>

    <p style="word-break: break-all; background: #f8f9fa; padding: 15px; border-radius: 5px; font-family: monospace; font-size: 14px; color: #666;">
      {{.ResetURL}}
    </p>

    <div style="border-top: 1px solid #e1e8ed; margin-top: 30px; padding-top: 20px;">
      <p style="font-size: 14px; color: #666; margin-bottom: 10px;">
        <strong>Important Security Information:</strong>
      </p>
      <ul style="font-size: 14px; color: #666; margin: 0; padding-left: 20px;">
        <li>This link will expire in 30 minutes for security reasons</li>
        <li>If you didn't request this reset, please ignore this email</li>
        <li>Never share this link with anyone</li>
      </ul>
    </div>

    <div style="margin-top: 30px; text-align: center; border-top: 1px solid #e1e8ed; padding-top: 20px;">
      <p style="font-size: 12px; color: #999; margin: 0;">
        This email was sent from a notification-only address that cannot accept incoming email.
      </p>
      <p style="font-size: 12px; color: #999; margin: 10px 0 0 0;">
        &copy; {{.Year}} Estara AI. All rights reserved.
      </p>
    </div>
  </div>
</body>
</html>`

	t := template.Must(template.New("password_reset").Parse(tmpl))
	var buf bytes.Buffer
	_ = t.Execute(&buf, map[string]interface{}{
		"FirstName": firstName,
		"ResetURL":  resetURL,
		"Year":      time.Now().Year(),
	})
	return buf.String()
}

func (s *Service) renderPasswordResetText(firstName, resetURL string) string {
	return fmt.Sprintf(`Hello %s,

We received a request to reset your password for your Estara AI account.

To reset your password, visit this link:
%s

This link will expire in 30 minutes for security reasons.

If you didn't request this reset, please ignore this email.

© %d Estara AI. All rights reserved.`, firstName, resetURL, time.Now().Year())
}

func (s *Service) renderVerificationCodeHTML(firstName, code string) string {
	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Verify Your Email</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Estara AI</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">Email Verification</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hi {{.FirstName}},</h2>

    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">
      Thank you for signing up for Estara AI! Please use the verification code below to complete your registration:
    </p>

    <div style="text-align: center; margin: 30px 0;">
      <div style="display: inline-block; background: #f8f9fa; padding: 20px 40px; border-radius: 10px; border: 2px dashed #D4AF37;">
        <span style="font-size: 36px; font-weight: bold; letter-spacing: 8px; color: #333;">{{.Code}}</span>
      </div>
    </div>

    <p style="margin-bottom: 20px; font-size: 14px; color: #666; text-align: center;">
      This code will expire in <strong>10 minutes</strong>.
    </p>

    <div style="border-top: 1px solid #e1e8ed; margin-top: 30px; padding-top: 20px;">
      <p style="font-size: 14px; color: #666; margin-bottom: 10px;">
        <strong>Didn't request this code?</strong>
      </p>
      <p style="font-size: 14px; color: #666; margin: 0;">
        If you didn't try to sign up for Estara AI, you can safely ignore this email. Someone may have entered your email address by mistake.
      </p>
    </div>

    <div style="margin-top: 30px; text-align: center; border-top: 1px solid #e1e8ed; padding-top: 20px;">
      <p style="font-size: 12px; color: #999; margin: 0;">
        This email was sent from a notification-only address that cannot accept incoming email.
      </p>
      <p style="font-size: 12px; color: #999; margin: 10px 0 0 0;">
        &copy; {{.Year}} Estara AI. All rights reserved.
      </p>
    </div>
  </div>
</body>
</html>`

	t := template.Must(template.New("verification_code").Parse(tmpl))
	var buf bytes.Buffer
	_ = t.Execute(&buf, map[string]interface{}{
		"FirstName": firstName,
		"Code":      code,
		"Year":      time.Now().Year(),
	})
	return buf.String()
}

func (s *Service) renderVerificationCodeText(firstName, code string) string {
	return fmt.Sprintf(`Hi %s,

Thank you for signing up for Estara AI!

Your verification code is: %s

This code will expire in 10 minutes.

If you didn't request this code, you can safely ignore this email.

© %d Estara AI. All rights reserved.`, firstName, code, time.Now().Year())
}
