//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// spanTimeout is how long each assertion waits for Jaeger to surface a span.
const spanTimeout = 10 * time.Second

// TestAgentCollectsHTTPSpan fires GET /api/auto/health and verifies the agent
// emitted an "http" span with network.protocol=http.
func TestAgentCollectsHTTPSpan(t *testing.T) {
	checkDeps(t)
	runner := newRunner()

	status, err := runner.DoRequest(http.MethodGet, "/api/auto/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("expected HTTP 200, got %d", status)
	}

	span, err := runner.WaitForSpan(spanTimeout,
		MatchName("http"),
		MatchAttr("network.protocol", "http"),
		MatchAttr("http.method", "GET"),
	)
	if err != nil {
		t.Fatalf("agent did not emit http span: %v", err)
	}
	t.Logf("http span found: attrs=%v", span.Attributes)
}

// TestAgentCollectsDBSpan fires GET /api/auto/health (which runs SELECT 1) and
// verifies the agent emitted a "db" span carrying the SQL statement.
func TestAgentCollectsDBSpan(t *testing.T) {
	checkDeps(t)
	runner := newRunner()

	status, err := runner.DoRequest(http.MethodGet, "/api/auto/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("expected HTTP 200, got %d", status)
	}

	span, err := runner.WaitForSpan(spanTimeout,
		MatchName("db"),
		MatchAttr("db.system", "postgresql"),
		MatchAttr("db.statement", "SELECT 1"),
	)
	if err != nil {
		t.Fatalf("agent did not emit db span with SELECT 1: %v", err)
	}
	t.Logf("db span found: attrs=%v", span.Attributes)
}

// TestAgentCollectsExceptionSpan fires GET /api/auto/auto-information (which
// always throws SbmException) and verifies the agent emitted a "method" span
// with dotnet.event=exception.
func TestAgentCollectsExceptionSpan(t *testing.T) {
	checkDeps(t)
	runner := newRunner()

	status, err := runner.DoRequest(http.MethodGet, "/api/auto/auto-information")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if status != 500 {
		t.Fatalf("expected HTTP 500, got %d", status)
	}

	span, err := runner.WaitForSpan(spanTimeout,
		MatchName("method"),
		MatchAttr("dotnet.event", "exception"),
	)
	if err != nil {
		t.Fatalf("agent did not emit exception span: %v", err)
	}
	t.Logf("exception span found: attrs=%v", span.Attributes)
}

// TestAgentCollectsMultipleDBSpans fires GET /api/auto/auto-information which
// executes 5 SQL queries and verifies the agent captured at least 3 db spans.
func TestAgentCollectsMultipleDBSpans(t *testing.T) {
	checkDeps(t)
	runner := newRunner()

	_, err := runner.DoRequest(http.MethodGet, "/api/auto/auto-information")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Wait until at least 3 db spans are present.
	deadline := time.Now().Add(spanTimeout)
	var dbSpans []Span
	for time.Now().Before(deadline) {
		export, fetchErr := runner.fetchSpans()
		if fetchErr == nil {
			dbSpans = nil
			for _, rs := range export.ResourceSpans {
				for _, ss := range rs.ScopeSpans {
					for _, s := range ss.Spans {
						if s.Name == "db" {
							dbSpans = append(dbSpans, s)
						}
					}
				}
			}
			if len(dbSpans) >= 3 {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if len(dbSpans) < 3 {
		t.Fatalf("expected at least 3 db spans, got %d", len(dbSpans))
	}
	t.Logf("collected %d db span(s):", len(dbSpans))
	for i, s := range dbSpans {
		t.Logf("  [%d] attrs=%v", i, s.Attributes)
	}
}

// TestAgentSimultaneous fires both endpoints at the same time and verifies
// that both an exception span and a healthy http span are collected.
func TestAgentSimultaneous(t *testing.T) {
	checkDeps(t)

	type result struct {
		status int
		err    error
	}

	exCh := make(chan result, 1)
	healthCh := make(chan result, 1)

	runner := newRunner()

	go func() {
		s, err := runner.DoRequest(http.MethodGet, "/api/auto/auto-information")
		exCh <- result{s, err}
	}()
	go func() {
		s, err := runner.DoRequest(http.MethodGet, "/api/auto/health")
		healthCh <- result{s, err}
	}()

	exRes := <-exCh
	healthRes := <-healthCh

	if exRes.err != nil {
		t.Fatalf("auto-information request failed: %v", exRes.err)
	}
	if healthRes.err != nil {
		t.Fatalf("health request failed: %v", healthRes.err)
	}

	if exRes.status != 500 {
		t.Errorf("auto-information: expected 500, got %d", exRes.status)
	}
	if healthRes.status != 200 {
		t.Errorf("health: expected 200, got %d", healthRes.status)
	}

	// Both span types must appear in Jaeger.
	checks := []struct {
		desc     string
		matchers []SpanMatcher
	}{
		{"http span (health)", []SpanMatcher{MatchName("http"), MatchAttr("http.method", "GET")}},
		{"db span (SELECT 1)", []SpanMatcher{MatchName("db"), MatchAttr("db.statement", "SELECT 1")}},
		{"exception span", []SpanMatcher{MatchName("method"), MatchAttr("dotnet.event", "exception")}},
	}

	for _, c := range checks {
		span, err := runner.WaitForSpan(spanTimeout, c.matchers...)
		if err != nil {
			t.Errorf("missing %s: %v", c.desc, err)
			continue
		}
		t.Logf("%s found: attrs=%v", c.desc, span.Attributes)
	}

	if t.Failed() {
		t.Fatal(fmt.Sprintf("simultaneous collection test failed — agent missed spans"))
	}
}
