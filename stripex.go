package stripex

import (
	"context"
	"fmt"

	"github.com/stripe/stripe-go/v85"
	acctapi "github.com/stripe/stripe-go/v85/account"
	acctlnkapi "github.com/stripe/stripe-go/v85/accountlink"
	whapi "github.com/stripe/stripe-go/v85/webhook"
)

type stripeClient struct {
	webhookSecret string
}

func NewStripeClient(secretKey, webhookSecret string) StripeClient {
	stripe.Key = secretKey
	return &stripeClient{
		webhookSecret: webhookSecret,
	}
}

// CreateConnectedAccount creates a new Stripe Connected Account with the provided details.
func (c *stripeClient) CreateConnectedAccount(ctx context.Context, in CreateConnectedAccountInput) (*Account, error) {
	params := &stripe.AccountParams{
		Email: stripe.String(in.Email),
	}

	// For Connect account creation. Adjust controller/capability fields later as needed.
	if in.Country != "" {
		params.Country = stripe.String(in.Country)
	}

	acct, err := acctapi.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe account create: %w", err)
	}

	return mapStripeAccount(acct), nil
}

// GetConnectedAccount retrieves the current state of a Stripe Connected Account by its ID.
func (c *stripeClient) GetConnectedAccount(ctx context.Context, accountID string) (*Account, error) {
	params := &stripe.AccountParams{}
	params.Context = ctx

	acct, err := acctapi.GetByID(accountID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe get account: %w", err)
	}
	return mapStripeAccount(acct), nil
}

// ListConnectedAccounts retrieves a list of all Stripe Connected Accounts associated with the authenticated account.
func (c *stripeClient) ListConnectedAccounts(ctx context.Context) ([]*Account, error) {
	params := &stripe.AccountListParams{}
	params.Context = ctx

	var accounts []*Account
	i := acctapi.List(params)
	for i.Next() {
		acct := i.Account()
		accounts = append(accounts, mapStripeAccount(acct))
	}
	if err := i.Err(); err != nil {
		return nil, fmt.Errorf("stripe list accounts: %w", err)
	}

	return accounts, nil
}

// GetAccount retrieves the authenticating Stripe Connected Account.
func (c *stripeClient) GetAccount(ctx context.Context) (*Account, error) {
	acct, err := acctapi.Get()
	if err != nil {
		return nil, fmt.Errorf("stripe get account: %w", err)
	}
	return mapStripeAccount(acct), nil
}

// DeleteConnectedAccount deletes an existing Stripe Connected Account by its ID.
func (c *stripeClient) DeleteConnectedAccount(ctx context.Context, accountID string) error {
	params := &stripe.AccountParams{}
	params.Context = ctx

	acc, err := acctapi.Del(accountID, params)
	if err != nil {
		return fmt.Errorf("stripe account delete: %w", err)
	} else if acc.Deleted != true {
		return fmt.Errorf("stripe account delete: account %s not deleted", accountID)
	}

	return nil
}

// CreateAccountLink generates an onboarding link for the specified Stripe Connected Account.
func (c *stripeClient) CreateAccountLink(ctx context.Context, in CreateAccountLinkInput) (*AccountLink, error) {
	params := &stripe.AccountLinkParams{
		Account:    stripe.String(in.AccountID),
		RefreshURL: stripe.String(in.RefreshURL),
		ReturnURL:  stripe.String(in.ReturnURL),
		Type:       stripe.String("account_onboarding"),
	}

	link, err := acctlnkapi.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe account link create: %w", err)
	}

	return &AccountLink{URL: link.URL}, nil
}

// ConstructWebhookEvent validates and constructs a WebhookEvent from the raw payload and signature header.
func (c *stripeClient) ConstructWebhookEvent(payload []byte, sigHeader string) (*WebhookEvent, error) {
	evt, err := whapi.ConstructEvent(payload, sigHeader, c.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("construct webhook event: %w", err)
	}

	return &WebhookEvent{
		ID:   evt.ID,
		Type: string(evt.Type),
		Data: evt.Data.Raw,
	}, nil
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
