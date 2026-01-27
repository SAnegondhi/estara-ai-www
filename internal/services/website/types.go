package website

import "time"

// Report types
const (
	ReportTypeSnapshot      = "SNAPSHOT"
	ReportTypeMIR           = "MIR"
	ReportTypeCIP           = "CIP"
	ReportTypeComprehensive = "COMPREHENSIVE"
)

// Report status
const (
	ReportStatusPending    = "PENDING"
	ReportStatusGenerating = "GENERATING"
	ReportStatusComplete   = "COMPLETE"
	ReportStatusFailed     = "FAILED"
)

// Job types
const (
	JobTypeSnapshot = "SNAPSHOT"
	JobTypeMIR      = "MIR"
	JobTypeCIP      = "CIP"
)

// GenerateReportRequest represents a request to generate a report
type GenerateReportRequest struct {
	ReportType     string                 `json:"reportType"`
	PropertyID     string                 `json:"propertyId,omitempty"`
	Address        string                 `json:"address"`
	City           string                 `json:"city"`
	State          string                 `json:"state"`
	ZipCode        string                 `json:"zipCode"`
	Price          float64                `json:"price"`
	Criteria       map[string]interface{} `json:"criteria,omitempty"`
	Email          string                 `json:"email"`
	StripePayment  string                 `json:"stripePayment,omitempty"`
	InsightAccess  string                 `json:"insightAccess,omitempty"`
	ReportPackID   string                 `json:"reportPackId,omitempty"`
}

// GenerateReportResponse represents the response from report generation
type GenerateReportResponse struct {
	Success  bool   `json:"success"`
	ReportID string `json:"reportId"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

// CheckoutRequest represents a website checkout request
type CheckoutRequest struct {
	ProductType   string            `json:"productType"`
	PriceID       string            `json:"priceId"`
	Email         string            `json:"email"`
	SuccessURL    string            `json:"successUrl"`
	CancelURL     string            `json:"cancelUrl"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	AllowPromo    bool              `json:"allowPromo"`
}

// CheckoutResponse represents the checkout response
type CheckoutResponse struct {
	SessionID      string `json:"sessionId"`
	SessionURL     string `json:"sessionUrl"`
	PublishableKey string `json:"publishableKey"`
}

// FreeSnapshotRequest represents a request for a free snapshot
type FreeSnapshotRequest struct {
	Email     string                 `json:"email"`
	Address   string                 `json:"address"`
	City      string                 `json:"city"`
	State     string                 `json:"state"`
	ZipCode   string                 `json:"zipCode"`
	Criteria  map[string]interface{} `json:"criteria,omitempty"`
	Source    string                 `json:"source,omitempty"`
	IPAddress string                 `json:"-"`
	UserAgent string                 `json:"-"`
}

// FreeSnapshotResponse represents the response for a free snapshot request
type FreeSnapshotResponse struct {
	Success    bool   `json:"success"`
	RequestID  string `json:"requestId"`
	SessionID  string `json:"sessionId"`
	Message    string `json:"message,omitempty"`
	Remaining  int    `json:"remaining,omitempty"`
}

// OrderStatusResponse represents the status of an order
type OrderStatusResponse struct {
	OrderID     string     `json:"orderId"`
	Status      string     `json:"status"`
	ProductType string     `json:"productType,omitempty"`
	ReportID    string     `json:"reportId,omitempty"`
	ReportURL   string     `json:"reportUrl,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// QueueJobRequest represents a request to queue an async job
type QueueJobRequest struct {
	JobType   string                 `json:"jobType"`
	Email     string                 `json:"email"`
	Address   string                 `json:"address"`
	City      string                 `json:"city"`
	State     string                 `json:"state"`
	ZipCode   string                 `json:"zipCode"`
	Criteria  map[string]interface{} `json:"criteria,omitempty"`
	SessionID string                 `json:"sessionId,omitempty"`
	IPAddress string                 `json:"-"`
	UserAgent string                 `json:"-"`
}

// QueueJobResponse represents the response from queuing a job
type QueueJobResponse struct {
	Success   bool   `json:"success"`
	JobID     string `json:"jobId"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message,omitempty"`
}

// ContactSubmission represents a contact form submission
type ContactSubmission struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone,omitempty"`
	Company   string    `json:"company,omitempty"`
	Subject   string    `json:"subject"`
	Message   string    `json:"message"`
	Source    string    `json:"source,omitempty"`
	IPAddress string    `json:"ipAddress,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ContactRequest represents a contact form submission request
type ContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Phone   string `json:"phone,omitempty"`
	Company string `json:"company,omitempty"`
	Subject string `json:"subject"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
}

// ContactResponse represents the response from a contact submission
type ContactResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// PricingConfig represents public pricing configuration
type PricingConfig struct {
	Subscriptions []PricingPlan  `json:"subscriptions"`
	OneTimePurchases []PricingItem `json:"oneTimePurchases"`
	Features       []FeatureSet   `json:"features"`
}

// PricingPlan represents a subscription pricing plan
type PricingPlan struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Price       string   `json:"price"`
	PriceID     string   `json:"priceId"`
	Interval    string   `json:"interval"`
	Features    []string `json:"features"`
	Popular     bool     `json:"popular,omitempty"`
}

// PricingItem represents a one-time purchase item
type PricingItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       string `json:"price"`
	PriceID     string `json:"priceId"`
}

// FeatureSet represents features for a tier
type FeatureSet struct {
	Tier     string   `json:"tier"`
	Features []string `json:"features"`
}

// CreditWallet represents a user's credit wallet
type CreditWallet struct {
	UserID         string    `json:"userId"`
	Balance        int64     `json:"balance"`
	TotalPurchased int64     `json:"totalPurchased"`
	TotalUsed      int64     `json:"totalUsed"`
	LastUpdated    time.Time `json:"lastUpdated"`
}

// PurchaseCreditsRequest represents a request to purchase credits
type PurchaseCreditsRequest struct {
	PackageID   string `json:"packageId"`
	SuccessURL  string `json:"successUrl"`
	CancelURL   string `json:"cancelUrl"`
}

// PurchaseCreditsResponse represents the response from a credit purchase
type PurchaseCreditsResponse struct {
	SessionID      string `json:"sessionId"`
	SessionURL     string `json:"sessionUrl"`
	PublishableKey string `json:"publishableKey"`
}

// InsightAccessSignupRequest represents a request to sign up for insight access
type InsightAccessSignupRequest struct {
	Tier        string `json:"tier"`
	Frequency   string `json:"frequency"`
	Email       string `json:"email"`
	SuccessURL  string `json:"successUrl"`
	CancelURL   string `json:"cancelUrl"`
}

// InsightAccessStatusResponse represents the status of insight access
type InsightAccessStatusResponse struct {
	Active        bool       `json:"active"`
	Tier          string     `json:"tier,omitempty"`
	ReportsUsed   int        `json:"reportsUsed"`
	ReportsTotal  int        `json:"reportsTotal"`
	PeriodEnd     *time.Time `json:"periodEnd,omitempty"`
}

// EarlyAccessRequest represents an early access signup request
type EarlyAccessRequest struct {
	Email       string `json:"email"`
	Name        string `json:"name,omitempty"`
	Company     string `json:"company,omitempty"`
	UseCase     string `json:"useCase,omitempty"`
}

// EarlyAccessResponse represents the early access signup response
type EarlyAccessResponse struct {
	Success  bool   `json:"success"`
	Position int    `json:"position,omitempty"`
	Message  string `json:"message"`
}

// GuestSessionResponse represents a guest session creation response
type GuestSessionResponse struct {
	SessionID string    `json:"sessionId"`
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"token"`
}
