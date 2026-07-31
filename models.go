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

	// ListConnectedAccounts retrieves one page of Stripe Connected Accounts associated with the authenticating account.
	ListConnectedAccounts(ctx context.Context, in ListConnectedAccountsInput) (*ConnectedAccountsPage, error)
}

type Account struct {
	ID                   string
	ChargesEnabled       bool
	PayoutsEnabled       bool
	RequirementsDueCount int
}

const (
	DefaultConnectedAccountsLimit int64 = 25
	MaxConnectedAccountsLimit     int64 = 100
)

// ListConnectedAccountsInput configures forward cursor pagination.
// A zero Limit uses DefaultConnectedAccountsLimit.
type ListConnectedAccountsInput struct {
	Limit         int64
	StartingAfter string
}

// ConnectedAccountsPage is one bounded page of connected accounts.
type ConnectedAccountsPage struct {
	Accounts   []*Account
	HasMore    bool
	NextCursor string
}

// ControllerFeesPayer identifies who pays Stripe fees for the connected account.
type ControllerFeesPayer string

const (
	ControllerFeesPayerAccount     ControllerFeesPayer = "account"
	ControllerFeesPayerApplication ControllerFeesPayer = "application"
)

// ControllerLossesPayments identifies who is liable for negative payment balances.
type ControllerLossesPayments string

const (
	ControllerLossesPaymentsApplication ControllerLossesPayments = "application"
	ControllerLossesPaymentsStripe      ControllerLossesPayments = "stripe"
)

// ControllerRequirementCollection identifies who collects account requirements.
type ControllerRequirementCollection string

const (
	ControllerRequirementCollectionApplication ControllerRequirementCollection = "application"
	ControllerRequirementCollectionStripe      ControllerRequirementCollection = "stripe"
)

// ControllerDashboardType identifies the Stripe-hosted dashboard available to the account.
type ControllerDashboardType string

const (
	ControllerDashboardTypeExpress ControllerDashboardType = "express"
	ControllerDashboardTypeFull    ControllerDashboardType = "full"
	ControllerDashboardTypeNone    ControllerDashboardType = "none"
)

// ConnectedAccountCapability identifies a Stripe capability to request.
type ConnectedAccountCapability string

const (
	ConnectedAccountCapabilityCardPayments ConnectedAccountCapability = "card_payments"
	ConnectedAccountCapabilityTransfers    ConnectedAccountCapability = "transfers"
)

// ConnectedAccountController configures control of the connected account.
// Empty fields retain Stripe's defaults.
type ConnectedAccountController struct {
	FeesPayer             ControllerFeesPayer
	LossesPayments        ControllerLossesPayments
	RequirementCollection ControllerRequirementCollection
	DashboardType         ControllerDashboardType
}

// ConnectedAccountConfiguration configures the account controller and requested capabilities.
// A nil configuration retains Stripe's defaults.
type ConnectedAccountConfiguration struct {
	Controller            ConnectedAccountController
	RequestedCapabilities []ConnectedAccountCapability
}

type CreateConnectedAccountInput struct {
	Email          string
	Country        string
	IdempotencyKey string
	Configuration  *ConnectedAccountConfiguration
}

type CreateAccountLinkInput struct {
	AccountID      string
	RefreshURL     string
	ReturnURL      string
	IdempotencyKey string
}

type AccountLink struct {
	URL string
}

type WebhookEvent struct {
	ID        string
	AccountID string
	Type      string
	Data      []byte
}
