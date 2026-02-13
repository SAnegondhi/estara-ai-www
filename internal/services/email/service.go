// Package email provides email sending functionality using Mailjet
package email

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"net/http"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/estara-ai/www/internal/config"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

// Service handles email sending via Mailjet
type Service struct {
	cfg           *config.Config
	client        *http.Client
	logger        *slog.Logger
	htmlTemplates *htmltemplate.Template
	textTemplates *texttemplate.Template
}

// NewService creates a new email service
func NewService(cfg *config.Config) *Service {
	htmlTmpl := htmltemplate.Must(htmltemplate.ParseFS(templateFS, "templates/*.html"))
	textTmpl := texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/*.txt"))

	return &Service{
		cfg:           cfg,
		client:        &http.Client{Timeout: 30 * time.Second},
		logger:        slog.Default().With("component", "email_service"),
		htmlTemplates: htmlTmpl,
		textTemplates: textTmpl,
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

	html := s.renderHTML("password_reset.html", map[string]interface{}{
		"FirstName": firstName,
		"ResetURL":  resetURL,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("password_reset.txt", map[string]interface{}{
		"FirstName": firstName,
		"ResetURL":  resetURL,
		"Year":      time.Now().Year(),
	})

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

	html := s.renderHTML("verification_code.html", map[string]interface{}{
		"FirstName": firstName,
		"Code":      code,
		"Year":      time.Now().Year(),
	})
	text := s.renderText("verification_code.txt", map[string]interface{}{
		"FirstName": firstName,
		"Code":      code,
		"Year":      time.Now().Year(),
	})

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
	Tier      string // e.g., "PROFESSIONAL_ALLOCATOR", "ANNUAL_ACCESS", "FREE"
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
	case "PROFESSIONAL_ALLOCATOR", "professional_allocator":
		return TierInfo{
			Name:   "Professional Allocator",
			IsPaid: true,
			Features: []string{
				"<strong>60 AI-Powered Reports</strong> per year",
				"<strong>Unlimited Market Analysis</strong>",
				"<strong>Investment Planning Tools</strong>",
				"<strong>Priority Support</strong>",
			},
		}
	case "ANNUAL_ACCESS", "annual_access":
		return TierInfo{
			Name:   "Annual Access",
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

// renderHTML executes a named HTML template with the given data.
func (s *Service) renderHTML(name string, data interface{}) string {
	var buf bytes.Buffer
	if err := s.htmlTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		s.logger.Error("failed to render HTML template", "template", name, "error", err)
		return ""
	}
	return buf.String()
}

// renderText executes a named text template with the given data.
func (s *Service) renderText(name string, data interface{}) string {
	var buf bytes.Buffer
	if err := s.textTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		s.logger.Error("failed to render text template", "template", name, "error", err)
		return ""
	}
	return buf.String()
}

func (s *Service) renderBillingHTML(firstName, headline, message string) string {
	return s.renderHTML("billing.html", map[string]interface{}{
		"FirstName": firstName,
		"Headline":  headline,
		"Message":   message,
	})
}

func (s *Service) renderBillingText(firstName, headline, message string) string {
	return s.renderText("billing.txt", map[string]interface{}{
		"FirstName": firstName,
		"Headline":  headline,
		"Message":   message,
	})
}

func formatCurrency(amount int64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	return fmt.Sprintf("%.2f %s", float64(amount)/100, strings.ToUpper(currency))
}

// ADR-067: New email templates for billing completeness

func (s *Service) renderWelcomeHTML(firstName string, tierInfo TierInfo, appURL string, isPaid bool) string {
	featuresHTML := ""
	for _, f := range tierInfo.Features {
		featuresHTML += fmt.Sprintf(`<li style="margin-bottom: 6px;">%s</li>`, f)
	}

	badgeText := tierInfo.Name
	if !isPaid {
		badgeText = "14-Day Evaluation Period"
	}

	guaranteeSection := ""
	if isPaid {
		guaranteeSection = `
        <div style="background: #f0fdf4; padding: 16px 20px; border-radius: 8px; margin-top: 28px; border-left: 3px solid #16a34a;">
          <p style="margin: 0; font-size: 12px; color: #4b5563; line-height: 1.65;">
            <strong style="color: #111827;">14-Day Money-Back Guarantee</strong> — Not satisfied? Request a full refund within 14 days of purchase. No questions asked.
          </p>
        </div>`
	} else {
		guaranteeSection = fmt.Sprintf(`
        <div style="background: #f9fafb; padding: 16px 20px; border-radius: 8px; margin-top: 28px; border: 1px solid #f3f4f6;">
          <p style="margin: 0; font-size: 12px; color: #4b5563; line-height: 1.65;">
            <strong style="color: #111827;">Ready to subscribe?</strong> Upgrade to a paid plan for more reports and advanced features.
            <a href="%s/pricing" style="color: #1e40af; text-decoration: none; font-weight: 500;">View Plans</a>
          </p>
        </div>`, s.cfg.Server.MarketingURL)
	}

	downloadURL := s.cfg.Server.MarketingURL + "/download-mobile-app"

	return s.renderHTML("welcome.html", map[string]interface{}{
		"FirstName":        firstName,
		"TierName":         tierInfo.Name,
		"BadgeText":        badgeText,
		"FeaturesHTML":     htmltemplate.HTML(featuresHTML),
		"AppURL":           appURL,
		"IsPaid":           isPaid,
		"GuaranteeSection": htmltemplate.HTML(guaranteeSection),
		"DownloadURL":      downloadURL,
		"Year":             time.Now().Year(),
	})
}

func (s *Service) renderWelcomeText(firstName string, tierInfo TierInfo, appURL string, isPaid bool) string {
	features := ""
	for _, f := range tierInfo.Features {
		// Strip HTML tags for text version
		clean := strings.ReplaceAll(f, "<strong>", "")
		clean = strings.ReplaceAll(clean, "</strong>", "")
		features += fmt.Sprintf("- %s\n", clean)
	}

	badgeText := tierInfo.Name
	if !isPaid {
		badgeText = "14-Day Evaluation Period"
	}

	guaranteeSectionText := ""
	if isPaid {
		guaranteeSectionText = "14-DAY MONEY-BACK GUARANTEE\nNot satisfied? Request a full refund within 14 days of purchase. No questions asked."
	} else {
		guaranteeSectionText = fmt.Sprintf("Ready to subscribe? Upgrade to a paid plan for more reports\nand advanced features. View plans: %s/pricing", s.cfg.Server.MarketingURL)
	}

	downloadURL := s.cfg.Server.MarketingURL + "/download-mobile-app"

	return s.renderText("welcome.txt", map[string]interface{}{
		"FirstName":            firstName,
		"TierName":             tierInfo.Name,
		"BadgeText":            badgeText,
		"IsPaid":               isPaid,
		"FeaturesText":         features,
		"AppURL":               appURL,
		"DownloadURL":          downloadURL,
		"GuaranteeSectionText": guaranteeSectionText,
		"Year":                 time.Now().Year(),
	})
}

func (s *Service) renderInvoiceHTML(firstName, invoiceNumber, amount string, dueDate *time.Time, invoiceURL, pdfURL string) string {
	dueDateStr := "Due upon receipt"
	if dueDate != nil {
		dueDateStr = fmt.Sprintf("Due by %s", dueDate.Format("January 2, 2006"))
	}

	pdfLink := ""
	if pdfURL != "" {
		pdfLink = fmt.Sprintf(`<a href="%s" style="color: #1e40af;">Download PDF</a>`, pdfURL)
	}

	return s.renderHTML("invoice.html", map[string]interface{}{
		"FirstName":     firstName,
		"InvoiceNumber": invoiceNumber,
		"Amount":        amount,
		"DueDate":       dueDateStr,
		"InvoiceURL":    invoiceURL,
		"PDFURL":        pdfURL,
		"PDFLink":       htmltemplate.HTML(pdfLink),
		"Year":          time.Now().Year(),
	})
}

func (s *Service) renderInvoiceText(firstName, invoiceNumber, amount string, dueDate *time.Time, invoiceURL string) string {
	dueDateStr := "Due upon receipt"
	if dueDate != nil {
		dueDateStr = fmt.Sprintf("Due by %s", dueDate.Format("January 2, 2006"))
	}

	return s.renderText("invoice.txt", map[string]interface{}{
		"FirstName":     firstName,
		"InvoiceNumber": invoiceNumber,
		"Amount":        amount,
		"DueDate":       dueDateStr,
		"InvoiceURL":    invoiceURL,
		"Year":          time.Now().Year(),
	})
}

func (s *Service) renderReceiptHTML(firstName, receiptNumber, amount, cardBrand, cardLast4 string, paidAt time.Time, receiptURL, description string) string {
	paymentMethod := "Card"
	if cardBrand != "" && cardLast4 != "" {
		paymentMethod = fmt.Sprintf("%s ending in %s", cardBrand, cardLast4)
	}

	return s.renderHTML("receipt.html", map[string]interface{}{
		"FirstName":     firstName,
		"ReceiptNumber": receiptNumber,
		"Amount":        amount,
		"PaidAt":        paidAt.Format("January 2, 2006"),
		"PaymentMethod": paymentMethod,
		"Description":   description,
		"ReceiptURL":    receiptURL,
		"Year":          time.Now().Year(),
	})
}

func (s *Service) renderReceiptText(firstName, receiptNumber, amount, cardBrand, cardLast4 string, paidAt time.Time, receiptURL string) string {
	paymentMethod := "Card"
	if cardBrand != "" && cardLast4 != "" {
		paymentMethod = fmt.Sprintf("%s ending in %s", cardBrand, cardLast4)
	}

	return s.renderText("receipt.txt", map[string]interface{}{
		"FirstName":     firstName,
		"ReceiptNumber": receiptNumber,
		"Amount":        amount,
		"PaidAt":        paidAt.Format("January 2, 2006"),
		"PaymentMethod": paymentMethod,
		"ReceiptURL":    receiptURL,
		"Year":          time.Now().Year(),
	})
}

func (s *Service) renderRenewalReminderHTML(firstName, planName, amount string, renewalDate time.Time, manageURL string) string {
	return s.renderHTML("renewal_reminder.html", map[string]interface{}{
		"FirstName":   firstName,
		"PlanName":    planName,
		"Amount":      amount,
		"RenewalDate": renewalDate.Format("January 2, 2006"),
		"ManageURL":   manageURL,
		"Year":        time.Now().Year(),
	})
}

func (s *Service) renderRenewalReminderText(firstName, planName, amount string, renewalDate time.Time, manageURL string) string {
	return s.renderText("renewal_reminder.txt", map[string]interface{}{
		"FirstName":   firstName,
		"PlanName":    planName,
		"Amount":      amount,
		"RenewalDate": renewalDate.Format("January 2, 2006"),
		"ManageURL":   manageURL,
		"Year":        time.Now().Year(),
	})
}
