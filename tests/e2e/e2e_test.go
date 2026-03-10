//go:build integration

package e2e

import (
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestAutoAPIIntegration runs the golden-path E2E scenario against a running
// Auto.API instance and OTLP endpoint. If bağımlılıklar (uygulama / OTLP)
// ayakta değilse, testi başarısız etmek yerine atlıyoruz.
func TestAutoAPIIntegration(t *testing.T) {
	appEndpoint := os.Getenv("APP_ENDPOINT")
	if appEndpoint == "" {
		appEndpoint = "localhost:5000"
	}
	otelEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otelEndpoint == "" {
		otelEndpoint = "localhost:4318"
	}

	deps := []string{appEndpoint, otelEndpoint}

	for _, addr := range deps {
		tcpAddr := addr
		if u, err := url.Parse(addr); err == nil && u.Host != "" {
			tcpAddr = u.Host
		}
		conn, err := net.DialTimeout("tcp", tcpAddr, 2*time.Second)
		if err != nil {
			t.Skipf("Skipping E2E test; dependency %s not reachable: %v", addr, err)
		}
		_ = conn.Close()
	}

	if err := RunAutoAPIIntegrationTest(); err != nil {
		t.Fatalf("RunAutoAPIIntegrationTest failed: %v", err)
	}
}

