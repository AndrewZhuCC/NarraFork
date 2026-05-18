package main

import (
	"testing"

	"narrafork-go/internal/config"
)

func TestTLSListenConfig(t *testing.T) {
	var core config.Core
	core.Server.TLS.Enabled = true
	core.Server.TLS.CertFile = " /tmp/cert.pem "
	core.Server.TLS.KeyFile = " /tmp/key.pem "

	enabled, certFile, keyFile := tlsListenConfig(core)
	if !enabled {
		t.Fatal("expected TLS to be enabled when enabled flag and cert/key files are present")
	}
	if certFile != "/tmp/cert.pem" || keyFile != "/tmp/key.pem" {
		t.Fatalf("expected trimmed cert/key paths, got cert=%q key=%q", certFile, keyFile)
	}
}

func TestTLSListenConfigRequiresEnabledAndFiles(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*config.Core)
	}{
		{
			name: "disabled with files",
			configure: func(core *config.Core) {
				core.Server.TLS.CertFile = "/tmp/cert.pem"
				core.Server.TLS.KeyFile = "/tmp/key.pem"
			},
		},
		{
			name: "missing cert",
			configure: func(core *config.Core) {
				core.Server.TLS.Enabled = true
				core.Server.TLS.KeyFile = "/tmp/key.pem"
			},
		},
		{
			name: "missing key",
			configure: func(core *config.Core) {
				core.Server.TLS.Enabled = true
				core.Server.TLS.CertFile = "/tmp/cert.pem"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var core config.Core
			tc.configure(&core)
			enabled, _, _ := tlsListenConfig(core)
			if enabled {
				t.Fatal("expected TLS to be disabled")
			}
		})
	}
}
