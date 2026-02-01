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
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/webhook"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/internal/services/email"
)

// WebhookService handles Stripe webhook events
type WebhookService struct {
	db     *postgres.DB
	cfg    *config.Config
	logger *slog.Logger
}

// NewWebhookService creates a new webhook service
func NewWebhookService(db *postgres.DB, cfg *config.Config) *WebhookService {
	return &WebhookService{
		db:     db,
		cfg:    cfg,
		logger: slog.Default().With("component", "webhook_service"),
	}
}

// VerifyAndParseEvent verifies the webhook signature and parses the event
func (s *WebhookService) VerifyAndParseEvent(payload []byte, signature string) (*stripe.Event, error) {
	// Allow newer Stripe API versions from CLI/test fixtures.
	event, err := webhook.ConstructEventWithOptions(payload, signature, s.cfg.Stripe.WebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("webhook signature verification failed: %w", err)
	}
	return &event, nil
}

// HandleEvent processes a Stripe webhook event
func (s *WebhookService) HandleEvent(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	s.logger.Info("processing webhook event",
		"event_id", event.ID,
		"event_type", event.Type,
	)

	switch event.Type {
	case "checkout.session.completed":
		return s.handleCheckoutSessionCompleted(ctx, event, ipAddress, userAgent)
	case "checkout.session.expired":
		return s.handleCheckoutSessionExpired(ctx, event, ipAddress, userAgent)
	case "customer.subscription.created":
		return s.handleSubscriptionCreated(ctx, event, ipAddress, userAgent)
	case "customer.subscription.updated":
		return s.handleSubscriptionUpdated(ctx, event, ipAddress, userAgent)
	case "customer.subscription.deleted":
		return s.handleSubscriptionDeleted(ctx, event, ipAddress, userAgent)
	case "customer.subscription.trial_will_end":
		return s.handleTrialWillEnd(ctx, event, ipAddress, userAgent)
	case "invoice.payment_succeeded":
		return s.handleInvoicePaymentSucceeded(ctx, event, ipAddress, userAgent)
	case "invoice.payment_failed":
		return s.handleInvoicePaymentFailed(ctx, event, ipAddress, userAgent)
	case "charge.dispute.created":
		return s.handleDisputeCreated(ctx, event, ipAddress, userAgent)
	case "charge.dispute.updated":
		return s.handleDisputeUpdated(ctx, event, ipAddress, userAgent)
	case "charge.dispute.closed":
		return s.handleDisputeClosed(ctx, event, ipAddress, userAgent)
	default:
		s.logger.Debug("unhandled webhook event type", "type", event.Type)
		return nil
	}
}

func (s *WebhookService) handleCheckoutSessionCompleted(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return fmt.Errorf("failed to parse checkout session: %w", err)
	}

	s.logger.Info("checkout session completed",
		"session_id", session.ID,
		"customer_id", session.Customer.ID,
		"payment_status", session.PaymentStatus,
	)

	// Get user ID from metadata
	userID := session.Metadata["userId"]
	if userID == "" {
		s.logger.Warn("checkout session missing userId metadata", "session_id", session.ID)
		// Try to find user by email
	}

	// Record billing audit log
	eventData, _ := json.Marshal(session)
	if err := s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:               uuid.New().String(),
		UserId:           pgtype.Text{String: userID, Valid: userID != ""},
		StripeCustomerId: pgtype.Text{String: session.Customer.ID, Valid: session.Customer != nil},
		EventType:        "CHECKOUT_COMPLETED",
		EventData:        eventData,
		IpAddress:        pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:        pgtype.Text{String: userAgent, Valid: userAgent != ""},
	}); err != nil {
		s.logger.Error("failed to record audit log", "error", err)
	}

	// If this is a subscription checkout, create subscription record
	if session.Subscription != nil && userID != "" {
		if err := s.createOrUpdateSubscription(ctx, userID, session.Subscription.ID, session.Customer.ID); err != nil {
			s.logger.Error("failed to create subscription", "error", err)
			return err
		}
	}

	return nil
}

func (s *WebhookService) handleCheckoutSessionExpired(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return fmt.Errorf("failed to parse checkout session: %w", err)
	}

	s.logger.Info("checkout session expired", "session_id", session.ID)

	// Record billing audit log
	eventData, _ := json.Marshal(session)
	userID := session.Metadata["userId"]
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:               uuid.New().String(),
		UserId:           pgtype.Text{String: userID, Valid: userID != ""},
		StripeCustomerId: pgtype.Text{String: session.Customer.ID, Valid: session.Customer != nil},
		EventType:        "CHECKOUT_EXPIRED",
		EventData:        eventData,
		IpAddress:        pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:        pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleSubscriptionCreated(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to parse subscription: %w", err)
	}

	s.logger.Info("subscription created",
		"subscription_id", subscription.ID,
		"customer_id", subscription.Customer.ID,
		"status", subscription.Status,
	)

	if user, err := s.findUserForStripe(ctx, subscription.Customer.ID, subscription.ID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)
		if _, err := emailSvc.SendSubscriptionActivated(user.Email, s.userFirstName(user)); err != nil {
			s.logger.Warn("failed to send subscription created email", "error", err)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for subscription created email", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(subscription)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: subscription.Customer.ID, Valid: true},
		StripeSubscriptionId: pgtype.Text{String: subscription.ID, Valid: true},
		EventType:            "SUBSCRIPTION_CREATED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleSubscriptionUpdated(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to parse subscription: %w", err)
	}

	s.logger.Info("subscription updated",
		"subscription_id", subscription.ID,
		"status", subscription.Status,
		"cancel_at_period_end", subscription.CancelAtPeriodEnd,
	)

	q := queries.New(s.db.Main)

	// Find the subscription in our database
	sub, err := q.GetSubscriptionByStripeID(ctx, pgtype.Text{
		String: subscription.ID,
		Valid:  true,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	if err == nil {
		// Update the subscription status
		if err := q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
			ID:     sub.ID,
			Status: s.mapStripeStatus(subscription.Status),
		}); err != nil {
			s.logger.Error("failed to update subscription status", "error", err)
		}

		// Update period
		if err := q.UpdateSubscriptionPeriod(ctx, queries.UpdateSubscriptionPeriodParams{
			ID: sub.ID,
			CurrentPeriodStart: pgtype.Timestamp{
				Time:  time.Unix(subscription.CurrentPeriodStart, 0),
				Valid: true,
			},
			CurrentPeriodEnd: pgtype.Timestamp{
				Time:  time.Unix(subscription.CurrentPeriodEnd, 0),
				Valid: true,
			},
		}); err != nil {
			s.logger.Error("failed to update subscription period", "error", err)
		}
	}

	// Record billing audit log
	eventData, _ := json.Marshal(subscription)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: subscription.Customer.ID, Valid: true},
		StripeSubscriptionId: pgtype.Text{String: subscription.ID, Valid: true},
		EventType:            "SUBSCRIPTION_UPDATED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleSubscriptionDeleted(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to parse subscription: %w", err)
	}

	s.logger.Info("subscription deleted", "subscription_id", subscription.ID)

	q := queries.New(s.db.Main)

	// Find and cancel the subscription in our database
	sub, err := q.GetSubscriptionByStripeID(ctx, pgtype.Text{
		String: subscription.ID,
		Valid:  true,
	})
	if err == nil {
		if err := q.CancelSubscription(ctx, queries.CancelSubscriptionParams{
			ID:                sub.ID,
			CancelAtPeriodEnd: false,
			Status:            string(StatusCanceled),
		}); err != nil {
			s.logger.Error("failed to cancel subscription", "error", err)
		}
	}

	if user, err := s.findUserForStripe(ctx, subscription.Customer.ID, subscription.ID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)
		var endDate *time.Time
		if subscription.CurrentPeriodEnd > 0 {
			t := time.Unix(subscription.CurrentPeriodEnd, 0)
			endDate = &t
		}
		if _, err := emailSvc.SendSubscriptionCancelled(user.Email, s.userFirstName(user), endDate); err != nil {
			s.logger.Warn("failed to send subscription cancelled email", "error", err)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for subscription cancelled email", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(subscription)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: subscription.Customer.ID, Valid: true},
		StripeSubscriptionId: pgtype.Text{String: subscription.ID, Valid: true},
		EventType:            "SUBSCRIPTION_DELETED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleTrialWillEnd(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to parse subscription: %w", err)
	}

	s.logger.Info("trial will end",
		"subscription_id", subscription.ID,
		"trial_end", time.Unix(subscription.TrialEnd, 0),
	)

	if user, err := s.findUserForStripe(ctx, subscription.Customer.ID, subscription.ID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)
		trialEnd := time.Unix(subscription.TrialEnd, 0)
		if _, err := emailSvc.SendTrialEnding(user.Email, s.userFirstName(user), trialEnd); err != nil {
			s.logger.Warn("failed to send trial ending email", "error", err)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for trial ending email", "error", err)
	}

	// Record billing audit log (map trial_will_end to a valid enum)
	eventData, _ := json.Marshal(subscription)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: subscription.Customer.ID, Valid: true},
		StripeSubscriptionId: pgtype.Text{String: subscription.ID, Valid: true},
		EventType:            "SUBSCRIPTION_UPDATED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleInvoicePaymentSucceeded(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		return fmt.Errorf("failed to parse invoice: %w", err)
	}

	customerID := ""
	if invoice.Customer != nil {
		customerID = invoice.Customer.ID
	}
	subscriptionID := ""
	if invoice.Subscription != nil {
		subscriptionID = invoice.Subscription.ID
	}

	s.logger.Info("invoice payment succeeded",
		"invoice_id", invoice.ID,
		"amount_paid", invoice.AmountPaid,
	)

	// Create invoice record
	if err := s.createInvoiceRecord(ctx, &invoice); err != nil {
		s.logger.Error("failed to create invoice record", "error", err)
	}

	if user, err := s.findUserForStripe(ctx, customerID, subscriptionID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)
		if _, err := emailSvc.SendPaymentSucceeded(user.Email, s.userFirstName(user), invoice.AmountPaid, string(invoice.Currency), invoice.HostedInvoiceURL); err != nil {
			s.logger.Warn("failed to send payment success email", "error", err)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for payment success email", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(invoice)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: customerID, Valid: customerID != ""},
		StripeSubscriptionId: pgtype.Text{String: subscriptionID, Valid: subscriptionID != ""},
		StripeInvoiceId:      pgtype.Text{String: invoice.ID, Valid: true},
		EventType:            "PAYMENT_SUCCEEDED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleInvoicePaymentFailed(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		return fmt.Errorf("failed to parse invoice: %w", err)
	}

	customerID := ""
	if invoice.Customer != nil {
		customerID = invoice.Customer.ID
	}
	subscriptionID := ""
	if invoice.Subscription != nil {
		subscriptionID = invoice.Subscription.ID
	}

	s.logger.Warn("invoice payment failed",
		"invoice_id", invoice.ID,
		"customer_id", customerID,
	)

	q := queries.New(s.db.Main)

	// Update subscription status to past due if applicable
	if invoice.Subscription != nil {
		sub, err := q.GetSubscriptionByStripeID(ctx, pgtype.Text{
			String: invoice.Subscription.ID,
			Valid:  true,
		})
		if err == nil {
			if err := q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
				ID:     sub.ID,
				Status: string(StatusPastDue),
			}); err != nil {
				s.logger.Error("failed to update subscription to past due", "error", err)
			}
		}
	}

	if user, err := s.findUserForStripe(ctx, customerID, subscriptionID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)
		if _, err := emailSvc.SendPaymentFailed(user.Email, s.userFirstName(user), invoice.AmountDue, string(invoice.Currency)); err != nil {
			s.logger.Warn("failed to send payment failed email", "error", err)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for payment failed email", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(invoice)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: customerID, Valid: customerID != ""},
		StripeSubscriptionId: pgtype.Text{String: subscriptionID, Valid: subscriptionID != ""},
		StripeInvoiceId:      pgtype.Text{String: invoice.ID, Valid: true},
		EventType:            "PAYMENT_FAILED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleDisputeCreated(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var dispute stripe.Dispute
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		return fmt.Errorf("failed to parse dispute: %w", err)
	}

	s.logger.Warn("dispute created",
		"dispute_id", dispute.ID,
		"charge_id", dispute.Charge.ID,
		"amount", dispute.Amount,
		"reason", dispute.Reason,
	)

	// TODO: Send dispute notification to admin
	// TODO: Auto-submit evidence if available

	// Record billing audit log
	eventData, _ := json.Marshal(dispute)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:        uuid.New().String(),
		EventType: "DISPUTE_CREATED",
		EventData: eventData,
		IpAddress: pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent: pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleDisputeUpdated(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var dispute stripe.Dispute
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		return fmt.Errorf("failed to parse dispute: %w", err)
	}

	s.logger.Info("dispute updated",
		"dispute_id", dispute.ID,
		"status", dispute.Status,
	)

	// Record billing audit log
	eventData, _ := json.Marshal(dispute)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:        uuid.New().String(),
		EventType: "DISPUTE_UPDATED",
		EventData: eventData,
		IpAddress: pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent: pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleDisputeClosed(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
	var dispute stripe.Dispute
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		return fmt.Errorf("failed to parse dispute: %w", err)
	}

	s.logger.Info("dispute closed",
		"dispute_id", dispute.ID,
		"status", dispute.Status,
	)

	// Record billing audit log
	eventData, _ := json.Marshal(dispute)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:        uuid.New().String(),
		EventType: "DISPUTE_CLOSED",
		EventData: eventData,
		IpAddress: pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent: pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

// Helper methods

func (s *WebhookService) recordAuditLog(ctx context.Context, params queries.CreateBillingAuditLogParams) error {
	eventType := params.EventType
	if value, ok := params.EventType.(string); ok {
		eventType = value
	}

	_, err := s.db.Main.Exec(ctx, `
		INSERT INTO billing_audit_logs (
			id, "userId", "stripeCustomerId", "stripeSubscriptionId",
			"stripePaymentIntentId", "stripeInvoiceId",
			"appleOriginalTransactionId", "appleTransactionId", "appleProductId", "appleEnvironment",
			"eventType", "eventData", "ipAddress", "userAgent", "createdAt"
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW()
		)
	`, params.ID, params.UserId, params.StripeCustomerId, params.StripeSubscriptionId,
		params.StripePaymentIntentId, params.StripeInvoiceId, params.AppleOriginalTransactionId,
		params.AppleTransactionId, params.AppleProductId, params.AppleEnvironment, eventType,
		params.EventData, params.IpAddress, params.UserAgent,
	)
	return err
}

func (s *WebhookService) createOrUpdateSubscription(ctx context.Context, userID, stripeSubID, stripeCustomerID string) error {
	q := queries.New(s.db.Main)

	// Get the Stripe subscription details
	stripe.Key = s.cfg.Stripe.SecretKey
	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// Determine tier from price
	tier := string(TierInvestor) // Default tier

	if errors.Is(err, pgx.ErrNoRows) {
		// Create new subscription
		_, err = q.CreateSubscription(ctx, queries.CreateSubscriptionParams{
			ID:     uuid.New().String(),
			UserId: userID,
			StripeSubscriptionId: pgtype.Text{
				String: stripeSubID,
				Valid:  true,
			},
			StripeCustomerId: pgtype.Text{
				String: stripeCustomerID,
				Valid:  true,
			},
			Tier:   tier,
			Status: string(StatusActive),
			CurrentPeriodStart: pgtype.Timestamp{
				Time:  time.Now(),
				Valid: true,
			},
			CurrentPeriodEnd: pgtype.Timestamp{
				Time:  time.Now().AddDate(0, 1, 0), // 1 month
				Valid: true,
			},
			CancelAtPeriodEnd: false,
			Metadata:          []byte("{}"),
		})
		return err
	}

	// Update existing subscription
	return q.UpdateSubscriptionStatus(ctx, queries.UpdateSubscriptionStatusParams{
		ID:     sub.ID,
		Status: string(StatusActive),
	})
}

func (s *WebhookService) createInvoiceRecord(ctx context.Context, inv *stripe.Invoice) error {
	q := queries.New(s.db.Main)

	customerID := ""
	if inv.Customer != nil {
		customerID = inv.Customer.ID
	}
	subscriptionID := ""
	if inv.Subscription != nil {
		subscriptionID = inv.Subscription.ID
	}

	// Find user by Stripe customer ID
	userID := ""
	if inv.Subscription != nil {
		sub, err := q.GetSubscriptionByStripeID(ctx, pgtype.Text{
			String: inv.Subscription.ID,
			Valid:  true,
		})
		if err == nil {
			userID = sub.UserId
		}
	}

	if userID == "" {
		s.logger.Warn("could not find user for invoice", "invoice_id", inv.ID)
		return nil
	}

	// Check if invoice already exists
	_, err := q.GetInvoiceByStripeID(ctx, inv.ID)
	if err == nil {
		// Invoice already exists, update it
		paidAt := pgtype.Timestamp{}
		if inv.StatusTransitions != nil && inv.StatusTransitions.PaidAt > 0 {
			paidAt = pgtype.Timestamp{
				Time:  time.Unix(inv.StatusTransitions.PaidAt, 0),
				Valid: true,
			}
		}
		return q.UpdateInvoiceStatus(ctx, queries.UpdateInvoiceStatusParams{
			ID:         inv.ID,
			Status:     string(inv.Status),
			PaidAt:     paidAt,
			AmountPaid: int32(inv.AmountPaid),
		})
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// Create new invoice
	_, err = q.CreateInvoice(ctx, queries.CreateInvoiceParams{
		ID:              uuid.New().String(),
		StripeInvoiceId: inv.ID,
		StripeCustomerId: customerID,
		StripeSubscriptionId: pgtype.Text{
			String: subscriptionID,
			Valid:  subscriptionID != "",
		},
		UserId: userID,
		InvoiceNumber: pgtype.Text{
			String: inv.Number,
			Valid:  inv.Number != "",
		},
		Status:     string(inv.Status),
		Subtotal:   int32(inv.Subtotal),
		TaxAmount:  int32(inv.Tax),
		Total:      int32(inv.Total),
		AmountPaid: int32(inv.AmountPaid),
		AmountDue:  int32(inv.AmountDue),
		Currency:   string(inv.Currency),
		DueDate: pgtype.Timestamp{
			Time:  time.Unix(inv.DueDate, 0),
			Valid: inv.DueDate > 0,
		},
		HostedInvoiceUrl: pgtype.Text{
			String: inv.HostedInvoiceURL,
			Valid:  inv.HostedInvoiceURL != "",
		},
		InvoicePdfUrl: pgtype.Text{
			String: inv.InvoicePDF,
			Valid:  inv.InvoicePDF != "",
		},
		ProductType:    string(ProductSubscription),
		EmailDelivered: false,
	})

	return err
}

func (s *WebhookService) findUserForStripe(ctx context.Context, customerID, subscriptionID string) (*queries.User, error) {
	q := queries.New(s.db.Main)

	if subscriptionID != "" {
		sub, err := q.GetSubscriptionByStripeID(ctx, pgtype.Text{
			String: subscriptionID,
			Valid:  true,
		})
		if err == nil {
			user, err := q.GetUserByID(ctx, sub.UserId)
			if err == nil {
				return &user, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	if customerID == "" {
		return nil, nil
	}

	user, err := q.GetUserByStripeCustomerID(ctx, pgtype.Text{
		String: customerID,
		Valid:  true,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &user, nil
}

func (s *WebhookService) userFirstName(user *queries.User) string {
	if user == nil {
		return ""
	}
	if user.FirstName.Valid {
		return user.FirstName.String
	}
	return ""
}

func (s *WebhookService) mapStripeStatus(status stripe.SubscriptionStatus) string {
	switch status {
	case stripe.SubscriptionStatusActive:
		return string(StatusActive)
	case stripe.SubscriptionStatusTrialing:
		return string(StatusTrialing)
	case stripe.SubscriptionStatusCanceled:
		return string(StatusCanceled)
	case stripe.SubscriptionStatusPastDue:
		return string(StatusPastDue)
	case stripe.SubscriptionStatusUnpaid:
		return string(StatusUnpaid)
	case stripe.SubscriptionStatusIncomplete:
		return string(StatusIncomplete)
	default:
		return string(StatusActive)
	}
}
