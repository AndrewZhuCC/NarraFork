package systemroutes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"narrafork-go/internal/httpx"
)

// TestNugBillingCreateOrderValidatesAmountAndProvider locks the billing-orders
// create input validation against the TS route (server/routes/nug.ts): amount
// must coerce to a finite number > 0, and provider is required. These run BEFORE
// the upstream call, so invalid input must 400 even without a configured provider.
func TestNugBillingCreateOrderValidatesAmountAndProvider(t *testing.T) {
	h := &Handler{}
	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/nug/providers/p1/billing/orders", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := h.nugBillingCreateOrder(w, req); err != nil {
			httpx.WriteError(w, err)
		}
		return w
	}

	cases := []struct{ name, body, want string }{
		{"missing amount", `{"provider":"alipay"}`, "amount must be greater than 0"},
		{"zero amount", `{"amount":0,"provider":"alipay"}`, "amount must be greater than 0"},
		{"negative amount", `{"amount":-5,"provider":"alipay"}`, "amount must be greater than 0"},
		{"non-numeric amount", `{"amount":"abc","provider":"alipay"}`, "amount must be greater than 0"},
		{"missing provider", `{"amount":10}`, "provider is required"},
		{"blank provider", `{"amount":10,"provider":"  "}`, "provider is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := post(t, tc.body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("expected 400 %q, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
