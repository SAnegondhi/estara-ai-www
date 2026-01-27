package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
)

var (
	// ErrSubscriptionNotFound is returned when a subscription is not found
	ErrSubscriptionNotFound = errors.New("subscription not found")
	// ErrCustomerNotFound is returned when a customer is not found
	ErrCustomerNotFound = errors.New("customer not found")
	// ErrInvalidTier is returned when an invalid tier is specified
	ErrInvalidTier = errors.New("invalid subscription tier")
)

// Service handles billing operations
type Service struct {
	db     *postgres.DB
	stripe *StripeClient
	cfg    *config.Config
	logger *slog.Logger
}

// NewService creates a new billing service
func NewService(db *postgres.DB, cfg *config.Config) *Service {
	return &Service{
		db:     db,
		stripe: NewStripeClient(&cfg.Stripe),
		cfg:    cfg,
		logger: slog.Default().With("component", "billing_service"),
	}
}

// CreateCheckoutSession creates a new checkout session
func (s *Service) CreateCheckoutSession(ctx context.Context, userID, email string, req *CheckoutSessionRequest) (*CheckoutSessionResponse, error) {
	// Set customer email if not provided
	if req.CustomerEmail == "" {
		req.CustomerEmail = email
	}

	// Create checkout session
	resp, err := s.stripe.CreateCheckoutSession(req, userID)
	if err != nil {
		return nil, err
	}

	// Log the checkout creation
	s.logger.Info("checkout session created",
		"user_id", userID,
		"session_id", resp.SessionID,
		"product_type", req.ProductType,
	)

	return resp, nil
}

// GetCheckoutSessionStatus retrieves the status of a checkout session
func (s *Service) GetCheckoutSessionStatus(ctx context.Context, sessionID string) (*PaymentStatusResponse, error) {
	sess, err := s.stripe.GetCheckoutSession(sessionID)
	if err != nil {
		return nil, err
	}

	resp := &PaymentStatusResponse{
		Success: sess.PaymentStatus == "paid",
		Status:  string(sess.Status),
	}

	if sess.PaymentIntent != nil {
		resp.PaymentIntentID = sess.PaymentIntent.ID
	}
	if sess.Subscription != nil {
		resp.SubscriptionID = sess.Subscription.ID
	}
	if sess.Customer != nil {
		resp.CustomerID = sess.Customer.ID
	}

	return resp, nil
}

// GetSubscription retrieves the subscription for a user
func (s *Service) GetSubscription(ctx context.Context, userID string) (*SubscriptionResponse, error) {
	q := queries.New(s.db.Main)
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	return s.mapSubscriptionToResponse(&sub), nil
}

// CancelSubscription cancels a user's subscription
func (s *Service) CancelSubscription(ctx context.Context, userID string, cancelAtPeriodEnd bool) error {
	q := queries.New(s.db.Main)

	// Get the subscription
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSubscriptionNotFound
		}
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	// If there's a Stripe subscription, cancel it
	if sub.StripeSubscriptionId.Valid && sub.StripeSubscriptionId.String != "" {
		_, err = s.stripe.CancelSubscription(sub.StripeSubscriptionId.String, cancelAtPeriodEnd)
		if err != nil {
			return err
		}
	}

	// Update the database
	status := StatusCanceled
	if cancelAtPeriodEnd {
		status = StatusActive // Still active until period end
	}

	err = q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
		ID:                sub.ID,
		CancelAtPeriodEnd: cancelAtPeriodEnd,
		Status:            string(status),
	})
	if err != nil {
		return fmt.Errorf("failed to update subscription: %w", err)
	}

	s.logger.Info("subscription canceled",
		"user_id", userID,
		"subscription_id", sub.ID,
		"cancel_at_period_end", cancelAtPeriodEnd,
	)

	return nil
}

// ReactivateSubscription reactivates a canceled subscription
func (s *Service) ReactivateSubscription(ctx context.Context, userID string) error {
	q := queries.New(s.db.Main)

	// Get the subscription
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSubscriptionNotFound
		}
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	// If there's a Stripe subscription, reactivate it
	if sub.StripeSubscriptionId.Valid && sub.StripeSubscriptionId.String != "" {
		_, err = s.stripe.ReactivateSubscription(sub.StripeSubscriptionId.String)
		if err != nil {
			return err
		}
	}

	// Update the database
	err = q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
		ID:     sub.ID,
		Status: string(StatusActive),
	})
	if err != nil {
		return fmt.Errorf("failed to update subscription: %w", err)
	}

	s.logger.Info("subscription reactivated",
		"user_id", userID,
		"subscription_id", sub.ID,
	)

	return nil
}

// CreateFreeSubscription creates a free tier subscription
func (s *Service) CreateFreeSubscription(ctx context.Context, userID string) (*SubscriptionResponse, error) {
	q := queries.New(s.db.Main)

	// Check if subscription already exists
	existing, err := q.GetSubscriptionByUserID(ctx, userID)
	if err == nil {
		// Subscription already exists
		return s.mapSubscriptionToResponse(&existing), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check existing subscription: %w", err)
	}

	// Create new free subscription
	subID := uuid.New().String()
	now := time.Now()

	sub, err := q.CreateSubscription(ctx, queries.CreateSubscriptionParams{
		ID:     subID,
		UserId: userID,
		Tier:   string(TierFree),
		Status: string(StatusFree),
		CurrentPeriodStart: pgtype.Timestamp{
			Time:  now,
			Valid: true,
		},
		CurrentPeriodEnd: pgtype.Timestamp{
			Time:  now.AddDate(100, 0, 0), // Far future for free tier
			Valid: true,
		},
		CancelAtPeriodEnd: false,
		Metadata:          []byte("{}"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create subscription: %w", err)
	}

	s.logger.Info("free subscription created",
		"user_id", userID,
		"subscription_id", sub.ID,
	)

	return s.mapSubscriptionToResponse(&sub), nil
}

// CreateBillingPortalSession creates a billing portal session for a user
func (s *Service) CreateBillingPortalSession(ctx context.Context, userID, returnURL string) (*PortalSessionResponse, error) {
	q := queries.New(s.db.Main)

	// Get the subscription to find the customer ID
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	if !sub.StripeCustomerId.Valid || sub.StripeCustomerId.String == "" {
		return nil, ErrCustomerNotFound
	}

	return s.stripe.CreateBillingPortalSession(sub.StripeCustomerId.String, returnURL)
}

// UpgradeSubscription upgrades a subscription to a new tier
func (s *Service) UpgradeSubscription(ctx context.Context, userID string, newTier SubscriptionTier) error {
	q := queries.New(s.db.Main)

	// Get current subscription
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSubscriptionNotFound
		}
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	// Get the price ID for the new tier
	priceID := s.stripe.GetPriceIDForTier(newTier)
	if priceID == "" {
		return ErrInvalidTier
	}

	// If there's a Stripe subscription, update it
	if sub.StripeSubscriptionId.Valid && sub.StripeSubscriptionId.String != "" {
		_, err = s.stripe.UpdateSubscriptionTier(sub.StripeSubscriptionId.String, priceID)
		if err != nil {
			return err
		}
	}

	// Update the database
	err = q.UpdateSubscriptionTier(ctx, queries.UpdateSubscriptionTierParams{
		ID:   sub.ID,
		Tier: string(newTier),
		StripePriceId: pgtype.Text{
			String: priceID,
			Valid:  true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update subscription tier: %w", err)
	}

	s.logger.Info("subscription upgraded",
		"user_id", userID,
		"subscription_id", sub.ID,
		"new_tier", newTier,
	)

	return nil
}

// GetUserInvoices retrieves invoices for a user
func (s *Service) GetUserInvoices(ctx context.Context, userID string, limit, offset int32) ([]InvoiceResponse, error) {
	q := queries.New(s.db.Main)

	invoices, err := q.ListUserInvoices(ctx, queries.ListUserInvoicesParams{
		UserId: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get invoices: %w", err)
	}

	var result []InvoiceResponse
	for _, inv := range invoices {
		result = append(result, s.mapInvoiceToResponse(&inv))
	}

	return result, nil
}

// GetUserReceipts retrieves receipts for a user
func (s *Service) GetUserReceipts(ctx context.Context, userID string, limit, offset int32) ([]ReceiptResponse, error) {
	q := queries.New(s.db.Main)

	receipts, err := q.ListUserReceipts(ctx, queries.ListUserReceiptsParams{
		UserId: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get receipts: %w", err)
	}

	var result []ReceiptResponse
	for _, rec := range receipts {
		result = append(result, s.mapReceiptToResponse(&rec))
	}

	return result, nil
}

// RecordBillingAuditLog records a billing event for audit purposes
func (s *Service) RecordBillingAuditLog(ctx context.Context, params queries.CreateBillingAuditLogParams) error {
	q := queries.New(s.db.Main)

	_, err := q.CreateBillingAuditLog(ctx, params)
	if err != nil {
		s.logger.Error("failed to create billing audit log",
			"error", err,
			"event_type", params.EventType,
		)
		return err
	}
	return nil
}

// RecordCheckoutEvidence records evidence for chargeback protection
func (s *Service) RecordCheckoutEvidence(ctx context.Context, params queries.CreateCheckoutEvidenceParams) error {
	q := queries.New(s.db.Main)

	_, err := q.CreateCheckoutEvidence(ctx, params)
	if err != nil {
		s.logger.Error("failed to create checkout evidence",
			"error", err,
			"user_id", params.UserId,
		)
		return err
	}
	return nil
}

// GetCheckoutEvidence retrieves checkout evidence for a payment intent
func (s *Service) GetCheckoutEvidence(ctx context.Context, paymentIntentID string) (*ChargebackEvidence, error) {
	q := queries.New(s.db.Main)

	evidence, err := q.GetCheckoutEvidenceByPaymentIntent(ctx, pgtype.Text{
		String: paymentIntentID,
		Valid:  true,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get checkout evidence: %w", err)
	}

	// Parse billing address
	var address map[string]interface{}
	if err := json.Unmarshal(evidence.BillingAddress, &address); err != nil {
		address = nil
	}
	addressStr := ""
	if address != nil {
		if line1, ok := address["line1"].(string); ok {
			addressStr = line1
		}
		if city, ok := address["city"].(string); ok {
			addressStr += ", " + city
		}
		if state, ok := address["state"].(string); ok {
			addressStr += ", " + state
		}
		if postal, ok := address["postal_code"].(string); ok {
			addressStr += " " + postal
		}
	}

	return &ChargebackEvidence{
		CustomerEmail:      evidence.BillingEmail,
		CustomerName:       evidence.BillingName.String,
		BillingAddress:     addressStr,
		ProductDescription: evidence.ProductDescription,
		ServiceDate:        evidence.CreatedAt.Time.Format("2006-01-02"),
		CustomerSignature:  fmt.Sprintf("Terms accepted at: %s", evidence.TermsAcceptedAt.Time.Format(time.RFC3339)),
		RefundPolicy:       "14-day refund policy as agreed at checkout",
	}, nil
}

// Helper methods

func (s *Service) mapSubscriptionToResponse(sub *queries.Subscription) *SubscriptionResponse {
	resp := &SubscriptionResponse{
		ID:                sub.ID,
		Tier:              SubscriptionTier(sub.Tier.(string)),
		Status:            SubscriptionStatus(sub.Status.(string)),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		CreatedAt:         sub.CreatedAt.Time,
	}

	if sub.StripeSubscriptionId.Valid {
		resp.StripeSubscriptionID = sub.StripeSubscriptionId.String
	}
	if sub.CurrentPeriodStart.Valid {
		resp.CurrentPeriodStart = &sub.CurrentPeriodStart.Time
	}
	if sub.CurrentPeriodEnd.Valid {
		resp.CurrentPeriodEnd = &sub.CurrentPeriodEnd.Time
	}
	if sub.TrialEnd.Valid {
		resp.TrialEnd = &sub.TrialEnd.Time
	}

	return resp
}

func (s *Service) mapInvoiceToResponse(inv *queries.Invoice) InvoiceResponse {
	resp := InvoiceResponse{
		ID:              inv.ID,
		StripeInvoiceID: inv.StripeInvoiceId,
		Status:          inv.Status.(string),
		Subtotal:        int64(inv.Subtotal),
		TaxAmount:       int64(inv.TaxAmount),
		Total:           int64(inv.Total),
		AmountPaid:      int64(inv.AmountPaid),
		AmountDue:       int64(inv.AmountDue),
		Currency:        inv.Currency,
		CreatedAt:       inv.CreatedAt.Time,
	}

	if inv.InvoiceNumber.Valid {
		resp.InvoiceNumber = inv.InvoiceNumber.String
	}
	if inv.DueDate.Valid {
		resp.DueDate = &inv.DueDate.Time
	}
	if inv.PaidAt.Valid {
		resp.PaidAt = &inv.PaidAt.Time
	}
	if inv.HostedInvoiceUrl.Valid {
		resp.HostedInvoiceURL = inv.HostedInvoiceUrl.String
	}
	if inv.InvoicePdfUrl.Valid {
		resp.InvoicePDFURL = inv.InvoicePdfUrl.String
	}
	if inv.Description.Valid {
		resp.Description = inv.Description.String
	}
	if inv.ProductType != nil {
		resp.ProductType = inv.ProductType.(string)
	}

	return resp
}

func (s *Service) mapReceiptToResponse(rec *queries.Receipt) ReceiptResponse {
	resp := ReceiptResponse{
		ID:                 rec.ID,
		ReceiptNumber:      rec.ReceiptNumber,
		Amount:             int64(rec.Amount),
		Currency:           rec.Currency,
		ProductDescription: rec.ProductDescription,
		CreatedAt:          rec.CreatedAt.Time,
	}

	if rec.PaymentMethod.Valid {
		resp.PaymentMethod = rec.PaymentMethod.String
	}
	if rec.CardBrand.Valid {
		resp.CardBrand = rec.CardBrand.String
	}
	if rec.CardLast4.Valid {
		resp.CardLast4 = rec.CardLast4.String
	}
	if rec.PaidAt.Valid {
		resp.PaidAt = &rec.PaidAt.Time
	}
	if rec.StripeReceiptUrl.Valid {
		resp.StripeReceiptURL = rec.StripeReceiptUrl.String
	}

	return resp
}
