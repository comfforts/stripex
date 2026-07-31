package stripex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v85"
	whapi "github.com/stripe/stripe-go/v85/webhook"

	"github.com/comfforts/stripex"
)

const (
	runStripeIntegrationTestsEnv = "RUN_STRIPE_INTEGRATION_TESTS"
	testStripeSecretKeyEnv       = "TEST_STRIPE_SECRET_KEY"
	testStripeCountryEnv         = "TEST_STRIPE_COUNTRY"
)

func TestStripeClientIntegrationAccountLifecycle(t *testing.T) {
	secretKey := stripeIntegrationSecretKey(t)
	country := strings.ToUpper(strings.TrimSpace(os.Getenv(testStripeCountryEnv)))
	if country == "" {
		country = "US"
	}

	client, err := stripex.NewStripeClient(secretKey, "whsec_not_used_by_account_lifecycle")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	authAcc, err := client.GetAccount(ctx)
	require.NoError(t, err)
	require.NotNil(t, authAcc)
	require.True(t, strings.HasPrefix(authAcc.ID, "acct_"), "unexpected Stripe account ID %q", authAcc.ID)
	require.GreaterOrEqual(t, authAcc.RequirementsDueCount, 0)

	email := fmt.Sprintf("comff-onboarding-stripe-it+%d@example.com", time.Now().UTC().UnixNano())
	created, err := client.CreateConnectedAccount(ctx, stripex.CreateConnectedAccountInput{
		Email:   email,
		Country: country,
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	require.True(t, strings.HasPrefix(created.ID, "acct_"), "unexpected Stripe account ID %q", created.ID)
	require.GreaterOrEqual(t, created.RequirementsDueCount, 0)

	registerStripeAccountCleanup(t, client, created.ID)

	retrieved, err := client.GetConnectedAccount(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	require.Equal(t, created.ID, retrieved.ID)
	require.GreaterOrEqual(t, retrieved.RequirementsDueCount, 0)

	conAccs, err := client.ListConnectedAccounts(ctx)
	require.NoError(t, err)
	require.NotNil(t, conAccs)
	require.GreaterOrEqual(t, len(conAccs), 1)

	link, err := client.CreateAccountLink(ctx, stripex.CreateAccountLinkInput{
		AccountID:  created.ID,
		RefreshURL: "https://example.com/onboarding/stripe/refresh",
		ReturnURL:  "https://example.com/onboarding/stripe/return",
	})
	require.NoError(t, err)
	require.NotNil(t, link)
	require.NotEmpty(t, link.URL)

	onboardingURL, err := url.Parse(link.URL)
	require.NoError(t, err)
	require.Equal(t, "https", onboardingURL.Scheme)
	require.True(
		t,
		onboardingURL.Hostname() == "stripe.com" || strings.HasSuffix(onboardingURL.Hostname(), ".stripe.com"),
		"unexpected Stripe onboarding host %q",
		onboardingURL.Hostname(),
	)
}

func TestStripeClientIntegrationConstructWebhookEvent(t *testing.T) {
	const webhookSecret = "whsec_stripe_client_integration_test"

	payload, err := json.Marshal(map[string]any{
		"id":               "evt_stripe_client_integration",
		"object":           "event",
		"api_version":      stripe.APIVersion,
		"created":          time.Now().UTC().Unix(),
		"livemode":         false,
		"pending_webhooks": 1,
		"type":             "account.updated",
		"data": map[string]any{
			"object": map[string]any{
				"id":              "acct_stripe_client_integration",
				"object":          "account",
				"charges_enabled": false,
				"payouts_enabled": false,
				"requirements": map[string]any{
					"currently_due": []string{"external_account"},
				},
			},
		},
	})
	require.NoError(t, err)

	signed := whapi.GenerateTestSignedPayload(&whapi.UnsignedPayload{
		Payload: payload,
		Secret:  webhookSecret,
	})
	client, err := stripex.NewStripeClient("sk_test_not_used_by_webhook_test", webhookSecret)
	require.NoError(t, err)

	event, err := client.ConstructWebhookEvent(payload, signed.Header)
	require.NoError(t, err)
	require.NotNil(t, event)
	require.Equal(t, "evt_stripe_client_integration", event.ID)
	require.Equal(t, "account.updated", event.Type)
	require.JSONEq(t, `{
		"id": "acct_stripe_client_integration",
		"object": "account",
		"charges_enabled": false,
		"payouts_enabled": false,
		"requirements": {
			"currently_due": ["external_account"]
		}
	}`, string(event.Data))

	invalidSignature := whapi.GenerateTestSignedPayload(&whapi.UnsignedPayload{
		Payload: payload,
		Secret:  "whsec_wrong",
	})
	event, err = client.ConstructWebhookEvent(payload, invalidSignature.Header)
	require.Error(t, err)
	require.Nil(t, event)
	require.Contains(t, err.Error(), "construct webhook event")
}

func stripeIntegrationSecretKey(t *testing.T) string {
	t.Helper()

	if !envEnabled(os.Getenv(runStripeIntegrationTestsEnv)) {
		t.Skipf(
			"Stripe sandbox integration test disabled; set %s=1 and %s to a Stripe test key",
			runStripeIntegrationTestsEnv,
			testStripeSecretKeyEnv,
		)
	}

	secretKey := strings.TrimSpace(os.Getenv(testStripeSecretKeyEnv))
	if secretKey == "" {
		t.Fatalf("%s is required when %s is enabled", testStripeSecretKeyEnv, runStripeIntegrationTestsEnv)
	}
	if !strings.HasPrefix(secretKey, "sk_test_") && !strings.HasPrefix(secretKey, "rk_test_") {
		t.Fatalf("%s must be a Stripe test or sandbox key; live keys are not accepted", testStripeSecretKeyEnv)
	}
	if len(secretKey) < 20 {
		t.Fatalf("%s looks like a placeholder rather than a usable Stripe test key", testStripeSecretKeyEnv)
	}

	return secretKey
}

func registerStripeAccountCleanup(t *testing.T, cl stripex.StripeClient, accountID string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := cl.DeleteConnectedAccount(ctx, accountID); err != nil {
			t.Errorf("delete Stripe test account %s: %v", accountID, err)
		} else {
			t.Logf("deleted Stripe test account %s", accountID)
		}

		conAccs, err := cl.ListConnectedAccounts(ctx)
		if err != nil {
			t.Errorf("list Stripe connected accounts after cleanup: %v", err)
		} else {
			if len(conAccs) > 0 {
				t.Logf("Stripe connected test accounts exist after cleanup")
				// for _, acc := range conAccs {
				// 	if err := cl.DeleteConnectedAccount(ctx, acc.ID); err != nil {
				// 		t.Errorf("delete Stripe test account %s: %v", acc.ID, err)
				// 	} else {
				// 		t.Logf("deleted Stripe test account %s", acc.ID)
				// 	}
				// }
			}
		}
	})
}

func envEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
