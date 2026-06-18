package agentbasic

import (
	"errors"
	"testing"
)

func TestGetPaymentRequiredErrorInfo(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if info := getPaymentRequiredErrorInfo(nil); info != nil {
			t.Fatalf("expected nil for nil error, got %+v", info)
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if info := getPaymentRequiredErrorInfo(errors.New("connection reset by peer")); info != nil {
			t.Fatalf("expected nil for unrelated error, got %+v", info)
		}
	})

	t.Run("402 status via providerHTTPError", func(t *testing.T) {
		err := &providerHTTPError{StatusCode: 402, Provider: "nug", Body: `{"error":{"balance":1.5,"required":10}}`}
		info := getPaymentRequiredErrorInfo(err)
		if info == nil {
			t.Fatal("expected payment-required info for 402")
		}
		if info.Balance == nil || *info.Balance != 1.5 {
			t.Fatalf("balance = %v, want 1.5", info.Balance)
		}
		if info.Required == nil || *info.Required != 10 {
			t.Fatalf("required = %v, want 10", info.Required)
		}
	})

	t.Run("insufficient_quota message", func(t *testing.T) {
		err := errors.New(`provider error: {"error":{"message":"insufficient_quota","balance":0}}`)
		info := getPaymentRequiredErrorInfo(err)
		if info == nil {
			t.Fatal("expected payment-required info for insufficient_quota")
		}
		if info.Balance == nil || *info.Balance != 0 {
			t.Fatalf("balance = %v, want 0", info.Balance)
		}
		if info.Required != nil {
			t.Fatalf("required = %v, want nil", info.Required)
		}
	})

	t.Run("payment required text without embedded json", func(t *testing.T) {
		info := getPaymentRequiredErrorInfo(errors.New("Payment Required"))
		if info == nil {
			t.Fatal("expected payment-required info for 'payment required' text")
		}
		if info.Balance != nil || info.Required != nil {
			t.Fatalf("expected nil balance/required, got balance=%v required=%v", info.Balance, info.Required)
		}
		if info.Message != "Payment Required" {
			t.Fatalf("message = %q, want %q", info.Message, "Payment Required")
		}
	})

	t.Run("numeric strings parsed", func(t *testing.T) {
		err := errors.New(`{"error":{"message":"insufficient quota","balance":"2.25","required":"50"}}`)
		info := getPaymentRequiredErrorInfo(err)
		if info == nil {
			t.Fatal("expected payment-required info")
		}
		if info.Balance == nil || *info.Balance != 2.25 {
			t.Fatalf("balance = %v, want 2.25", info.Balance)
		}
		if info.Required == nil || *info.Required != 50 {
			t.Fatalf("required = %v, want 50", info.Required)
		}
	})
}
