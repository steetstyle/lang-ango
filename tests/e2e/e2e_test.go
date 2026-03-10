//go:build integration

package e2e

import (
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

// checkDeps skips the test if any required service is unreachable.
func checkDeps(t *testing.T) {
	t.Helper()
	appEndpoint := os.Getenv("APP_ENDPOINT")
	if appEndpoint == "" {
		appEndpoint = "localhost:5000"
	}
	otelEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otelEndpoint == "" {
		otelEndpoint = "localhost:4318"
	}
	for _, addr := range []string{appEndpoint, otelEndpoint} {
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
}

// TestExceptionTrace hits the endpoint that always throws SbmException (HTTP 500)
// and verifies the response. This scenario validates that the lang-ango agent
// captures exception-path stack traces.
func TestExceptionTrace(t *testing.T) {
	checkDeps(t)
	if err := RunExceptionScenario(); err != nil {
		t.Fatalf("exception trace scenario failed: %v", err)
	}
}

// TestStackOnlyTrace hits the healthy endpoint (HTTP 200, no exception) and
// verifies the response. This scenario validates that the lang-ango agent
// captures normal call-stack traces without any exception path.
func TestStackOnlyTrace(t *testing.T) {
	checkDeps(t)
	if err := RunStackOnlyScenario(); err != nil {
		t.Fatalf("stack-only trace scenario failed: %v", err)
	}
}

// TestAutoAPIIntegration runs both scenarios for backwards compatibility.
func TestAutoAPIIntegration(t *testing.T) {
	checkDeps(t)
	if err := RunAutoAPIIntegrationTest(); err != nil {
		t.Fatalf("RunAutoAPIIntegrationTest failed: %v", err)
	}
}
