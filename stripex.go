package stripex

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v85"
	whapi "github.com/stripe/stripe-go/v85/webhook"
)

const (
	ERR_INVALID_CONNECTED_ACCOUNT_CONFIGURATION = "invalid connected account configuration"
	ERR_INVALID_CONNECTED_ACCOUNTS_LIMIT        = "invalid connected accounts limit"
	ERR_MISSING_ACCOUNT_ID                      = "missing account ID"
	ERR_MISSING_IDEMPOTENCY_KEY                 = "missing idempotency key"
	ERR_MISSING_STRIPE_SECRET_KEY               = "missing Stripe secret key"
	ERR_MISSING_WEBHOOK_SECRET                  = "missing Stripe webhook secret"
	ERR_MISSING_REQUIRED_PARAMS                 = "missing required parameters"
)

var (
	ErrInvalidConnectedAccountConfiguration = errors.New(ERR_INVALID_CONNECTED_ACCOUNT_CONFIGURATION)
	ErrInvalidConnectedAccountsLimit        = errors.New(ERR_INVALID_CONNECTED_ACCOUNTS_LIMIT)
	ErrMissingAccountID                     = errors.New(ERR_MISSING_ACCOUNT_ID)
	ErrMissingIdempotencyKey                = errors.New(ERR_MISSING_IDEMPOTENCY_KEY)
	ErrMissingStripeSecretKey               = errors.New(ERR_MISSING_STRIPE_SECRET_KEY)
	ErrMissingWebhookSecret                 = errors.New(ERR_MISSING_WEBHOOK_SECRET)
	ErrMissingRequiredParams                = errors.New(ERR_MISSING_REQUIRED_PARAMS)
)

type stripeClient struct {
	webhookSecret string
	client        *stripe.Client
}

func NewStripeClient(secretKey, webhookSecret string) (StripeClient, error) {
	if strings.TrimSpace(secretKey) == "" {
		return nil, ErrMissingStripeSecretKey
	}

	return &stripeClient{
		webhookSecret: webhookSecret,
		client:        stripe.NewClient(secretKey),
	}, nil
}

// CreateConnectedAccount creates a new Stripe Connected Account with the provided details.
func (c *stripeClient) CreateConnectedAccount(ctx context.Context, in CreateConnectedAccountInput) (*Account, error) {
	if in.Email == "" || in.Country == "" {
		return nil, ErrMissingRequiredParams
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, ErrMissingIdempotencyKey
	}

	params := &stripe.AccountCreateParams{
		Country: stripe.String(in.Country),
		Email:   stripe.String(in.Email),
	}
	params.SetIdempotencyKey(in.IdempotencyKey)
	if err := applyConnectedAccountConfiguration(params, in.Configuration); err != nil {
		return nil, err
	}

	acct, err := c.client.V1Accounts.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("stripe account create: %w", err)
	}

	return mapStripeAccount(acct), nil
}

// GetConnectedAccount retrieves the current state of a Stripe Connected Account by its ID.
func (c *stripeClient) GetConnectedAccount(ctx context.Context, accountID string) (*Account, error) {
	if accountID == "" {
		return nil, ErrMissingAccountID
	}

	acct, err := c.client.V1Accounts.GetByID(ctx, accountID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe get account: %w", err)
	}
	return mapStripeAccount(acct), nil
}

// ListConnectedAccounts retrieves one page of Stripe Connected Accounts associated with the authenticated account.
func (c *stripeClient) ListConnectedAccounts(
	ctx context.Context,
	in ListConnectedAccountsInput,
) (*ConnectedAccountsPage, error) {
	limit := in.Limit
	if limit == 0 {
		limit = DefaultConnectedAccountsLimit
	}
	if limit < 1 || limit > MaxConnectedAccountsLimit {
		return nil, fmt.Errorf(
			"%w: must be between 1 and %d",
			ErrInvalidConnectedAccountsLimit,
			MaxConnectedAccountsLimit,
		)
	}

	params := &stripe.AccountListParams{}
	params.Limit = stripe.Int64(limit)
	if in.StartingAfter != "" {
		params.StartingAfter = stripe.String(in.StartingAfter)
	}

	list := c.client.V1Accounts.List(ctx, params)
	if err := list.Err(); err != nil {
		return nil, fmt.Errorf("stripe list accounts: %w", err)
	}

	stripeAccounts := list.Data()
	accounts := make([]*Account, 0, len(stripeAccounts))
	for _, account := range stripeAccounts {
		accounts = append(accounts, mapStripeAccount(account))
	}

	hasMore := list.Meta().HasMore
	nextCursor := ""
	if hasMore {
		if len(accounts) == 0 {
			return nil, fmt.Errorf("stripe list accounts: response has_more without accounts")
		}
		nextCursor = accounts[len(accounts)-1].ID
	}

	return &ConnectedAccountsPage{
		Accounts:   accounts,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// GetAccount retrieves the authenticating Stripe Connected Account.
func (c *stripeClient) GetAccount(ctx context.Context) (*Account, error) {
	acct, err := c.client.V1Accounts.Retrieve(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe get account: %w", err)
	}
	return mapStripeAccount(acct), nil
}

// DeleteConnectedAccount deletes an existing Stripe Connected Account by its ID.
func (c *stripeClient) DeleteConnectedAccount(ctx context.Context, accountID string) error {
	if accountID == "" {
		return ErrMissingAccountID
	}

	acc, err := c.client.V1Accounts.Delete(ctx, accountID, nil)
	if err != nil {
		return fmt.Errorf("stripe account delete: %w", err)
	} else if !acc.Deleted {
		return fmt.Errorf("stripe account delete: account %s not deleted", accountID)
	}

	return nil
}

// CreateAccountLink generates an onboarding link for the specified Stripe Connected Account.
func (c *stripeClient) CreateAccountLink(ctx context.Context, in CreateAccountLinkInput) (*AccountLink, error) {
	if in.AccountID == "" || in.RefreshURL == "" || in.ReturnURL == "" {
		return nil, ErrMissingRequiredParams
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, ErrMissingIdempotencyKey
	}

	params := &stripe.AccountLinkCreateParams{
		Account:    stripe.String(in.AccountID),
		RefreshURL: stripe.String(in.RefreshURL),
		ReturnURL:  stripe.String(in.ReturnURL),
		Type:       stripe.String("account_onboarding"),
	}
	params.SetIdempotencyKey(in.IdempotencyKey)

	link, err := c.client.V1AccountLinks.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("stripe account link create: %w", err)
	}

	return &AccountLink{URL: link.URL}, nil
}

// ConstructWebhookEvent validates and constructs a WebhookEvent from the raw payload and signature header.
func (c *stripeClient) ConstructWebhookEvent(payload []byte, sigHeader string) (*WebhookEvent, error) {
	if strings.TrimSpace(c.webhookSecret) == "" {
		return nil, ErrMissingWebhookSecret
	}

	evt, err := whapi.ConstructEvent(payload, sigHeader, c.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("construct webhook event: %w", err)
	}

	return &WebhookEvent{
		ID:        evt.ID,
		AccountID: evt.Account,
		Type:      string(evt.Type),
		Data:      evt.Data.Raw,
	}, nil
}

func applyConnectedAccountConfiguration(
	params *stripe.AccountCreateParams,
	config *ConnectedAccountConfiguration,
) error {
	if config == nil {
		return nil
	}

	controller := &stripe.AccountCreateControllerParams{}
	hasController := false

	if value := config.Controller.FeesPayer; value != "" {
		switch value {
		case ControllerFeesPayerAccount, ControllerFeesPayerApplication:
			controller.Fees = &stripe.AccountCreateControllerFeesParams{
				Payer: stripe.String(string(value)),
			}
			hasController = true
		default:
			return fmt.Errorf("%w: unsupported fees payer %q", ErrInvalidConnectedAccountConfiguration, value)
		}
	}

	if value := config.Controller.LossesPayments; value != "" {
		switch value {
		case ControllerLossesPaymentsApplication, ControllerLossesPaymentsStripe:
			controller.Losses = &stripe.AccountCreateControllerLossesParams{
				Payments: stripe.String(string(value)),
			}
			hasController = true
		default:
			return fmt.Errorf("%w: unsupported losses payer %q", ErrInvalidConnectedAccountConfiguration, value)
		}
	}

	if value := config.Controller.RequirementCollection; value != "" {
		switch value {
		case ControllerRequirementCollectionApplication, ControllerRequirementCollectionStripe:
			controller.RequirementCollection = stripe.String(string(value))
			hasController = true
		default:
			return fmt.Errorf(
				"%w: unsupported requirement collection %q",
				ErrInvalidConnectedAccountConfiguration,
				value,
			)
		}
	}

	if value := config.Controller.DashboardType; value != "" {
		switch value {
		case ControllerDashboardTypeExpress, ControllerDashboardTypeFull, ControllerDashboardTypeNone:
			controller.StripeDashboard = &stripe.AccountCreateControllerStripeDashboardParams{
				Type: stripe.String(string(value)),
			}
			hasController = true
		default:
			return fmt.Errorf("%w: unsupported dashboard type %q", ErrInvalidConnectedAccountConfiguration, value)
		}
	}

	if hasController {
		params.Controller = controller
	}

	requestedCapabilities := make(map[string]struct{}, len(config.RequestedCapabilities))
	for _, capability := range config.RequestedCapabilities {
		name := string(capability)
		if !validCapabilityName(name) {
			return fmt.Errorf("%w: invalid capability %q", ErrInvalidConnectedAccountConfiguration, name)
		}
		if _, exists := requestedCapabilities[name]; exists {
			continue
		}
		requestedCapabilities[name] = struct{}{}
		params.AddExtra(fmt.Sprintf("capabilities[%s][requested]", name), "true")
	}

	return nil
}

func validCapabilityName(name string) bool {
	if name == "" {
		return false
	}

	for index, char := range name {
		if char >= 'a' && char <= 'z' {
			continue
		}
		if index > 0 && (char == '_' || char >= '0' && char <= '9') {
			continue
		}
		return false
	}

	return true
}

// mapStripeAccount converts a Stripe Account object to our internal Account representation.
func mapStripeAccount(acct *stripe.Account) *Account {
	reqCount := 0
	if acct != nil && acct.Requirements != nil {
		reqCount = len(acct.Requirements.CurrentlyDue)
	}

	return &Account{
		ID:                   acct.ID,
		ChargesEnabled:       acct.ChargesEnabled,
		PayoutsEnabled:       acct.PayoutsEnabled,
		RequirementsDueCount: reqCount,
	}
}
