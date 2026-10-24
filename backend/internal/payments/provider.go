// Package payments defines the provider boundary. No provider is enabled yet:
// Telebirr must be implemented strictly from the official, approved documentation.
package payments

import (
	"context"
	"errors"
)

var ErrNotConfigured = errors.New("payment provider not configured")

type InitiateRequest struct {
	OrderID      int64
	AmountSantim int64
	Reference    string // internal payment record reference, created BEFORE calling the provider
}

type InitiateResult struct {
	ProviderRef string
	// ClientPayload is whatever the official integration requires the client to receive.
	ClientPayload map[string]string
}

// Provider is implemented per official payment integration. Confirmation of payment
// must come from provider-verified server-side data, never from client input.
type Provider interface {
	Name() string
	Initiate(ctx context.Context, req InitiateRequest) (InitiateResult, error)
	// VerifyNotification validates a provider callback (signature, amount, merchant) and returns the verified outcome.
	VerifyNotification(ctx context.Context, rawBody []byte, headers map[string]string) (providerRef string, paid bool, err error)
}

// Disabled is the default until Telebirr credentials and approval exist.
type Disabled struct{}

func (Disabled) Name() string { return "telebirr" }
func (Disabled) Initiate(context.Context, InitiateRequest) (InitiateResult, error) {
	return InitiateResult{}, ErrNotConfigured
}
func (Disabled) VerifyNotification(context.Context, []byte, map[string]string) (string, bool, error) {
	return "", false, ErrNotConfigured
}
