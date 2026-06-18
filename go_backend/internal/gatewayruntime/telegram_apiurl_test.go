package gatewayruntime

import "testing"

// TestTelegramAdapterAPIURL covers the apiURL builder: the default Telegram API
// base is used when none is configured, a custom APIBase overrides it, trailing
// slashes are trimmed, and the /bot<token>/<method> path format is produced.
func TestTelegramAdapterAPIURL(t *testing.T) {
	cases := []struct {
		name    string
		apiBase string
		token   string
		method  string
		want    string
	}{
		{
			name:    "default base",
			apiBase: "",
			token:   "T",
			method:  "getUpdates",
			want:    "https://api.telegram.org/botT/getUpdates",
		},
		{
			name:    "custom base",
			apiBase: "http://localhost:9000",
			token:   "abc",
			method:  "sendMessage",
			want:    "http://localhost:9000/botabc/sendMessage",
		},
		{
			name:    "custom base trailing slash trimmed",
			apiBase: "http://proxy.example/api/",
			token:   "tok",
			method:  "getUpdates",
			want:    "http://proxy.example/api/bottok/getUpdates",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newTelegramAdapter(
				platformConfig{Platform: "telegram", Token: tc.token, APIBase: tc.apiBase},
				nil,
			)
			if got := adapter.apiURL(tc.method); got != tc.want {
				t.Fatalf("apiURL(%q) = %q, want %q", tc.method, got, tc.want)
			}
		})
	}
}
