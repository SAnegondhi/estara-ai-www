package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/webhook"
	"golang.org/x/crypto/bcrypt"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/crypto"
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
	case "invoice.created":
		return s.handleInvoiceCreated(ctx, event, ipAddress, userAgent)
	case "invoice.finalized":
		return s.handleInvoiceFinalized(ctx, event, ipAddress, userAgent)
	case "invoice.upcoming":
		return s.handleInvoiceUpcoming(ctx, event, ipAddress, userAgent)
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
		"signup_type", session.Metadata["signupType"],
	)

	// Check if this is a guest checkout (new user signup with payment)
	signupType := session.Metadata["signupType"]
	if signupType == "guest_checkout" {
		return s.handleGuestCheckoutCompleted(ctx, &session, ipAddress, userAgent)
	}

	// Existing user checkout flow
	userID := session.Metadata["userId"]
	if userID == "" {
		s.logger.Warn("checkout session missing userId metadata", "session_id", session.ID)
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

// handleGuestCheckoutCompleted handles guest checkout - creates user AFTER successful payment
// This mirrors the www_v1 handleGuestCheckoutCompleted function
func (s *WebhookService) handleGuestCheckoutCompleted(ctx context.Context, session *stripe.CheckoutSession, ipAddress, userAgent string) error {
	// Extract user data from metadata
	pendingEmail := session.Metadata["pendingEmail"]
	pendingPassword := session.Metadata["pendingPassword"]
	pendingFirstName := session.Metadata["pendingFirstName"]
	pendingLastName := session.Metadata["pendingLastName"]
	tier := session.Metadata["tier"]

	if pendingEmail == "" || pendingPassword == "" {
		s.logger.Error("guest checkout missing required signup data",
			"session_id", session.ID,
			"has_email", pendingEmail != "",
			"has_password", pendingPassword != "",
		)
		return fmt.Errorf("missing required signup data in session metadata")
	}

	s.logger.Info("guest checkout payment successful - creating user account",
		"email", pendingEmail,
		"tier", tier,
	)

	q := queries.New(s.db.Main)

	// Normalize email
	userEmail := strings.ToLower(strings.TrimSpace(pendingEmail))

	// Check if user already exists (edge case - duplicate webhook)
	existingUser, err := q.GetUserByEmail(ctx, userEmail)
	if err == nil {
		s.logger.Warn("user already exists - updating subscription",
			"email", userEmail,
			"user_id", existingUser.ID,
		)

		// Update subscription for existing user
		if session.Subscription != nil && session.Subscription.ID != "" {
			if err := s.createOrUpdateSubscription(ctx, existingUser.ID, session.Subscription.ID, session.Customer.ID); err != nil {
				s.logger.Error("failed to update subscription for existing user", "error", err)
				return err
			}
		}

		// Ensure V2 evaluation quota exists (required for Insight login)
		existingTier := s.mapTierToSubscriptionTier(tier)
		if err := s.upsertV2EvaluationQuota(ctx, existingUser.ID, existingTier); err != nil {
			s.logger.Error("failed to upsert V2 evaluation quota for existing user", "error", err)
			return err
		}

		// Record audit log
		eventData, _ := json.Marshal(session)
		s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
			ID:               uuid.New().String(),
			UserId:           pgtype.Text{String: existingUser.ID, Valid: true},
			StripeCustomerId: pgtype.Text{String: session.Customer.ID, Valid: session.Customer != nil},
			EventType:        "CHECKOUT_COMPLETED",
			EventData:        eventData,
			IpAddress:        pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
			UserAgent:        pgtype.Text{String: userAgent, Valid: userAgent != ""},
		})

		return nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to check for existing user: %w", err)
	}

	// Decrypt password (encrypted by website before storing in Stripe metadata)
	var plainPassword string
	if s.cfg.Security.CheckoutEncryptionKey != "" {
		decrypted, err := crypto.DecryptPassword(pendingPassword, s.cfg.Security.CheckoutEncryptionKey)
		if err != nil {
			s.logger.Error("failed to decrypt password", "error", err)
			return fmt.Errorf("failed to decrypt password: %w", err)
		}
		plainPassword = decrypted
	} else {
		// Fallback for backwards compatibility (no encryption key configured)
		s.logger.Warn("CHECKOUT_ENCRYPTION_KEY not set - using password as-is (insecure)")
		plainPassword = pendingPassword
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(plainPassword), 12)
	if err != nil {
		s.logger.Error("failed to hash password", "error", err)
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Create user
	userID := uuid.New().String()
	stripeCustomerID := ""
	if session.Customer != nil {
		stripeCustomerID = session.Customer.ID
	}

	// Map tier to subscription tier
	subscriptionTier := s.mapTierToSubscriptionTier(tier)

	newUser, err := q.CreateUserWithPassword(ctx, queries.CreateUserWithPasswordParams{
		ID:               userID,
		Email:            userEmail,
		Password:         pgtype.Text{String: string(hashedPassword), Valid: true},
		FirstName:        pgtype.Text{String: strings.TrimSpace(pendingFirstName), Valid: pendingFirstName != ""},
		LastName:         pgtype.Text{String: strings.TrimSpace(pendingLastName), Valid: pendingLastName != ""},
		Role:             "USER",
		SubscriptionTier: pgtype.Text{String: subscriptionTier, Valid: true},
		StripeCustomerId: pgtype.Text{String: stripeCustomerID, Valid: stripeCustomerID != ""},
	})
	if err != nil {
		s.logger.Error("failed to create user", "error", err, "email", userEmail)
		return fmt.Errorf("failed to create user: %w", err)
	}

	s.logger.Info("user created successfully",
		"user_id", newUser.ID,
		"email", userEmail,
		"tier", subscriptionTier,
	)

	// Create subscription record
	if session.Subscription != nil && session.Subscription.ID != "" {
		if err := s.createOrUpdateSubscription(ctx, newUser.ID, session.Subscription.ID, stripeCustomerID); err != nil {
			s.logger.Error("failed to create subscription", "error", err)
			return err
		}
	} else {
		s.logger.Warn("checkout session has no subscription object - subscription will be created by customer.subscription.created webhook",
			"user_id", newUser.ID,
			"session_id", session.ID,
		)
	}

	// Create V2 evaluation quota (required for Insight login)
	if err := s.upsertV2EvaluationQuota(ctx, newUser.ID, subscriptionTier); err != nil {
		s.logger.Error("failed to create V2 evaluation quota", "error", err, "user_id", newUser.ID)
		return err
	}

	// Record audit log
	eventData, _ := json.Marshal(session)
	s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:               uuid.New().String(),
		UserId:           pgtype.Text{String: newUser.ID, Valid: true},
		StripeCustomerId: pgtype.Text{String: stripeCustomerID, Valid: stripeCustomerID != ""},
		EventType:        "CHECKOUT_COMPLETED",
		EventData:        eventData,
		IpAddress:        pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:        pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})

	// ADR-067: Send rich welcome email with tier features and app link
	emailSvc := email.NewService(s.cfg)
	result, err := emailSvc.SendWelcomeEmail(email.WelcomeEmailParams{
		To:        userEmail,
		FirstName: pendingFirstName,
		Tier:      tier,
	})
	if err != nil {
		s.logger.Warn("failed to send welcome email", "error", err, "email", userEmail)
	} else if result != nil && !result.Success {
		s.logger.Warn("welcome email not delivered", "error", result.Error, "email", userEmail)
	}

	return nil
}

// mapTierToSubscriptionTier maps checkout tier to database subscription tier
func (s *WebhookService) mapTierToSubscriptionTier(tier string) string {
	switch tier {
	case "PROFESSIONAL_ALLOCATOR", "professional_allocator":
		return "professional_allocator"
	case "ANNUAL_ACCESS", "annual_access":
		return "annual_access"
	// Backwards compatibility for old tier names
	case "PROFESSIONAL_ACCESS", "professional":
		return "professional_allocator"
	case "INVESTOR_ACCESS", "investor":
		return "annual_access"
	case "AAPI_INVESTOR":
		return "aapi_investor"
	case "AAPI_ALLOCATOR":
		return "aapi_allocator"
	default:
		return "professional_allocator" // Default tier
	}
}

// mapSubscriptionTierToV2Tier converts a subscription tier to its V2 enum value and annual limit.
func mapSubscriptionTierToV2Tier(subscriptionTier string) (v2Tier string, annualLimit int) {
	switch subscriptionTier {
	case "professional_allocator":
		return "V2_PROFESSIONAL_ALLOCATOR", 1000
	case "annual_access":
		return "V2_ANNUAL_ACCESS", 500
	// Backwards compatibility
	case "professional":
		return "V2_PROFESSIONAL_ALLOCATOR", 1000
	case "investor":
		return "V2_ANNUAL_ACCESS", 500
	default:
		return "V2_ANNUAL_ACCESS", 500
	}
}

// upsertV2EvaluationQuota creates or updates the V2 evaluation quota required for Insight login.
func (s *WebhookService) upsertV2EvaluationQuota(ctx context.Context, userID, subscriptionTier string) error {
	v2Tier, annualLimit := mapSubscriptionTierToV2Tier(subscriptionTier)

	now := time.Now()
	periodEnd := now.AddDate(1, 0, 0) // 1 year from now

	query := `
		INSERT INTO v2_evaluation_quotas (id, user_id, tier, annual_limit, used_this_period, period_start_date, period_end_date, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 0, $5, $6, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			tier = $3,
			annual_limit = $4,
			period_start_date = CASE WHEN v2_evaluation_quotas.period_end_date < NOW() THEN $5 ELSE v2_evaluation_quotas.period_start_date END,
			period_end_date = CASE WHEN v2_evaluation_quotas.period_end_date < NOW() THEN $6 ELSE v2_evaluation_quotas.period_end_date END,
			used_this_period = CASE WHEN v2_evaluation_quotas.period_end_date < NOW() THEN 0 ELSE v2_evaluation_quotas.used_this_period END,
			updated_at = NOW()
	`

	_, err := s.db.Main.Exec(ctx, query,
		uuid.New().String(), // $1: id
		userID,              // $2: user_id
		v2Tier,              // $3: tier
		annualLimit,         // $4: annual_limit
		now,                 // $5: period_start_date
		periodEnd,           // $6: period_end_date
	)
	if err != nil {
		return fmt.Errorf("failed to upsert v2_evaluation_quotas: %w", err)
	}

	s.logger.Info("V2 evaluation quota created",
		"user_id", userID,
		"tier", v2Tier,
		"annual_limit", annualLimit,
	)
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

	customerID := ""
	if subscription.Customer != nil {
		customerID = subscription.Customer.ID
	}

	s.logger.Info("subscription created",
		"subscription_id", subscription.ID,
		"customer_id", customerID,
		"status", subscription.Status,
	)

	// Determine tier from subscription price
	tier := string(TierAnnualAccess)
	if subscription.Items != nil && len(subscription.Items.Data) > 0 && subscription.Items.Data[0].Price != nil {
		stripeClient := NewStripeClient(&s.cfg.Stripe)
		tier = string(stripeClient.GetTierFromPriceID(subscription.Items.Data[0].Price.ID))
	}

	if user, err := s.findUserForStripe(ctx, customerID, subscription.ID); err == nil && user != nil {
		// Create/update subscription record (may not have been created by checkout.session.completed)
		if err := s.createOrUpdateSubscription(ctx, user.ID, subscription.ID, customerID); err != nil {
			s.logger.Error("failed to create subscription record", "error", err)
		}

		// Ensure V2 evaluation quota exists (required for Insight login)
		subscriptionTier := s.mapTierToSubscriptionTier(tier)
		if err := s.upsertV2EvaluationQuota(ctx, user.ID, subscriptionTier); err != nil {
			s.logger.Error("failed to upsert V2 evaluation quota", "error", err)
		}

		// Welcome email is sent by handleGuestCheckoutCompleted; not duplicated here.
	} else if err != nil {
		s.logger.Warn("failed to resolve user for subscription created", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(subscription)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: customerID, Valid: customerID != ""},
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
		result, err := emailSvc.SendSubscriptionCancelled(user.Email, s.userFirstName(user), endDate)
		if err != nil {
			s.logger.Warn("failed to send subscription cancelled email", "error", err)
		} else if result != nil && !result.Success {
			s.logger.Warn("subscription cancelled email not delivered", "error", result.Error, "email", user.Email)
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
		result, err := emailSvc.SendTrialEnding(user.Email, s.userFirstName(user), trialEnd)
		if err != nil {
			s.logger.Warn("failed to send trial ending email", "error", err)
		} else if result != nil && !result.Success {
			s.logger.Warn("trial ending email not delivered", "error", result.Error, "email", user.Email)
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

// ADR-067: Invoice event handlers for billing completeness

func (s *WebhookService) handleInvoiceCreated(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
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

	s.logger.Info("invoice created",
		"invoice_id", invoice.ID,
		"invoice_number", invoice.Number,
		"amount_due", invoice.AmountDue,
	)

	// Create/update invoice record in database
	if err := s.createInvoiceRecord(ctx, &invoice); err != nil {
		s.logger.Error("failed to create invoice record", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(invoice)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: customerID, Valid: customerID != ""},
		StripeSubscriptionId: pgtype.Text{String: subscriptionID, Valid: subscriptionID != ""},
		StripeInvoiceId:      pgtype.Text{String: invoice.ID, Valid: true},
		EventType:            "INVOICE_CREATED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleInvoiceFinalized(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
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

	s.logger.Info("invoice finalized",
		"invoice_id", invoice.ID,
		"invoice_number", invoice.Number,
		"amount_due", invoice.AmountDue,
	)

	q := queries.New(s.db.Main)

	// Update invoice status to OPEN and set URLs
	if err := q.UpdateInvoiceStatusByStripeID(ctx, queries.UpdateInvoiceStatusByStripeIDParams{
		StripeInvoiceId: invoice.ID,
		Status:          "open",
		HostedInvoiceUrl: pgtype.Text{
			String: invoice.HostedInvoiceURL,
			Valid:  invoice.HostedInvoiceURL != "",
		},
		InvoicePdfUrl: pgtype.Text{
			String: invoice.InvoicePDF,
			Valid:  invoice.InvoicePDF != "",
		},
		EmailSentAt:    pgtype.Timestamp{Valid: false}, // Will update after email sent
		EmailDelivered: false,
	}); err != nil {
		s.logger.Warn("failed to update invoice status", "error", err)
	}

	// Send invoice email to user
	if user, err := s.findUserForStripe(ctx, customerID, subscriptionID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)

		invoiceNumber := invoice.Number
		if invoiceNumber == "" {
			invoiceNumber = invoice.ID
		}

		var dueDate *time.Time
		if invoice.DueDate > 0 {
			t := time.Unix(invoice.DueDate, 0)
			dueDate = &t
		}

		result, err := emailSvc.SendInvoiceEmail(email.InvoiceEmailParams{
			To:            user.Email,
			FirstName:     s.userFirstName(user),
			InvoiceNumber: invoiceNumber,
			Amount:        invoice.AmountDue,
			Currency:      string(invoice.Currency),
			DueDate:       dueDate,
			InvoiceURL:    invoice.HostedInvoiceURL,
			PDFURL:        invoice.InvoicePDF,
		})
		if err != nil {
			s.logger.Warn("failed to send invoice email", "error", err)
		} else if result != nil && result.Success {
			// Update invoice with email sent timestamp
			if err := q.UpdateInvoiceStatusByStripeID(ctx, queries.UpdateInvoiceStatusByStripeIDParams{
				StripeInvoiceId: invoice.ID,
				Status:          "open",
				HostedInvoiceUrl: pgtype.Text{
					String: invoice.HostedInvoiceURL,
					Valid:  invoice.HostedInvoiceURL != "",
				},
				InvoicePdfUrl: pgtype.Text{
					String: invoice.InvoicePDF,
					Valid:  invoice.InvoicePDF != "",
				},
				EmailSentAt: pgtype.Timestamp{
					Time:  time.Now(),
					Valid: true,
				},
				EmailDelivered: true,
			}); err != nil {
				s.logger.Warn("failed to update invoice email sent", "error", err)
			}
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for invoice email", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(invoice)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: customerID, Valid: customerID != ""},
		StripeSubscriptionId: pgtype.Text{String: subscriptionID, Valid: subscriptionID != ""},
		StripeInvoiceId:      pgtype.Text{String: invoice.ID, Valid: true},
		EventType:            "INVOICE_FINALIZED",
		EventData:            eventData,
		IpAddress:            pgtype.Text{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:            pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
}

func (s *WebhookService) handleInvoiceUpcoming(ctx context.Context, event *stripe.Event, ipAddress, userAgent string) error {
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

	s.logger.Info("invoice upcoming",
		"invoice_id", invoice.ID,
		"amount_due", invoice.AmountDue,
		"period_end", time.Unix(invoice.PeriodEnd, 0),
	)

	q := queries.New(s.db.Main)

	// Send renewal reminder email and create record for chargeback protection
	if user, err := s.findUserForStripe(ctx, customerID, subscriptionID); err == nil && user != nil {
		emailSvc := email.NewService(s.cfg)

		// Get tier/plan name from subscription
		planName := "Subscription"
		if invoice.Subscription != nil {
			sub, err := q.GetSubscriptionByStripeID(ctx, pgtype.Text{
				String: invoice.Subscription.ID,
				Valid:  true,
			})
			if err == nil {
				if tier, ok := sub.Tier.(string); ok && tier != "" {
					planName = tier
				}
			}
		}

		renewalDate := time.Unix(invoice.PeriodEnd, 0)

		result, err := emailSvc.SendRenewalReminder(email.RenewalReminderParams{
			To:          user.Email,
			FirstName:   s.userFirstName(user),
			RenewalDate: renewalDate,
			Amount:      invoice.AmountDue,
			Currency:    string(invoice.Currency),
			PlanName:    planName,
			ManageURL:   s.cfg.Server.ClientURL + "/settings/subscription",
		})
		if err != nil {
			s.logger.Warn("failed to send renewal reminder email", "error", err)
		}

		// Create renewal notification record for chargeback protection
		delivered := result != nil && result.Success
		deliveredAt := pgtype.Timestamp{}
		if delivered {
			deliveredAt = pgtype.Timestamp{Time: time.Now(), Valid: true}
		}

		emailContent := fmt.Sprintf("Renewal reminder: %s - $%.2f on %s",
			planName, float64(invoice.AmountDue)/100, renewalDate.Format("January 2, 2006"))

		// Convert amount to pgtype.Numeric
		renewalAmount := pgtype.Numeric{}
		_ = renewalAmount.Scan(fmt.Sprintf("%.2f", float64(invoice.AmountDue)/100))

		if _, err := q.CreateRenewalNotification(ctx, queries.CreateRenewalNotificationParams{
			ID:               uuid.New().String(),
			UserId:           user.ID,
			SubscriptionId:   subscriptionID,
			EmailType:        "REMINDER_7_DAY",
			EmailContent:     emailContent,
			RecipientEmail:   user.Email,
			RenewalDate:      pgtype.Timestamp{Time: renewalDate, Valid: true},
			RenewalAmount:    renewalAmount,
			SendgridMessageId: pgtype.Text{
				String: func() string {
					if result != nil {
						return result.MessageID
					}
					return ""
				}(),
				Valid: result != nil && result.MessageID != "",
			},
			Delivered:   delivered,
			DeliveredAt: deliveredAt,
		}); err != nil {
			s.logger.Warn("failed to create renewal notification record", "error", err)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for renewal reminder", "error", err)
	}

	// Record billing audit log
	eventData, _ := json.Marshal(invoice)
	return s.recordAuditLog(ctx, queries.CreateBillingAuditLogParams{
		ID:                   uuid.New().String(),
		StripeCustomerId:     pgtype.Text{String: customerID, Valid: customerID != ""},
		StripeSubscriptionId: pgtype.Text{String: subscriptionID, Valid: subscriptionID != ""},
		StripeInvoiceId:      pgtype.Text{String: invoice.ID, Valid: true},
		EventType:            "INVOICE_UPCOMING",
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

	q := queries.New(s.db.Main)

	// Create/update invoice record
	if err := s.createInvoiceRecord(ctx, &invoice); err != nil {
		s.logger.Error("failed to create invoice record", "error", err)
	}

	// Update invoice as paid
	paidAt := time.Now()
	if invoice.StatusTransitions != nil && invoice.StatusTransitions.PaidAt > 0 {
		paidAt = time.Unix(invoice.StatusTransitions.PaidAt, 0)
	}
	if err := q.UpdateInvoicePaid(ctx, queries.UpdateInvoicePaidParams{
		StripeInvoiceId: invoice.ID,
		PaidAt:          pgtype.Timestamp{Time: paidAt, Valid: true},
		AmountPaid:      int32(invoice.AmountPaid),
	}); err != nil {
		s.logger.Warn("failed to update invoice as paid", "error", err)
	}

	// ADR-067: Create receipt record and send detailed receipt email
	if user, err := s.findUserForStripe(ctx, customerID, subscriptionID); err == nil && user != nil {
		// Get invoice record ID for receipt foreign key
		invoiceRecord, err := q.GetInvoiceByStripeID(ctx, invoice.ID)
		if err != nil {
			s.logger.Warn("could not find invoice record for receipt", "error", err)
		}

		// Extract payment details from charge
		var cardBrand, cardLast4, receiptURL, paymentIntentID, chargeID string
		if invoice.Charge != nil {
			chargeID = invoice.Charge.ID
			receiptURL = invoice.Charge.ReceiptURL
			if invoice.Charge.PaymentMethodDetails != nil && invoice.Charge.PaymentMethodDetails.Card != nil {
				cardBrand = string(invoice.Charge.PaymentMethodDetails.Card.Brand)
				cardLast4 = invoice.Charge.PaymentMethodDetails.Card.Last4
			}
		}
		if invoice.PaymentIntent != nil {
			paymentIntentID = invoice.PaymentIntent.ID
		}

		// Generate receipt number
		receiptNumber := fmt.Sprintf("REC-%s", invoice.ID[len(invoice.ID)-8:])

		// Product description
		description := "Estara AI Subscription"
		if invoice.Lines != nil && len(invoice.Lines.Data) > 0 && invoice.Lines.Data[0].Description != "" {
			description = invoice.Lines.Data[0].Description
		}

		// Create receipt record
		if invoiceRecord.ID != "" {
			if _, err := q.CreateReceipt(ctx, queries.CreateReceiptParams{
				ID:        uuid.New().String(),
				InvoiceId: invoiceRecord.ID,
				UserId:    user.ID,
				StripeChargeId: pgtype.Text{
					String: chargeID,
					Valid:  chargeID != "",
				},
				StripePaymentIntentId: pgtype.Text{
					String: paymentIntentID,
					Valid:  paymentIntentID != "",
				},
				ReceiptNumber: receiptNumber,
				Amount:        int32(invoice.AmountPaid),
				Currency:      string(invoice.Currency),
				PaymentMethod: pgtype.Text{
					String: func() string {
						if cardBrand != "" && cardLast4 != "" {
							return fmt.Sprintf("%s ending in %s", cardBrand, cardLast4)
						}
						return "Card"
					}(),
					Valid: true,
				},
				CardBrand: pgtype.Text{String: cardBrand, Valid: cardBrand != ""},
				CardLast4: pgtype.Text{String: cardLast4, Valid: cardLast4 != ""},
				PaidAt:    pgtype.Timestamp{Time: paidAt, Valid: true},
				StripeReceiptUrl: pgtype.Text{
					String: receiptURL,
					Valid:  receiptURL != "",
				},
				EmailSentAt:        pgtype.Timestamp{Valid: false},
				EmailDelivered:     false,
				ProductDescription: description,
			}); err != nil {
				s.logger.Warn("failed to create receipt record", "error", err)
			}
		}

		// Send detailed receipt email
		emailSvc := email.NewService(s.cfg)
		receiptResult, receiptErr := emailSvc.SendReceiptEmail(email.ReceiptEmailParams{
			To:            user.Email,
			FirstName:     s.userFirstName(user),
			ReceiptNumber: receiptNumber,
			Amount:        invoice.AmountPaid,
			Currency:      string(invoice.Currency),
			CardBrand:     cardBrand,
			CardLast4:     cardLast4,
			PaidAt:        paidAt,
			ReceiptURL:    receiptURL,
			Description:   description,
		})
		if receiptErr != nil {
			s.logger.Warn("failed to send receipt email", "error", receiptErr)
		} else if receiptResult != nil && !receiptResult.Success {
			s.logger.Warn("receipt email not delivered", "error", receiptResult.Error, "email", user.Email)
		}
	} else if err != nil {
		s.logger.Warn("failed to resolve user for receipt email", "error", err)
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
		result, err := emailSvc.SendPaymentFailed(user.Email, s.userFirstName(user), invoice.AmountDue, string(invoice.Currency))
		if err != nil {
			s.logger.Warn("failed to send payment failed email", "error", err)
		} else if result != nil && !result.Success {
			s.logger.Warn("payment failed email not delivered", "error", result.Error, "email", user.Email)
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

	sub, err := q.GetSubscriptionByUserID(ctx, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// Derive tier from user's subscriptionTier (already set by checkout flow)
	tier := string(TierAnnualAccess) // Default
	user, userErr := q.GetUserByID(ctx, userID)
	if userErr == nil && user.SubscriptionTier.Valid && user.SubscriptionTier.String != "" {
		// Map user's subscription tier to ENUM tier
		switch user.SubscriptionTier.String {
		case "professional_allocator", "professional":
			tier = string(TierProfessionalAllocator)
		case "annual_access", "investor":
			tier = string(TierAnnualAccess)
		default:
			tier = strings.ToUpper(user.SubscriptionTier.String)
		}
	}

	// Resolve Stripe price ID from tier
	stripeClient := NewStripeClient(&s.cfg.Stripe)
	priceID := stripeClient.GetPriceIDForTier(SubscriptionTier(tier))

	if errors.Is(err, pgx.ErrNoRows) {
		// Create new subscription
		_, err = q.CreateSubscription(ctx, queries.CreateSubscriptionParams{
			ID:     uuid.New().String(),
			UserId: userID,
			StripeSubscriptionId: pgtype.Text{
				String: stripeSubID,
				Valid:  true,
			},
			StripePriceId: pgtype.Text{
				String: priceID,
				Valid:  priceID != "",
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
				Time:  time.Now().AddDate(1, 0, 0), // 1 year (annual subscriptions)
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
