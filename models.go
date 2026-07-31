package stripex

import "context"

type StripeClient interface {
	// CreateConnectedAccount creates a new Stripe Connected Account with the provided details.
	CreateConnectedAccount(ctx context.Context, in CreateConnectedAccountInput) (*Account, error)

	// GetConnectedAccount retrieves the current state of a Stripe Connected Account by its ID.
	GetConnectedAccount(ctx context.Context, accountID string) (*Account, error)

	// DeleteConnectedAccount deletes an existing Stripe Connected Account by its ID.
	DeleteConnectedAccount(ctx context.Context, accountID string) error

	// CreateAccountLink generates an onboarding link for the specified Stripe Connected Account.
	CreateAccountLink(ctx context.Context, in CreateAccountLinkInput) (*AccountLink, error)

	// ConstructWebhookEvent validates and constructs a WebhookEvent from the raw payload and signature header.
	ConstructWebhookEvent(payload []byte, sigHeader string) (*WebhookEvent, error)

	// GetAccount retrieves the authenticating Stripe Connected Account.
	GetAccount(ctx context.Context) (*Account, error)

	// ListConnectedAccounts retrieves a list of all Stripe Connected Accounts associated with the authenticating account.
	ListConnectedAccounts(ctx context.Context) ([]*Account, error)
}

type Account struct {
	ID                   string
	ChargesEnabled       bool
	PayoutsEnabled       bool
	RequirementsDueCount int
}

type CreateConnectedAccountInput struct {
	Email   string
	Country string
}

type CreateAccountLinkInput struct {
	AccountID  string
	RefreshURL string
	ReturnURL  string
}

type AccountLink struct {
	URL string
}

type WebhookEvent struct {
	ID   string
	Type string
	Data []byte
}
