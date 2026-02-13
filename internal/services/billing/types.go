// Package billing provides Stripe payment integration
package billing

import "time"

// SubscriptionTier represents a subscription tier level
type SubscriptionTier string

const (
	TierFree                  SubscriptionTier = "FREE"
	TierAnnualAccess          SubscriptionTier = "ANNUAL_ACCESS"
	TierProfessionalAllocator SubscriptionTier = "PROFESSIONAL_ALLOCATOR"
	TierAPIInvestor           SubscriptionTier = "API_INVESTOR"
	TierAPIAllocator          SubscriptionTier = "API_ALLOCATOR"
)

// SubscriptionStatus represents subscription status
type SubscriptionStatus string

const (
	StatusActive    SubscriptionStatus = "ACTIVE"
	StatusTrialing  SubscriptionStatus = "TRIALING"
	StatusCanceled  SubscriptionStatus = "CANCELED"
	StatusPastDue   SubscriptionStatus = "PAST_DUE"
	StatusUnpaid    SubscriptionStatus = "UNPAID"
	StatusFree      SubscriptionStatus = "FREE"
	StatusIncomplete SubscriptionStatus = "INCOMPLETE"
)

// ProductType represents the type of product being purchased
type ProductType string

const (
	ProductSubscription   ProductType = "SUBSCRIPTION"
	ProductSingleReport   ProductType = "SINGLE_REPORT"
	ProductReportPack     ProductType = "REPORT_PACK"
	ProductOverageReport  ProductType = "OVERAGE_REPORT"
	ProductInsightAccess  ProductType = "INSIGHT_ACCESS"
)

// CheckoutSessionRequest represents a request to create a checkout session
type CheckoutSessionRequest struct {
	PriceID       string            `json:"priceId"`
	ProductType   ProductType       `json:"productType"`
	SuccessURL    string            `json:"successUrl"`
	CancelURL     string            `json:"cancelUrl"`
	CustomerEmail string            `json:"customerEmail,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	AllowPromoCode bool             `json:"allowPromoCode,omitempty"`
}

// CheckoutSessionResponse represents the response from creating a checkout session
type CheckoutSessionResponse struct {
	SessionID    string `json:"sessionId"`
	SessionURL   string `json:"sessionUrl"`
	PublishableKey string `json:"publishableKey,omitempty"`
}

// SubscriptionResponse represents subscription details
type SubscriptionResponse struct {
	ID                   string             `json:"id"`
	StripeSubscriptionID string             `json:"stripeSubscriptionId,omitempty"`
	Tier                 SubscriptionTier   `json:"tier"`
	Status               SubscriptionStatus `json:"status"`
	CurrentPeriodStart   *time.Time         `json:"currentPeriodStart,omitempty"`
	CurrentPeriodEnd     *time.Time         `json:"currentPeriodEnd,omitempty"`
	TrialEnd             *time.Time         `json:"trialEnd,omitempty"`
	CancelAtPeriodEnd    bool               `json:"cancelAtPeriodEnd"`
	CreatedAt            time.Time          `json:"createdAt"`
}

// PortalSessionResponse represents a billing portal session
type PortalSessionResponse struct {
	URL string `json:"url"`
}

// PaymentStatusResponse represents payment verification status
type PaymentStatusResponse struct {
	Success        bool   `json:"success"`
	Status         string `json:"status"`
	PaymentIntentID string `json:"paymentIntentId,omitempty"`
	SubscriptionID  string `json:"subscriptionId,omitempty"`
	CustomerID     string `json:"customerId,omitempty"`
}

// CancelSubscriptionRequest represents a request to cancel a subscription
type CancelSubscriptionRequest struct {
	CancelAtPeriodEnd bool `json:"cancelAtPeriodEnd"`
}

// WebhookEvent represents a Stripe webhook event
type WebhookEvent struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Data     interface{} `json:"data"`
	Created  int64  `json:"created"`
	Livemode bool   `json:"livemode"`
}

// ChargebackEvidence represents evidence for disputing a chargeback
type ChargebackEvidence struct {
	CustomerName        string `json:"customerName,omitempty"`
	CustomerEmail       string `json:"customerEmail,omitempty"`
	BillingAddress      string `json:"billingAddress,omitempty"`
	ProductDescription  string `json:"productDescription,omitempty"`
	ServiceDate         string `json:"serviceDate,omitempty"`
	ReceiptURL          string `json:"receiptUrl,omitempty"`
	CancellationPolicy  string `json:"cancellationPolicy,omitempty"`
	RefundPolicy        string `json:"refundPolicy,omitempty"`
	CustomerSignature   string `json:"customerSignature,omitempty"` // Terms acceptance timestamp
	UncategorizedText   string `json:"uncategorizedText,omitempty"`
}

// InvoiceResponse represents an invoice
type InvoiceResponse struct {
	ID               string     `json:"id"`
	StripeInvoiceID  string     `json:"stripeInvoiceId"`
	InvoiceNumber    string     `json:"invoiceNumber,omitempty"`
	Status           string     `json:"status"`
	Subtotal         int64      `json:"subtotal"`
	TaxAmount        int64      `json:"taxAmount"`
	Total            int64      `json:"total"`
	AmountPaid       int64      `json:"amountPaid"`
	AmountDue        int64      `json:"amountDue"`
	Currency         string     `json:"currency"`
	DueDate          *time.Time `json:"dueDate,omitempty"`
	PaidAt           *time.Time `json:"paidAt,omitempty"`
	HostedInvoiceURL string     `json:"hostedInvoiceUrl,omitempty"`
	InvoicePDFURL    string     `json:"invoicePdfUrl,omitempty"`
	Description      string     `json:"description,omitempty"`
	ProductType      string     `json:"productType,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

// ReceiptResponse represents a payment receipt
type ReceiptResponse struct {
	ID                    string     `json:"id"`
	ReceiptNumber         string     `json:"receiptNumber"`
	Amount                int64      `json:"amount"`
	Currency              string     `json:"currency"`
	PaymentMethod         string     `json:"paymentMethod,omitempty"`
	CardBrand             string     `json:"cardBrand,omitempty"`
	CardLast4             string     `json:"cardLast4,omitempty"`
	PaidAt                *time.Time `json:"paidAt,omitempty"`
	StripeReceiptURL      string     `json:"stripeReceiptUrl,omitempty"`
	ProductDescription    string     `json:"productDescription,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
}
