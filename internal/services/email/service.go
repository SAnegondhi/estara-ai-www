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

// WelcomeEmailParams holds parameters for the welcome email
type WelcomeEmailParams struct {
	To        string
	FirstName string
	Tier      string // e.g., "PROFESSIONAL", "INVESTOR", "FREE"
}

// SendWelcomeEmail sends a rich, tier-specific welcome email with app link.
// ADR-067: Enhanced welcome email with features list, app link, and quick start guide.
func (s *Service) SendWelcomeEmail(params WelcomeEmailParams) (*Result, error) {
	firstName := params.FirstName
	if firstName == "" {
		firstName = "Investor"
	}

	tier := strings.ToUpper(params.Tier)
	if tier == "" {
		tier = "FREE"
	}

	// Get app URL from config
	appURL := s.cfg.Server.ClientURL
	if appURL == "" {
		appURL = "https://insight.estara-ai.com"
	}

	// Tier-specific content
	tierInfo := getTierInfo(tier)
	isPaid := tierInfo.IsPaid

	var subject string
	if isPaid {
		subject = fmt.Sprintf("Welcome to Estara AI - Your %s Plan is Active!", tierInfo.Name)
	} else {
		subject = "Welcome to Estara AI - Your Free Trial Begins!"
	}

	html := s.renderWelcomeHTML(firstName, tierInfo, appURL, isPaid)
	text := s.renderWelcomeText(firstName, tierInfo, appURL, isPaid)

	return s.Send(EmailParams{
		To:      params.To,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// InvoiceEmailParams holds parameters for the invoice email
type InvoiceEmailParams struct {
	To            string
	FirstName     string
	InvoiceNumber string
	Amount        int64  // in cents
	Currency      string
	DueDate       *time.Time
	InvoiceURL    string // Stripe hosted invoice URL
	PDFURL        string // Invoice PDF download URL
}

// SendInvoiceEmail sends an invoice notification email.
// ADR-067: Send invoice before charge for transparency and chargeback protection.
func (s *Service) SendInvoiceEmail(params InvoiceEmailParams) (*Result, error) {
	firstName := params.FirstName
	if firstName == "" {
		firstName = "Customer"
	}

	amountText := formatCurrency(params.Amount, params.Currency)
	subject := fmt.Sprintf("Invoice %s from Estara AI", params.InvoiceNumber)

	html := s.renderInvoiceHTML(firstName, params.InvoiceNumber, amountText, params.DueDate, params.InvoiceURL, params.PDFURL)
	text := s.renderInvoiceText(firstName, params.InvoiceNumber, amountText, params.DueDate, params.InvoiceURL)

	return s.Send(EmailParams{
		To:      params.To,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// ReceiptEmailParams holds parameters for the receipt email
type ReceiptEmailParams struct {
	To            string
	FirstName     string
	ReceiptNumber string
	Amount        int64  // in cents
	Currency      string
	CardBrand     string // e.g., "Visa", "Mastercard"
	CardLast4     string // e.g., "4242"
	PaidAt        time.Time
	ReceiptURL    string // Stripe receipt URL
	Description   string // Product description
}

// SendReceiptEmail sends a detailed receipt email after payment.
// ADR-067: Receipt with payment method details for records and chargeback protection.
func (s *Service) SendReceiptEmail(params ReceiptEmailParams) (*Result, error) {
	firstName := params.FirstName
	if firstName == "" {
		firstName = "Customer"
	}

	amountText := formatCurrency(params.Amount, params.Currency)
	subject := fmt.Sprintf("Receipt %s - Payment Received", params.ReceiptNumber)

	html := s.renderReceiptHTML(firstName, params.ReceiptNumber, amountText, params.CardBrand, params.CardLast4, params.PaidAt, params.ReceiptURL, params.Description)
	text := s.renderReceiptText(firstName, params.ReceiptNumber, amountText, params.CardBrand, params.CardLast4, params.PaidAt, params.ReceiptURL)

	return s.Send(EmailParams{
		To:      params.To,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// RenewalReminderParams holds parameters for the renewal reminder email
type RenewalReminderParams struct {
	To          string
	FirstName   string
	RenewalDate time.Time
	Amount      int64  // in cents
	Currency    string
	PlanName    string
	ManageURL   string // URL to manage subscription
}

// SendRenewalReminder sends a renewal reminder email before automatic charge.
// ADR-067: Advance notice of recurring charges for compliance and chargeback protection.
func (s *Service) SendRenewalReminder(params RenewalReminderParams) (*Result, error) {
	firstName := params.FirstName
	if firstName == "" {
		firstName = "Customer"
	}

	amountText := formatCurrency(params.Amount, params.Currency)
	subject := fmt.Sprintf("Upcoming renewal: %s on %s", amountText, params.RenewalDate.Format("January 2"))

	html := s.renderRenewalReminderHTML(firstName, params.PlanName, amountText, params.RenewalDate, params.ManageURL)
	text := s.renderRenewalReminderText(firstName, params.PlanName, amountText, params.RenewalDate, params.ManageURL)

	return s.Send(EmailParams{
		To:      params.To,
		ToName:  firstName,
		Subject: subject,
		HTML:    html,
		Text:    text,
	})
}

// TierInfo holds tier-specific display information
type TierInfo struct {
	Name     string
	Features []string
	IsPaid   bool
}

func getTierInfo(tier string) TierInfo {
	switch tier {
	case "PROFESSIONAL", "PROFESSIONAL_ACCESS":
		return TierInfo{
			Name:   "Professional",
			IsPaid: true,
			Features: []string{
				"<strong>60 AI-Powered Reports</strong> per year",
				"<strong>Unlimited Market Analysis</strong>",
				"<strong>Investment Planning Tools</strong>",
				"<strong>Priority Support</strong>",
			},
		}
	case "INVESTOR", "INVESTOR_ACCESS":
		return TierInfo{
			Name:   "Investor",
			IsPaid: true,
			Features: []string{
				"<strong>24 AI-Powered Reports</strong> per year",
				"<strong>Market Analysis</strong>",
				"<strong>Investment Planning</strong>",
				"<strong>Email Support</strong>",
			},
		}
	case "API_INVESTOR", "AAPI_INVESTOR":
		return TierInfo{
			Name:   "API Investor",
			IsPaid: true,
			Features: []string{
				"<strong>36 AI-Powered Reports</strong> per year",
				"<strong>API Access</strong>",
				"<strong>Advanced Analytics</strong>",
				"<strong>Priority Support</strong>",
			},
		}
	case "API_ALLOCATOR", "AAPI_ALLOCATOR":
		return TierInfo{
			Name:   "API Allocator",
			IsPaid: true,
			Features: []string{
				"<strong>100 AI-Powered Reports</strong> per year",
				"<strong>Full API Access</strong>",
				"<strong>White-Label Reports</strong>",
				"<strong>Dedicated Support</strong>",
			},
		}
	default: // FREE
		return TierInfo{
			Name:   "Free Trial",
			IsPaid: false,
			Features: []string{
				"<strong>15-Day Full Access</strong> - Experience all features risk-free",
				"<strong>AI Market Analysis</strong> - Get insights for your first market",
				"<strong>Property Search</strong> - Discover investment opportunities",
				"<strong>Investment Planning</strong> - Build your investment strategy",
			},
		}
	}
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

// ADR-067: New email templates for billing completeness

func (s *Service) renderWelcomeHTML(firstName string, tierInfo TierInfo, appURL string, isPaid bool) string {
	featuresHTML := ""
	for _, f := range tierInfo.Features {
		featuresHTML += fmt.Sprintf(`<li style="margin-bottom: 8px;">%s</li>`, f)
	}

	badgeText := tierInfo.Name
	if !isPaid {
		badgeText = "15-Day Free Trial Started"
	}

	guaranteeSection := ""
	if isPaid {
		guaranteeSection = `
        <div style="background: #ecfdf5; padding: 15px; border-radius: 8px; margin-top: 25px; border-left: 4px solid #22c55e;">
          <p style="margin: 0; font-size: 14px; color: #666;">
            <strong>✅ 14-Day Money-Back Guarantee:</strong> Not satisfied? Request a full refund within 14 days of your purchase—no questions asked.
          </p>
        </div>`
	} else {
		guaranteeSection = fmt.Sprintf(`
        <div style="background: #fff8e1; padding: 15px; border-radius: 8px; margin-top: 25px; border-left: 4px solid #D4AF37;">
          <p style="margin: 0; font-size: 14px; color: #666;">
            <strong>💡 Upgrade Anytime:</strong> Love what you see? Upgrade to a paid plan for more reports and advanced features.
            <a href="%s/pricing" style="color: #D4AF37;">View Plans →</a>
          </p>
        </div>`, s.cfg.Server.MarketingURL)
	}

	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Welcome to Estara AI</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Welcome to Estara AI!</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">AI-Powered Real Estate Investment Analysis</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hello {{.FirstName}},</h2>

    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">
      {{if .IsPaid}}Thank you for subscribing to Estara AI! Your <strong>{{.TierName}}</strong> plan is now active.{{else}}Thank you for joining Estara AI! Your <strong>15-day free trial</strong> has started.{{end}}
    </p>

    <div style="background: #f8f9fa; padding: 20px; border-radius: 8px; margin: 25px 0;">
      <h3 style="margin: 0 0 15px 0; color: #333; font-size: 18px;">🎉 Your {{.BadgeText}} Includes:</h3>
      <ul style="margin: 0; padding-left: 20px; color: #555;">
        {{.FeaturesHTML}}
      </ul>
    </div>

    <div style="text-align: center; margin: 30px 0;">
      <a href="{{.AppURL}}"
         style="display: inline-block; padding: 15px 30px; background: #D4AF37; color: white; text-decoration: none; border-radius: 8px; font-size: 16px; font-weight: bold;">
        Launch Estara Insight App
      </a>
    </div>

    <div style="border-top: 1px solid #e1e8ed; margin-top: 30px; padding-top: 20px;">
      <h3 style="margin: 0 0 15px 0; color: #333; font-size: 16px;">Quick Start Guide:</h3>
      <ol style="margin: 0; padding-left: 20px; color: #555;">
        <li style="margin-bottom: 8px;">Sign in to Estara Insight with your email and password</li>
        <li style="margin-bottom: 8px;">Run your first AI Market Analysis</li>
        <li style="margin-bottom: 8px;">Explore AI-powered property recommendations</li>
        <li style="margin-bottom: 8px;">Build your personalized Investment Plan</li>
      </ol>
    </div>

    {{.GuaranteeSection}}

    <div style="margin-top: 30px; text-align: center; border-top: 1px solid #e1e8ed; padding-top: 20px;">
      <p style="font-size: 12px; color: #999; margin: 0;">
        Questions? Reply to this email or contact us at support@estara-ai.com
      </p>
      <p style="font-size: 12px; color: #999; margin: 10px 0 0 0;">
        © {{.Year}} Estara AI. All rights reserved.
      </p>
    </div>
  </div>
</body>
</html>`

	t := template.Must(template.New("welcome").Parse(tmpl))
	var buf bytes.Buffer
	_ = t.Execute(&buf, map[string]interface{}{
		"FirstName":        firstName,
		"TierName":         tierInfo.Name,
		"BadgeText":        badgeText,
		"FeaturesHTML":     template.HTML(featuresHTML),
		"AppURL":           appURL,
		"IsPaid":           isPaid,
		"GuaranteeSection": template.HTML(guaranteeSection),
		"Year":             time.Now().Year(),
	})
	return buf.String()
}

func (s *Service) renderWelcomeText(firstName string, tierInfo TierInfo, appURL string, isPaid bool) string {
	features := ""
	for _, f := range tierInfo.Features {
		// Strip HTML tags for text version
		clean := strings.ReplaceAll(f, "<strong>", "")
		clean = strings.ReplaceAll(clean, "</strong>", "")
		features += fmt.Sprintf("- %s\n", clean)
	}

	return fmt.Sprintf(`Hello %s,

Welcome to Estara AI!

Your %s is now active.

What's Included:
%s

Get started now: %s

Quick Start Guide:
1. Sign in with your email and password
2. Run your first AI Market Analysis
3. Explore AI-powered property recommendations
4. Build your personalized Investment Plan

Questions? Reply to this email or contact support@estara-ai.com

© %d Estara AI. All rights reserved.`, firstName, tierInfo.Name, features, appURL, time.Now().Year())
}

func (s *Service) renderInvoiceHTML(firstName, invoiceNumber, amount string, dueDate *time.Time, invoiceURL, pdfURL string) string {
	dueDateStr := "Due upon receipt"
	if dueDate != nil {
		dueDateStr = fmt.Sprintf("Due by %s", dueDate.Format("January 2, 2006"))
	}

	pdfLink := ""
	if pdfURL != "" {
		pdfLink = fmt.Sprintf(`<a href="%s" style="color: #D4AF37;">Download PDF</a>`, pdfURL)
	}

	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Invoice from Estara AI</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Estara AI</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">Invoice</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hello {{.FirstName}},</h2>

    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">
      Your invoice is ready.
    </p>

    <div style="background: #f8f9fa; padding: 20px; border-radius: 8px; margin: 25px 0;">
      <table style="width: 100%; border-collapse: collapse;">
        <tr>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed;"><strong>Invoice Number:</strong></td>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed; text-align: right;">{{.InvoiceNumber}}</td>
        </tr>
        <tr>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed;"><strong>Amount:</strong></td>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed; text-align: right; font-size: 18px; color: #333;"><strong>{{.Amount}}</strong></td>
        </tr>
        <tr>
          <td style="padding: 10px 0;"><strong>Status:</strong></td>
          <td style="padding: 10px 0; text-align: right;">{{.DueDate}}</td>
        </tr>
      </table>
    </div>

    <div style="text-align: center; margin: 30px 0;">
      <a href="{{.InvoiceURL}}"
         style="display: inline-block; padding: 15px 30px; background: #D4AF37; color: white; text-decoration: none; border-radius: 8px; font-size: 16px; font-weight: bold;">
        View Invoice
      </a>
      {{if .PDFURL}}<br><br>{{.PDFLink}}{{end}}
    </div>

    <div style="margin-top: 30px; text-align: center; border-top: 1px solid #e1e8ed; padding-top: 20px;">
      <p style="font-size: 12px; color: #999; margin: 0;">
        Questions about this invoice? Reply to this email.
      </p>
      <p style="font-size: 12px; color: #999; margin: 10px 0 0 0;">
        © {{.Year}} Estara AI. All rights reserved.
      </p>
    </div>
  </div>
</body>
</html>`

	t := template.Must(template.New("invoice").Parse(tmpl))
	var buf bytes.Buffer
	_ = t.Execute(&buf, map[string]interface{}{
		"FirstName":     firstName,
		"InvoiceNumber": invoiceNumber,
		"Amount":        amount,
		"DueDate":       dueDateStr,
		"InvoiceURL":    invoiceURL,
		"PDFURL":        pdfURL,
		"PDFLink":       template.HTML(pdfLink),
		"Year":          time.Now().Year(),
	})
	return buf.String()
}

func (s *Service) renderInvoiceText(firstName, invoiceNumber, amount string, dueDate *time.Time, invoiceURL string) string {
	dueDateStr := "Due upon receipt"
	if dueDate != nil {
		dueDateStr = fmt.Sprintf("Due by %s", dueDate.Format("January 2, 2006"))
	}

	return fmt.Sprintf(`Hello %s,

Your invoice is ready.

Invoice Number: %s
Amount: %s
Status: %s

View your invoice: %s

Questions? Reply to this email.

© %d Estara AI. All rights reserved.`, firstName, invoiceNumber, amount, dueDateStr, invoiceURL, time.Now().Year())
}

func (s *Service) renderReceiptHTML(firstName, receiptNumber, amount, cardBrand, cardLast4 string, paidAt time.Time, receiptURL, description string) string {
	paymentMethod := "Card"
	if cardBrand != "" && cardLast4 != "" {
		paymentMethod = fmt.Sprintf("%s ending in %s", cardBrand, cardLast4)
	}

	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Receipt from Estara AI</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Estara AI</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">Payment Receipt</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hello {{.FirstName}},</h2>

    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">
      Thank you for your payment! Here's your receipt.
    </p>

    <div style="background: #ecfdf5; padding: 15px; border-radius: 8px; margin-bottom: 20px; border-left: 4px solid #22c55e;">
      <p style="margin: 0; font-size: 14px; color: #166534;">
        <strong>✅ Payment Successful</strong>
      </p>
    </div>

    <div style="background: #f8f9fa; padding: 20px; border-radius: 8px; margin: 25px 0;">
      <table style="width: 100%; border-collapse: collapse;">
        <tr>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed;"><strong>Receipt Number:</strong></td>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed; text-align: right;">{{.ReceiptNumber}}</td>
        </tr>
        <tr>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed;"><strong>Date:</strong></td>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed; text-align: right;">{{.PaidAt}}</td>
        </tr>
        <tr>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed;"><strong>Description:</strong></td>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed; text-align: right;">{{.Description}}</td>
        </tr>
        <tr>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed;"><strong>Payment Method:</strong></td>
          <td style="padding: 10px 0; border-bottom: 1px solid #e1e8ed; text-align: right;">{{.PaymentMethod}}</td>
        </tr>
        <tr>
          <td style="padding: 10px 0;"><strong>Amount Paid:</strong></td>
          <td style="padding: 10px 0; text-align: right; font-size: 20px; color: #22c55e;"><strong>{{.Amount}}</strong></td>
        </tr>
      </table>
    </div>

    {{if .ReceiptURL}}
    <div style="text-align: center; margin: 30px 0;">
      <a href="{{.ReceiptURL}}"
         style="display: inline-block; padding: 15px 30px; background: #D4AF37; color: white; text-decoration: none; border-radius: 8px; font-size: 16px; font-weight: bold;">
        View Full Receipt
      </a>
    </div>
    {{end}}

    <div style="margin-top: 30px; text-align: center; border-top: 1px solid #e1e8ed; padding-top: 20px;">
      <p style="font-size: 12px; color: #999; margin: 0;">
        Keep this email for your records. Questions? Reply to this email.
      </p>
      <p style="font-size: 12px; color: #999; margin: 10px 0 0 0;">
        © {{.Year}} Estara AI. All rights reserved.
      </p>
    </div>
  </div>
</body>
</html>`

	t := template.Must(template.New("receipt").Parse(tmpl))
	var buf bytes.Buffer
	_ = t.Execute(&buf, map[string]interface{}{
		"FirstName":     firstName,
		"ReceiptNumber": receiptNumber,
		"Amount":        amount,
		"PaidAt":        paidAt.Format("January 2, 2006"),
		"PaymentMethod": paymentMethod,
		"Description":   description,
		"ReceiptURL":    receiptURL,
		"Year":          time.Now().Year(),
	})
	return buf.String()
}

func (s *Service) renderReceiptText(firstName, receiptNumber, amount, cardBrand, cardLast4 string, paidAt time.Time, receiptURL string) string {
	paymentMethod := "Card"
	if cardBrand != "" && cardLast4 != "" {
		paymentMethod = fmt.Sprintf("%s ending in %s", cardBrand, cardLast4)
	}

	return fmt.Sprintf(`Hello %s,

Thank you for your payment! Here's your receipt.

Receipt Number: %s
Date: %s
Payment Method: %s
Amount Paid: %s

View full receipt: %s

Keep this email for your records.

© %d Estara AI. All rights reserved.`, firstName, receiptNumber, paidAt.Format("January 2, 2006"), paymentMethod, amount, receiptURL, time.Now().Year())
}

func (s *Service) renderRenewalReminderHTML(firstName, planName, amount string, renewalDate time.Time, manageURL string) string {
	tmpl := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Upcoming Renewal - Estara AI</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
  <div style="background: linear-gradient(135deg, #D4AF37 0%, #B8860B 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
    <h1 style="color: white; margin: 0; font-size: 28px;">Estara AI</h1>
    <p style="color: rgba(255,255,255,0.9); margin: 10px 0 0 0; font-size: 16px;">Subscription Renewal Notice</p>
  </div>

  <div style="background: white; padding: 30px; border-radius: 0 0 10px 10px; border: 1px solid #e1e8ed;">
    <h2 style="color: #333; margin-bottom: 20px;">Hello {{.FirstName}},</h2>

    <p style="margin-bottom: 20px; font-size: 16px; line-height: 1.6;">
      This is a friendly reminder that your Estara AI subscription will automatically renew soon.
    </p>

    <div style="background: #fff8e1; padding: 20px; border-radius: 8px; margin: 25px 0; border-left: 4px solid #D4AF37;">
      <table style="width: 100%; border-collapse: collapse;">
        <tr>
          <td style="padding: 8px 0;"><strong>Plan:</strong></td>
          <td style="padding: 8px 0; text-align: right;">{{.PlanName}}</td>
        </tr>
        <tr>
          <td style="padding: 8px 0;"><strong>Renewal Date:</strong></td>
          <td style="padding: 8px 0; text-align: right;">{{.RenewalDate}}</td>
        </tr>
        <tr>
          <td style="padding: 8px 0;"><strong>Amount:</strong></td>
          <td style="padding: 8px 0; text-align: right; font-size: 18px;"><strong>{{.Amount}}</strong></td>
        </tr>
      </table>
    </div>

    <p style="margin-bottom: 20px; font-size: 14px; color: #666;">
      Your payment method on file will be charged automatically on the renewal date. No action is needed to continue your subscription.
    </p>

    {{if .ManageURL}}
    <div style="text-align: center; margin: 30px 0;">
      <a href="{{.ManageURL}}"
         style="display: inline-block; padding: 15px 30px; background: #6b7280; color: white; text-decoration: none; border-radius: 8px; font-size: 16px;">
        Manage Subscription
      </a>
    </div>
    {{end}}

    <div style="margin-top: 30px; text-align: center; border-top: 1px solid #e1e8ed; padding-top: 20px;">
      <p style="font-size: 12px; color: #999; margin: 0;">
        Questions or need to make changes? Reply to this email.
      </p>
      <p style="font-size: 12px; color: #999; margin: 10px 0 0 0;">
        © {{.Year}} Estara AI. All rights reserved.
      </p>
    </div>
  </div>
</body>
</html>`

	t := template.Must(template.New("renewal").Parse(tmpl))
	var buf bytes.Buffer
	_ = t.Execute(&buf, map[string]interface{}{
		"FirstName":   firstName,
		"PlanName":    planName,
		"Amount":      amount,
		"RenewalDate": renewalDate.Format("January 2, 2006"),
		"ManageURL":   manageURL,
		"Year":        time.Now().Year(),
	})
	return buf.String()
}

func (s *Service) renderRenewalReminderText(firstName, planName, amount string, renewalDate time.Time, manageURL string) string {
	return fmt.Sprintf(`Hello %s,

This is a friendly reminder that your Estara AI subscription will automatically renew soon.

Plan: %s
Renewal Date: %s
Amount: %s

Your payment method on file will be charged automatically. No action is needed to continue.

Manage your subscription: %s

Questions? Reply to this email.

© %d Estara AI. All rights reserved.`, firstName, planName, renewalDate.Format("January 2, 2006"), amount, manageURL, time.Now().Year())
}
