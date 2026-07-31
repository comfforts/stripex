# stripex

`stripex` is a small Go client for managing Stripe Connect accounts. It wraps
the official [`stripe-go`](https://github.com/stripe/stripe-go) SDK with a
focused interface for:

- Creating, retrieving, listing, and deleting connected accounts
- Creating Stripe-hosted Connect onboarding links
- Retrieving the authenticating Stripe account
- Verifying and decoding Stripe webhook events

Each `StripeClient` owns its Stripe API credentials. Clients can safely use
different API keys in the same process without modifying Stripe's global
configuration.

## Requirements

- Go 1.25.9 or later
- A Stripe platform secret key
- A Stripe webhook signing secret if webhook verification is used

## Installation

```bash
go get github.com/comfforts/stripex
```

## Creating a client

```go
package main

import (
	"log"
	"os"

	"github.com/comfforts/stripex"
)

func main() {
	client, err := stripex.NewStripeClient(
		os.Getenv("STRIPE_SECRET_KEY"),
		os.Getenv("STRIPE_WEBHOOK_SECRET"),
	)
	if err != nil {
		log.Fatal(err)
	}

	_ = client
}
```

The Stripe secret key is required. The webhook secret can be empty when the
application does not process webhooks; calling `ConstructWebhookEvent` without
a configured webhook secret returns `stripex.ErrMissingWebhookSecret`.

## Managing connected accounts

Creating an account requires a caller-generated idempotency key:

```go
account, err := client.CreateConnectedAccount(ctx, stripex.CreateConnectedAccountInput{
	Email:          "owner@example.com",
	Country:        "US",
	IdempotencyKey: "create-connected-account-7f2cf475-9d10-4c83-a9ec-6a4f42059a2e",
})
if err != nil {
	return err
}
```

Reuse the same idempotency key when retrying the same logical operation. Use a
new key for a different account creation. Avoid including email addresses or
other sensitive data in the key.

Retrieve or delete connected accounts:

```go
account, err := client.GetConnectedAccount(ctx, "acct_...")

err := client.DeleteConnectedAccount(ctx, "acct_...")
```

All network methods accept a `context.Context`; cancellation and deadlines are
passed to the Stripe SDK.

### Listing connected accounts

`ListConnectedAccounts` fetches exactly one bounded Stripe page. A zero limit
uses `DefaultConnectedAccountsLimit` (25); the maximum accepted limit is
`MaxConnectedAccountsLimit` (100).

Use `NextCursor` as `StartingAfter` to fetch the next page:

```go
cursor := ""

for {
	page, err := client.ListConnectedAccounts(ctx, stripex.ListConnectedAccountsInput{
		Limit:         100,
		StartingAfter: cursor,
	})
	if err != nil {
		return err
	}

	for _, account := range page.Accounts {
		// Process this bounded page.
	}

	if !page.HasMore {
		break
	}
	cursor = page.NextCursor
}
```

Invalid limits return `stripex.ErrInvalidConnectedAccountsLimit` before a
Stripe request is made. The method never automatically retrieves subsequent
pages.

### Controller and capabilities

Omit `Configuration` to use Stripe's account-controller and capability
defaults. To create an Express-style connected account and request common
capabilities:

```go
account, err := client.CreateConnectedAccount(ctx, stripex.CreateConnectedAccountInput{
	Email:          "owner@example.com",
	Country:        "US",
	IdempotencyKey: "create-connected-account-7f2cf475-9d10-4c83-a9ec-6a4f42059a2e",
	Configuration: &stripex.ConnectedAccountConfiguration{
		Controller: stripex.ConnectedAccountController{
			FeesPayer:             stripex.ControllerFeesPayerApplication,
			LossesPayments:        stripex.ControllerLossesPaymentsApplication,
			RequirementCollection: stripex.ControllerRequirementCollectionStripe,
			DashboardType:         stripex.ControllerDashboardTypeExpress,
		},
		RequestedCapabilities: []stripex.ConnectedAccountCapability{
			stripex.ConnectedAccountCapabilityCardPayments,
			stripex.ConnectedAccountCapabilityTransfers,
		},
	},
})
```

Controller fields are independently optional. Unsupported controller values or
malformed capability names return
`stripex.ErrInvalidConnectedAccountConfiguration` before contacting Stripe.
`ConnectedAccountCapability` is a string type, allowing additional
Stripe-supported capability names without waiting for a new `stripex` release;
Stripe still validates whether each capability is available for the account's
country and controller configuration.

## Creating an onboarding link

```go
link, err := client.CreateAccountLink(ctx, stripex.CreateAccountLinkInput{
	AccountID:      account.ID,
	RefreshURL:     "https://example.com/stripe/onboarding/refresh",
	ReturnURL:      "https://example.com/stripe/onboarding/complete",
	IdempotencyKey: "create-account-link-c7b9332b-99ea-4fcb-a995-92a5181f5b74",
})
if err != nil {
	return err
}

// Redirect the account holder to link.URL.
```

Account links are single-use. The refresh URL should create a replacement link
before redirecting the account holder back to Stripe. Reuse the same
idempotency key when retrying one link-creation operation, and generate a new
key when intentionally creating a replacement link.

## Verifying webhooks

Pass the unmodified request body and the `Stripe-Signature` header:

```go
payload, err := io.ReadAll(r.Body)
if err != nil {
	return err
}

event, err := client.ConstructWebhookEvent(
	payload,
	r.Header.Get("Stripe-Signature"),
)
if err != nil {
	return err
}

switch event.Type {
case "account.updated":
	// event.AccountID identifies the connected account that sent the event.
	// event.Data contains the raw JSON for the event's data.object.
}
```

Webhook verification requires the raw body. Do not decode and re-encode the
request before calling `ConstructWebhookEvent`. For Connect webhooks,
`event.AccountID` contains Stripe's top-level connected account identifier.

## Errors

Input validation errors are exported sentinel errors and can be checked with
`errors.Is`:

```go
if errors.Is(err, stripex.ErrMissingIdempotencyKey) {
	// Generate or recover the key for this logical account-creation operation.
}
```

The exported validation errors are:

- `ErrInvalidConnectedAccountConfiguration`
- `ErrInvalidConnectedAccountsLimit`
- `ErrMissingAccountID`
- `ErrMissingIdempotencyKey`
- `ErrMissingRequiredParams`
- `ErrMissingStripeSecretKey`
- `ErrMissingWebhookSecret`

Stripe API errors are wrapped with operation-specific context while preserving
the original error for `errors.Is` and `errors.As`.

## Tests

Run the unit tests, local webhook test, race detector, and static checks:

```bash
go test ./...
go test -race ./...
go vet ./...
```

### Unit-test backend

`stripex_test.go` uses `backendCall` and `recordingBackend` as a lightweight
test double for Stripe's `stripe.Backend` interface. This keeps unit tests
deterministic and allows them to verify the adapter-to-SDK boundary without
network access or Stripe credentials.

`backendCall` is a snapshot of one outgoing request. Depending on the endpoint,
it records:

- HTTP method, Stripe path, and API key
- Request context and idempotency key
- Connected-account controller settings and capability parameters
- List limit and `starting_after` cursor

`recordingBackend` records those snapshots and supplies controlled Stripe
responses. Its regular `Call` method returns fake accounts or account links.
Its `CallRaw` method simulates a connected-account page, including account
data, `has_more` metadata, request errors, limits, and cursors. Reflection is
used for list responses because Stripe's concrete V1 page type is unexported.
Unexpected streaming or multipart requests fail immediately.

The test flow is:

```text
install recording backend
        ↓
construct StripeClient
        ↓
call a stripex method
        ↓
record request and supply a fake Stripe response
        ↓
assert the returned value and captured backendCall
```

`installRecordingBackend` restores Stripe's original backend with `t.Cleanup`.
Recorded calls are protected by a mutex and exposed through a defensive
snapshot. Because installation temporarily changes Stripe's global backend,
tests using it must not call `t.Parallel`.

### Stripe sandbox integration tests

The account lifecycle integration test creates a connected account in a Stripe
sandbox, retrieves and lists it, creates an onboarding link without opening it,
and deletes the account during cleanup.

It is opt-in and rejects live credentials:

```bash
RUN_STRIPE_INTEGRATION_TESTS=1 \
TEST_STRIPE_SECRET_KEY='sk_test_...' \
go test . -run '^TestStripeClientIntegration' -count=1 -v
```

`TEST_STRIPE_COUNTRY` can optionally set the connected account's country and
defaults to `US`.

## License

Proprietary and confidential. Copyright © 2026 Comfforts. See
[LICENSE](LICENSE).

## dev notes

```bash
golangci-lint run

go mod tidy -diff

git diff --check

go tool cover -func=/tmp/stripex-cover.out

GOCACHE=/tmp/stripex-go-cache go test ./... -count=1 -coverprofile=/tmp/stripex-current-cover.out && GOCACHE=/tmp/stripex-go-cache go tool cover -func=/tmp/stripex-current-cover.out

go test ./... -count=1

go test -race ./... -count=1
```