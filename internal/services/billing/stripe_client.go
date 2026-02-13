package billing

import (
	"fmt"

	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/billingportal/session"
	checkoutSession "github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/subscription"

	"github.com/estara-ai/www/internal/config"
)

// StripeClient wraps Stripe API operations
type StripeClient struct {
	cfg *config.StripeConfig
}

// NewStripeClient creates a new Stripe client
func NewStripeClient(cfg *config.StripeConfig) *StripeClient {
	// Set the global API key
	stripe.Key = cfg.SecretKey

	return &StripeClient{
		cfg: cfg,
	}
}

// CreateCheckoutSession creates a new Stripe checkout session
func (c *StripeClient) CreateCheckoutSession(req *CheckoutSessionRequest, userID string) (*CheckoutSessionResponse, error) {
	// Determine the mode based on product type
	mode := stripe.CheckoutSessionModeSubscription
	if req.ProductType == ProductSingleReport || req.ProductType == ProductReportPack {
		mode = stripe.CheckoutSessionModePayment
	}

	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(mode)),
		SuccessURL: stripe.String(req.SuccessURL),
		CancelURL:  stripe.String(req.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(req.PriceID),
				Quantity: stripe.Int64(1),
			},
		},
	}

	// Add customer email if provided
	if req.CustomerEmail != "" {
		params.CustomerEmail = stripe.String(req.CustomerEmail)
	}

	// Add metadata
	params.Metadata = map[string]string{
		"userId":      userID,
		"productType": string(req.ProductType),
	}
	for k, v := range req.Metadata {
		params.Metadata[k] = v
	}

	// Allow promo codes
	if req.AllowPromoCode {
		params.AllowPromotionCodes = stripe.Bool(true)
	}

	// Create the session
	sess, err := checkoutSession.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkout session: %w", err)
	}

	return &CheckoutSessionResponse{
		SessionID:      sess.ID,
		SessionURL:     sess.URL,
		PublishableKey: c.cfg.PublishableKey,
	}, nil
}

// GetCheckoutSession retrieves a checkout session by ID
func (c *StripeClient) GetCheckoutSession(sessionID string) (*stripe.CheckoutSession, error) {
	params := &stripe.CheckoutSessionParams{}
	params.AddExpand("customer")
	params.AddExpand("subscription")
	params.AddExpand("payment_intent")

	sess, err := checkoutSession.Get(sessionID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get checkout session: %w", err)
	}

	return sess, nil
}

// CreateBillingPortalSession creates a billing portal session for a customer
func (c *StripeClient) CreateBillingPortalSession(customerID, returnURL string) (*PortalSessionResponse, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}

	sess, err := session.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create portal session: %w", err)
	}

	return &PortalSessionResponse{
		URL: sess.URL,
	}, nil
}

// CancelSubscription cancels a Stripe subscription
func (c *StripeClient) CancelSubscription(subscriptionID string, cancelAtPeriodEnd bool) (*stripe.Subscription, error) {
	params := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(cancelAtPeriodEnd),
	}

	sub, err := subscription.Update(subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel subscription: %w", err)
	}

	return sub, nil
}

// ReactivateSubscription removes the cancellation from a subscription
func (c *StripeClient) ReactivateSubscription(subscriptionID string) (*stripe.Subscription, error) {
	params := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(false),
	}

	sub, err := subscription.Update(subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to reactivate subscription: %w", err)
	}

	return sub, nil
}

// GetSubscription retrieves a subscription by ID
func (c *StripeClient) GetSubscription(subscriptionID string) (*stripe.Subscription, error) {
	params := &stripe.SubscriptionParams{}
	params.AddExpand("customer")
	params.AddExpand("latest_invoice")

	sub, err := subscription.Get(subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	return sub, nil
}

// CreateCustomer creates a new Stripe customer
func (c *StripeClient) CreateCustomer(email, name string, metadata map[string]string) (*stripe.Customer, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	}

	if metadata != nil {
		params.Metadata = metadata
	}

	cust, err := customer.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create customer: %w", err)
	}

	return cust, nil
}

// GetCustomer retrieves a customer by ID
func (c *StripeClient) GetCustomer(customerID string) (*stripe.Customer, error) {
	cust, err := customer.Get(customerID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer: %w", err)
	}

	return cust, nil
}

// GetCustomerByEmail finds a customer by email
func (c *StripeClient) GetCustomerByEmail(email string) (*stripe.Customer, error) {
	params := &stripe.CustomerListParams{
		Email: stripe.String(email),
	}
	params.Limit = stripe.Int64(1)

	iter := customer.List(params)
	if iter.Next() {
		return iter.Customer(), nil
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to search customer: %w", err)
	}

	return nil, nil // No customer found
}

// CreateFreeSubscription creates a free tier subscription (no Stripe subscription)
func (c *StripeClient) CreateFreeSubscription(customerID string) (*stripe.Subscription, error) {
	// For free subscriptions, we don't actually create a Stripe subscription
	// Instead, we just track it in our database
	// Return nil to indicate no Stripe subscription was created
	return nil, nil
}

// UpdateSubscriptionTier changes the subscription price/tier
func (c *StripeClient) UpdateSubscriptionTier(subscriptionID, newPriceID string) (*stripe.Subscription, error) {
	// Get current subscription
	sub, err := c.GetSubscription(subscriptionID)
	if err != nil {
		return nil, err
	}

	if len(sub.Items.Data) == 0 {
		return nil, fmt.Errorf("subscription has no items")
	}

	// Update the subscription item with new price
	params := &stripe.SubscriptionParams{
		Items: []*stripe.SubscriptionItemsParams{
			{
				ID:    stripe.String(sub.Items.Data[0].ID),
				Price: stripe.String(newPriceID),
			},
		},
		ProrationBehavior: stripe.String("create_prorations"),
	}

	updatedSub, err := subscription.Update(subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to update subscription tier: %w", err)
	}

	return updatedSub, nil
}

// GetPriceIDForTier returns the Stripe price ID for a subscription tier
func (c *StripeClient) GetPriceIDForTier(tier SubscriptionTier) string {
	switch tier {
	case TierAnnualAccess:
		return c.cfg.PriceAnnualAccess
	case TierProfessionalAllocator:
		return c.cfg.PriceProfessionalAllocator
	case TierAPIInvestor:
		return c.cfg.PriceAPIInvestor
	case TierAPIAllocator:
		return c.cfg.PriceAPIAllocator
	default:
		return ""
	}
}

// GetPriceIDForProduct returns the Stripe price ID for a product type
func (c *StripeClient) GetPriceIDForProduct(productType ProductType) string {
	switch productType {
	case ProductSingleReport:
		return c.cfg.PriceSingleReport
	case ProductReportPack:
		return c.cfg.PriceReportPack
	case ProductOverageReport:
		return c.cfg.PriceOverageReport
	default:
		return ""
	}
}

// GetTierFromPriceID returns the subscription tier for a given price ID
func (c *StripeClient) GetTierFromPriceID(priceID string) SubscriptionTier {
	switch priceID {
	case c.cfg.PriceAnnualAccess:
		return TierAnnualAccess
	case c.cfg.PriceProfessionalAllocator:
		return TierProfessionalAllocator
	case c.cfg.PriceAPIInvestor:
		return TierAPIInvestor
	case c.cfg.PriceAPIAllocator:
		return TierAPIAllocator
	default:
		return TierAnnualAccess // Default to annual access tier
	}
}
