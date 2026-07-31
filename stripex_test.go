package stripex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v85"
)

type testContextKey struct{}

type backendCall struct {
	method                          string
	path                            string
	key                             string
	context                         context.Context
	idempotencyKey                  string
	controllerFeesPayer             string
	controllerLossesPayments        string
	controllerRequirementCollection string
	controllerDashboardType         string
	extraValues                     map[string][]string
	limit                           string
	startingAfter                   string
}

type recordingBackend struct {
	mu           sync.Mutex
	calls        []backendCall
	callError    error
	deleteResult *bool
	listAccounts []*stripe.Account
	listHasMore  bool
	listError    error
}

func (b *recordingBackend) Call(
	method, path, key string,
	params stripe.ParamsContainer,
	result stripe.LastResponseSetter,
) error {
	call := backendCall{
		method: method,
		path:   path,
		key:    key,
	}
	if params != nil {
		baseParams := params.GetParams()
		call.context = baseParams.Context
		if baseParams.IdempotencyKey != nil {
			call.idempotencyKey = *baseParams.IdempotencyKey
		}
		if baseParams.Extra != nil {
			call.extraValues = make(map[string][]string, len(baseParams.Extra.Values))
			for name, values := range baseParams.Extra.Values {
				call.extraValues[name] = append([]string(nil), values...)
			}
		}
	}

	if accountParams, ok := params.(*stripe.AccountCreateParams); ok && accountParams.Controller != nil {
		controller := accountParams.Controller
		if controller.Fees != nil && controller.Fees.Payer != nil {
			call.controllerFeesPayer = *controller.Fees.Payer
		}
		if controller.Losses != nil && controller.Losses.Payments != nil {
			call.controllerLossesPayments = *controller.Losses.Payments
		}
		if controller.RequirementCollection != nil {
			call.controllerRequirementCollection = *controller.RequirementCollection
		}
		if controller.StripeDashboard != nil && controller.StripeDashboard.Type != nil {
			call.controllerDashboardType = *controller.StripeDashboard.Type
		}
	}

	b.mu.Lock()
	b.calls = append(b.calls, call)
	callErr := b.callError
	deleteResultSet := b.deleteResult != nil
	deleteResult := false
	if deleteResultSet {
		deleteResult = *b.deleteResult
	}
	b.mu.Unlock()

	if callErr != nil {
		return callErr
	}

	switch out := result.(type) {
	case *stripe.Account:
		out.ID = "acct_test"
		if strings.HasPrefix(path, "/v1/accounts/") {
			out.ID = strings.TrimPrefix(path, "/v1/accounts/")
		}
		out.Deleted = method == http.MethodDelete
		if method == http.MethodDelete && deleteResultSet {
			out.Deleted = deleteResult
		}
	case *stripe.AccountLink:
		out.URL = "https://connect.stripe.com/setup/test"
	default:
		return fmt.Errorf("unsupported Stripe result type %T", result)
	}

	return nil
}

func (b *recordingBackend) CallStreaming(
	_, _, _ string,
	_ stripe.ParamsContainer,
	_ stripe.StreamingLastResponseSetter,
) error {
	return fmt.Errorf("unexpected streaming Stripe request")
}

func (b *recordingBackend) CallRaw(
	method, path, key string,
	body []byte,
	params *stripe.Params,
	result stripe.LastResponseSetter,
) error {
	if path != "/v1/accounts" {
		return fmt.Errorf("unexpected raw Stripe request to %s", path)
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return fmt.Errorf("parse Stripe list request: %w", err)
	}
	call := backendCall{
		method:        method,
		path:          path,
		key:           key,
		limit:         values.Get("limit"),
		startingAfter: values.Get("starting_after"),
	}
	if params != nil {
		call.context = params.Context
	}

	b.mu.Lock()
	b.calls = append(b.calls, call)
	accounts := append([]*stripe.Account(nil), b.listAccounts...)
	hasMore := b.listHasMore
	listErr := b.listError
	b.mu.Unlock()

	if listErr != nil {
		return listErr
	}

	page := reflect.ValueOf(result)
	if page.Kind() != reflect.Pointer || page.IsNil() {
		return fmt.Errorf("unexpected Stripe list result type %T", result)
	}
	page = page.Elem()
	data := page.FieldByName("Data")
	listMeta := page.FieldByName("ListMeta")
	if !data.CanSet() || !listMeta.CanSet() {
		return fmt.Errorf("unexpected Stripe list result type %T", result)
	}
	data.Set(reflect.ValueOf(accounts))
	listMeta.FieldByName("HasMore").SetBool(hasMore)

	return nil
}

func (b *recordingBackend) CallMultipart(
	_, _, _, _ string,
	_ *bytes.Buffer,
	_ *stripe.Params,
	_ stripe.LastResponseSetter,
) error {
	return fmt.Errorf("unexpected multipart Stripe request")
}

func (b *recordingBackend) SetMaxNetworkRetries(_ int64) {}

func (b *recordingBackend) snapshot() []backendCall {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]backendCall(nil), b.calls...)
}

func installRecordingBackend(t *testing.T) *recordingBackend {
	t.Helper()

	original := stripe.GetBackend(stripe.APIBackend)
	backend := &recordingBackend{}
	stripe.SetBackend(stripe.APIBackend, backend)
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, original)
	})

	return backend
}

func TestNewStripeClientRejectsMissingSecret(t *testing.T) {
	client, err := NewStripeClient("   ", "whsec_test")
	require.ErrorIs(t, err, ErrMissingStripeSecretKey)
	require.Nil(t, client)
}

func TestStripeClientsKeepAPIKeysIsolated(t *testing.T) {
	backend := installRecordingBackend(t)

	originalGlobalKey := stripe.Key
	stripe.Key = "sk_test_global_unchanged"
	t.Cleanup(func() {
		stripe.Key = originalGlobalKey
	})

	first, err := NewStripeClient("sk_test_first", "whsec_first")
	require.NoError(t, err)
	second, err := NewStripeClient("sk_test_second", "whsec_second")
	require.NoError(t, err)

	_, err = first.GetConnectedAccount(t.Context(), "acct_first")
	require.NoError(t, err)
	_, err = second.GetConnectedAccount(t.Context(), "acct_second")
	require.NoError(t, err)

	calls := backend.snapshot()
	require.Len(t, calls, 2)
	require.Equal(t, "sk_test_first", calls[0].key)
	require.Equal(t, "sk_test_second", calls[1].key)
	require.Equal(t, "sk_test_global_unchanged", stripe.Key)
}

func TestListConnectedAccountsReturnsOneBoundedPage(t *testing.T) {
	backend := installRecordingBackend(t)
	backend.listAccounts = []*stripe.Account{
		{
			ID:             "acct_first",
			ChargesEnabled: true,
			PayoutsEnabled: true,
			Requirements:   &stripe.AccountRequirements{},
		},
		{
			ID:           "acct_second",
			Requirements: &stripe.AccountRequirements{CurrentlyDue: []string{"external_account"}},
		},
	}
	backend.listHasMore = true

	client, err := NewStripeClient("sk_test_list_accounts", "whsec_list_accounts")
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), testContextKey{}, "list-accounts")

	page, err := client.ListConnectedAccounts(ctx, ListConnectedAccountsInput{
		Limit:         2,
		StartingAfter: "acct_previous_page",
	})
	require.NoError(t, err)
	require.Len(t, page.Accounts, 2)
	require.Equal(t, "acct_first", page.Accounts[0].ID)
	require.True(t, page.Accounts[0].ChargesEnabled)
	require.True(t, page.Accounts[0].PayoutsEnabled)
	require.Equal(t, 1, page.Accounts[1].RequirementsDueCount)
	require.True(t, page.HasMore)
	require.Equal(t, "acct_second", page.NextCursor)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodGet, calls[0].method)
	require.Equal(t, "/v1/accounts", calls[0].path)
	require.Equal(t, "sk_test_list_accounts", calls[0].key)
	require.Equal(t, "2", calls[0].limit)
	require.Equal(t, "acct_previous_page", calls[0].startingAfter)
	require.Equal(t, "list-accounts", calls[0].context.Value(testContextKey{}))
}

func TestListConnectedAccountsUsesDefaultLimit(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_list_default", "whsec_list_default")
	require.NoError(t, err)

	page, err := client.ListConnectedAccounts(t.Context(), ListConnectedAccountsInput{})
	require.NoError(t, err)
	require.NotNil(t, page.Accounts)
	require.Empty(t, page.Accounts)
	require.False(t, page.HasMore)
	require.Empty(t, page.NextCursor)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, "25", calls[0].limit)
	require.Empty(t, calls[0].startingAfter)
}

func TestListConnectedAccountsRejectsInvalidLimit(t *testing.T) {
	for _, limit := range []int64{-1, MaxConnectedAccountsLimit + 1} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			backend := installRecordingBackend(t)
			client, err := NewStripeClient("sk_test_list_invalid", "whsec_list_invalid")
			require.NoError(t, err)

			page, err := client.ListConnectedAccounts(t.Context(), ListConnectedAccountsInput{Limit: limit})
			require.ErrorIs(t, err, ErrInvalidConnectedAccountsLimit)
			require.Nil(t, page)
			require.Empty(t, backend.snapshot())
		})
	}
}

func TestListConnectedAccountsReturnsBackendError(t *testing.T) {
	backend := installRecordingBackend(t)
	backendErr := errors.New("list failed")
	backend.listError = backendErr

	client, err := NewStripeClient("sk_test_list_error", "whsec_list_error")
	require.NoError(t, err)

	page, err := client.ListConnectedAccounts(t.Context(), ListConnectedAccountsInput{})
	require.ErrorIs(t, err, backendErr)
	require.Nil(t, page)
	require.Len(t, backend.snapshot(), 1)
}

func TestListConnectedAccountsRejectsInconsistentPagination(t *testing.T) {
	backend := installRecordingBackend(t)
	backend.listHasMore = true

	client, err := NewStripeClient("sk_test_list_inconsistent", "whsec_list_inconsistent")
	require.NoError(t, err)

	page, err := client.ListConnectedAccounts(t.Context(), ListConnectedAccountsInput{})
	require.EqualError(t, err, "stripe list accounts: response has_more without accounts")
	require.Nil(t, page)
}

func TestCreateConnectedAccountRejectsMissingRequiredParams(t *testing.T) {
	for _, input := range []CreateConnectedAccountInput{
		{Country: "US", IdempotencyKey: "missing-email"},
		{Email: "owner@example.com", IdempotencyKey: "missing-country"},
	} {
		backend := installRecordingBackend(t)
		client, err := NewStripeClient("sk_test_account_required", "whsec_account_required")
		require.NoError(t, err)

		account, err := client.CreateConnectedAccount(t.Context(), input)
		require.ErrorIs(t, err, ErrMissingRequiredParams)
		require.Nil(t, account)
		require.Empty(t, backend.snapshot())
	}
}

func TestCreateConnectedAccountRequiresAndForwardsIdempotencyKey(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_create", "whsec_account_create")
	require.NoError(t, err)

	input := CreateConnectedAccountInput{
		Email:   "owner@example.com",
		Country: "US",
	}
	_, err = client.CreateConnectedAccount(t.Context(), input)
	require.ErrorIs(t, err, ErrMissingIdempotencyKey)
	require.Empty(t, backend.snapshot())

	input.IdempotencyKey = "connected-account-user-123"
	ctx := context.WithValue(t.Context(), testContextKey{}, "create-account")
	account, err := client.CreateConnectedAccount(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "acct_test", account.ID)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodPost, calls[0].method)
	require.Equal(t, "/v1/accounts", calls[0].path)
	require.Equal(t, "sk_test_account_create", calls[0].key)
	require.Equal(t, "connected-account-user-123", calls[0].idempotencyKey)
	require.Equal(t, "create-account", calls[0].context.Value(testContextKey{}))
}

func TestCreateConnectedAccountReturnsBackendError(t *testing.T) {
	backend := installRecordingBackend(t)
	backendErr := errors.New("create failed")
	backend.callError = backendErr

	client, err := NewStripeClient("sk_test_account_create_error", "whsec_account_create_error")
	require.NoError(t, err)

	account, err := client.CreateConnectedAccount(t.Context(), CreateConnectedAccountInput{
		Email:          "owner@example.com",
		Country:        "US",
		IdempotencyKey: "account-create-error",
	})
	require.ErrorIs(t, err, backendErr)
	require.Nil(t, account)
	require.Len(t, backend.snapshot(), 1)
}

func TestCreateConnectedAccountForwardsConfiguration(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_configuration", "whsec_account_configuration")
	require.NoError(t, err)

	account, err := client.CreateConnectedAccount(t.Context(), CreateConnectedAccountInput{
		Email:          "owner@example.com",
		Country:        "US",
		IdempotencyKey: "connected-account-configuration-123",
		Configuration: &ConnectedAccountConfiguration{
			Controller: ConnectedAccountController{
				FeesPayer:             ControllerFeesPayerApplication,
				LossesPayments:        ControllerLossesPaymentsApplication,
				RequirementCollection: ControllerRequirementCollectionStripe,
				DashboardType:         ControllerDashboardTypeExpress,
			},
			RequestedCapabilities: []ConnectedAccountCapability{
				ConnectedAccountCapabilityCardPayments,
				ConnectedAccountCapabilityTransfers,
				ConnectedAccountCapabilityCardPayments,
				"us_bank_account_ach_payments",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "acct_test", account.ID)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, "application", calls[0].controllerFeesPayer)
	require.Equal(t, "application", calls[0].controllerLossesPayments)
	require.Equal(t, "stripe", calls[0].controllerRequirementCollection)
	require.Equal(t, "express", calls[0].controllerDashboardType)
	require.Equal(t, []string{"true"}, calls[0].extraValues["capabilities[card_payments][requested]"])
	require.Equal(t, []string{"true"}, calls[0].extraValues["capabilities[transfers][requested]"])
	require.Equal(t, []string{"true"}, calls[0].extraValues["capabilities[us_bank_account_ach_payments][requested]"])
}

func TestCreateConnectedAccountRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		configuration *ConnectedAccountConfiguration
	}{
		{
			name: "fees payer",
			configuration: &ConnectedAccountConfiguration{
				Controller: ConnectedAccountController{FeesPayer: "invalid"},
			},
		},
		{
			name: "losses payer",
			configuration: &ConnectedAccountConfiguration{
				Controller: ConnectedAccountController{LossesPayments: "invalid"},
			},
		},
		{
			name: "requirement collection",
			configuration: &ConnectedAccountConfiguration{
				Controller: ConnectedAccountController{RequirementCollection: "invalid"},
			},
		},
		{
			name: "dashboard type",
			configuration: &ConnectedAccountConfiguration{
				Controller: ConnectedAccountController{DashboardType: "invalid"},
			},
		},
		{
			name: "capability",
			configuration: &ConnectedAccountConfiguration{
				RequestedCapabilities: []ConnectedAccountCapability{"card_payments][requested]"},
			},
		},
		{
			name: "empty capability",
			configuration: &ConnectedAccountConfiguration{
				RequestedCapabilities: []ConnectedAccountCapability{""},
			},
		},
		{
			name: "capability begins with a digit",
			configuration: &ConnectedAccountConfiguration{
				RequestedCapabilities: []ConnectedAccountCapability{"1_capability"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := installRecordingBackend(t)
			client, err := NewStripeClient("sk_test_invalid_configuration", "whsec_invalid_configuration")
			require.NoError(t, err)

			account, err := client.CreateConnectedAccount(t.Context(), CreateConnectedAccountInput{
				Email:          "owner@example.com",
				Country:        "US",
				IdempotencyKey: "invalid-connected-account-configuration",
				Configuration:  test.configuration,
			})
			require.ErrorIs(t, err, ErrInvalidConnectedAccountConfiguration)
			require.Nil(t, account)
			require.Empty(t, backend.snapshot())
		})
	}
}

func TestGetConnectedAccountRejectsMissingID(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_get_required", "whsec_account_get_required")
	require.NoError(t, err)

	account, err := client.GetConnectedAccount(t.Context(), "")
	require.ErrorIs(t, err, ErrMissingAccountID)
	require.Nil(t, account)
	require.Empty(t, backend.snapshot())
}

func TestGetConnectedAccountReturnsBackendError(t *testing.T) {
	backend := installRecordingBackend(t)
	backendErr := errors.New("get failed")
	backend.callError = backendErr

	client, err := NewStripeClient("sk_test_account_get_error", "whsec_account_get_error")
	require.NoError(t, err)

	account, err := client.GetConnectedAccount(t.Context(), "acct_failed")
	require.ErrorIs(t, err, backendErr)
	require.Nil(t, account)
	require.Len(t, backend.snapshot(), 1)
}

func TestGetAccount(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_get", "whsec_account_get")
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), testContextKey{}, "get-account")

	account, err := client.GetAccount(ctx)
	require.NoError(t, err)
	require.Equal(t, "acct_test", account.ID)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodGet, calls[0].method)
	require.Equal(t, "/v1/account", calls[0].path)
	require.Equal(t, "sk_test_account_get", calls[0].key)
	require.Equal(t, "get-account", calls[0].context.Value(testContextKey{}))
}

func TestGetAccountReturnsBackendError(t *testing.T) {
	backend := installRecordingBackend(t)
	backendErr := errors.New("retrieve failed")
	backend.callError = backendErr

	client, err := NewStripeClient("sk_test_account_get_error", "whsec_account_get_error")
	require.NoError(t, err)

	account, err := client.GetAccount(t.Context())
	require.ErrorIs(t, err, backendErr)
	require.Nil(t, account)
	require.Len(t, backend.snapshot(), 1)
}

func TestDeleteConnectedAccountRejectsMissingID(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_delete_required", "whsec_account_delete_required")
	require.NoError(t, err)

	err = client.DeleteConnectedAccount(t.Context(), "")
	require.ErrorIs(t, err, ErrMissingAccountID)
	require.Empty(t, backend.snapshot())
}

func TestDeleteConnectedAccount(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_delete", "whsec_account_delete")
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), testContextKey{}, "delete-account")

	err = client.DeleteConnectedAccount(ctx, "acct_delete")
	require.NoError(t, err)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodDelete, calls[0].method)
	require.Equal(t, "/v1/accounts/acct_delete", calls[0].path)
	require.Equal(t, "sk_test_account_delete", calls[0].key)
	require.Equal(t, "delete-account", calls[0].context.Value(testContextKey{}))
}

func TestDeleteConnectedAccountReturnsBackendError(t *testing.T) {
	backend := installRecordingBackend(t)
	backendErr := errors.New("delete failed")
	backend.callError = backendErr

	client, err := NewStripeClient("sk_test_account_delete_error", "whsec_account_delete_error")
	require.NoError(t, err)

	err = client.DeleteConnectedAccount(t.Context(), "acct_failed")
	require.ErrorIs(t, err, backendErr)
	require.Len(t, backend.snapshot(), 1)
}

func TestDeleteConnectedAccountRequiresDeletedResponse(t *testing.T) {
	backend := installRecordingBackend(t)
	deleted := false
	backend.deleteResult = &deleted

	client, err := NewStripeClient("sk_test_account_not_deleted", "whsec_account_not_deleted")
	require.NoError(t, err)

	err = client.DeleteConnectedAccount(t.Context(), "acct_not_deleted")
	require.EqualError(t, err, "stripe account delete: account acct_not_deleted not deleted")
}

func TestCreateAccountLinkRejectsMissingRequiredParams(t *testing.T) {
	for _, input := range []CreateAccountLinkInput{
		{
			RefreshURL:     "https://example.com/stripe/refresh",
			ReturnURL:      "https://example.com/stripe/return",
			IdempotencyKey: "missing-account-id",
		},
		{
			AccountID:      "acct_test",
			ReturnURL:      "https://example.com/stripe/return",
			IdempotencyKey: "missing-refresh-url",
		},
		{
			AccountID:      "acct_test",
			RefreshURL:     "https://example.com/stripe/refresh",
			IdempotencyKey: "missing-return-url",
		},
	} {
		backend := installRecordingBackend(t)
		client, err := NewStripeClient("sk_test_account_link_required", "whsec_account_link_required")
		require.NoError(t, err)

		link, err := client.CreateAccountLink(t.Context(), input)
		require.ErrorIs(t, err, ErrMissingRequiredParams)
		require.Nil(t, link)
		require.Empty(t, backend.snapshot())
	}
}

func TestCreateAccountLinkRequiresAndForwardsIdempotencyKey(t *testing.T) {
	backend := installRecordingBackend(t)
	client, err := NewStripeClient("sk_test_account_link", "whsec_account_link")
	require.NoError(t, err)

	input := CreateAccountLinkInput{
		AccountID:  "acct_test",
		RefreshURL: "https://example.com/stripe/refresh",
		ReturnURL:  "https://example.com/stripe/return",
	}
	_, err = client.CreateAccountLink(t.Context(), input)
	require.ErrorIs(t, err, ErrMissingIdempotencyKey)
	require.Empty(t, backend.snapshot())

	input.IdempotencyKey = "account-link-operation-123"
	ctx := context.WithValue(t.Context(), testContextKey{}, "create-account-link")
	link, err := client.CreateAccountLink(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "https://connect.stripe.com/setup/test", link.URL)

	calls := backend.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodPost, calls[0].method)
	require.Equal(t, "/v1/account_links", calls[0].path)
	require.Equal(t, "sk_test_account_link", calls[0].key)
	require.Equal(t, "account-link-operation-123", calls[0].idempotencyKey)
	require.Equal(t, "create-account-link", calls[0].context.Value(testContextKey{}))
}

func TestCreateAccountLinkReturnsBackendError(t *testing.T) {
	backend := installRecordingBackend(t)
	backendErr := errors.New("account link failed")
	backend.callError = backendErr

	client, err := NewStripeClient("sk_test_account_link_error", "whsec_account_link_error")
	require.NoError(t, err)

	link, err := client.CreateAccountLink(t.Context(), CreateAccountLinkInput{
		AccountID:      "acct_test",
		RefreshURL:     "https://example.com/stripe/refresh",
		ReturnURL:      "https://example.com/stripe/return",
		IdempotencyKey: "account-link-error",
	})
	require.ErrorIs(t, err, backendErr)
	require.Nil(t, link)
	require.Len(t, backend.snapshot(), 1)
}

func TestConstructWebhookEventRejectsMissingSecret(t *testing.T) {
	client, err := NewStripeClient("sk_test_webhook", "   ")
	require.NoError(t, err)

	event, err := client.ConstructWebhookEvent([]byte(`{"object":"event"}`), "attacker-controlled")
	require.ErrorIs(t, err, ErrMissingWebhookSecret)
	require.Nil(t, event)
}
