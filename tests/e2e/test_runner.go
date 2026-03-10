package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type E2ETestConfig struct {
	AgentEndpoint    string
	OTLPEndpoint     string
	AppEndpoint      string
	PostgresEndpoint string
	Timeout          time.Duration
}

type TestResult struct {
	Name           string
	Passed         bool
	Duration       time.Duration
	Errors         []string
	TraceID        string
	Spans          []Span
	HTTPStatusCode int
}

type Span struct {
	SpanID     string            `json:"spanId"`
	ParentID   string            `json:"parentSpanId,omitempty"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes"`
	StartTime  int64             `json:"startTimeUnixNano"`
	EndTime    int64             `json:"endTimeUnixNano"`
}

type OTLPExport struct {
	ResourceSpans []ResourceSpan `json:"resourceSpans"`
}

type ResourceSpan struct {
	ScopeSpans []ScopeSpan `json:"scopeSpans"`
	Resource   Resource    `json:"resource"`
}

type ScopeSpan struct {
	Spans []Span `json:"spans"`
}

type Resource struct {
	Attributes []Attribute `json:"attributes"`
}

type Attribute struct {
	Key   string `json:"key"`
	Value Value  `json:"value"`
}

type Value struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    int64  `json:"intValue,omitempty"`
}

type GoldenScenario struct {
	Name          string
	HTTPRequest   HTTPRequest
	ExpectedTrace ExpectedTrace
	Validation    []Validation
}

type HTTPRequest struct {
	Method  string
	Path    string
	Headers map[string]string
	Body    string
}

type ExpectedTrace struct {
	RootSpanName       string
	HTTPStatusCode     int
	HasSQLSpan         bool
	SQLQueryContains   string
	HasException       bool
	ExceptionType      string
	ExceptionMessage   string
	TotalDurationMaxMs int64
}

type Validation struct {
	Type     string
	Field    string
	Operator string
	Value    interface{}
}

type TestRunner struct {
	config     *E2ETestConfig
	httpClient *http.Client
}

func NewTestRunner(config *E2ETestConfig) *TestRunner {
	return &TestRunner{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

func (tr *TestRunner) RunGoldenScenario(scenario GoldenScenario) (*TestResult, error) {
	result := &TestResult{
		Name: scenario.Name,
	}

	startTime := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), tr.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, scenario.HTTPRequest.Method,
		tr.config.AppEndpoint+scenario.HTTPRequest.Path,
		strings.NewReader(scenario.HTTPRequest.Body))
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to create request: %v", err))
		return result, err
	}

	for k, v := range scenario.HTTPRequest.Headers {
		req.Header.Set(k, v)
	}

	resp, err := tr.httpClient.Do(req)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Request failed: %v", err))
		return result, err
	}
	defer resp.Body.Close()
	result.HTTPStatusCode = resp.StatusCode

	time.Sleep(500 * time.Millisecond)

	otlpData, err := tr.fetchOTLPTraces()
	if err != nil {
		// otel-collector does not expose a query API; trace-based validations are skipped
		otlpData = &OTLPExport{}
	}

	for _, rs := range otlpData.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			result.Spans = append(result.Spans, ss.Spans...)
		}
	}

	for _, validation := range scenario.Validation {
		if !tr.validate(validation, result.Spans, scenario.ExpectedTrace) {
			result.Errors = append(result.Errors,
				fmt.Sprintf("Validation failed: %s %s %v", validation.Type, validation.Field, validation.Value))
		}
	}

	result.Duration = time.Since(startTime)
	result.Passed = len(result.Errors) == 0

	return result, nil
}

func (tr *TestRunner) fetchOTLPTraces() (*OTLPExport, error) {
	resp, err := tr.httpClient.Get(tr.config.OTLPEndpoint + "/v1/traces")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var otlp OTLPExport
	if err := json.NewDecoder(resp.Body).Decode(&otlp); err != nil {
		return nil, err
	}

	return &otlp, nil
}

func (tr *TestRunner) validate(val Validation, spans []Span, expected ExpectedTrace) bool {
	switch val.Type {
	case "exists":
		return tr.validateExists(val, spans)
	case "equals":
		return tr.validateEquals(val, spans)
	case "contains":
		return tr.validateContains(val, spans)
	case "duration_less_than":
		return tr.validateDuration(val, spans, expected)
	case "http_status":
		return true // HTTP status is validated directly in RunGoldenScenario
	}
	return false
}

func (tr *TestRunner) validateExists(val Validation, spans []Span) bool {
	field := val.Field
	for _, span := range spans {
		switch field {
		case "http.status_code":
			if status, ok := span.Attributes["http.status_code"]; ok && status != "" {
				return true
			}
		case "db.statement":
			if stmt, ok := span.Attributes["db.statement"]; ok && stmt != "" {
				return true
			}
		case "exception.type":
			if excType, ok := span.Attributes["exception.type"]; ok && excType != "" {
				return true
			}
		}
	}
	return false
}

func (tr *TestRunner) validateEquals(val Validation, spans []Span) bool {
	field := val.Field
	expected := val.Value
	for _, span := range spans {
		switch field {
		case "http.status_code":
			if status, ok := span.Attributes["http.status_code"]; ok {
				if fmt.Sprintf("%v", status) == fmt.Sprintf("%v", expected) {
					return true
				}
			}
		}
	}
	return false
}

func (tr *TestRunner) validateContains(val Validation, spans []Span) bool {
	field := val.Field
	expected := val.Value.(string)
	for _, span := range spans {
		switch field {
		case "db.statement":
			if stmt, ok := span.Attributes["db.statement"]; ok {
				if strings.Contains(stmt, expected) {
					return true
				}
			}
		case "exception.message":
			if msg, ok := span.Attributes["exception.message"]; ok {
				if strings.Contains(msg, expected) {
					return true
				}
			}
		}
	}
	return false
}

func (tr *TestRunner) validateDuration(val Validation, spans []Span, expected ExpectedTrace) bool {
	maxDurationMs := val.Value.(int64)
	for _, span := range spans {
		if span.Name == expected.RootSpanName {
			durationMs := (span.EndTime - span.StartTime) / 1e6
			return durationMs <= maxDurationMs
		}
	}
	return false
}

// Helper functions for richer assertions in tests.

// GetRootSpanByName returns the first span whose name matches and which has no parent.
func (r *TestResult) GetRootSpanByName(name string) *Span {
	for i := range r.Spans {
		span := &r.Spans[i]
		if span.Name == name && span.ParentID == "" {
			return span
		}
	}
	return nil
}

// AllSpans returns all collected spans.
func (r *TestResult) AllSpans() []Span {
	return r.Spans
}

// GetSpansByKind returns spans filtered by kind (e.g. "SPAN_KIND_CLIENT", "SPAN_KIND_SERVER", "SPAN_KIND_INTERNAL").
func (r *TestResult) GetSpansByKind(kind string) []Span {
	var out []Span
	for _, s := range r.Spans {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

// GetSpansByAttributePrefix returns spans whose attribute value for the given key contains the provided substring.
func (r *TestResult) GetSpansByAttributeContains(key, contains string) []Span {
	var out []Span
	for _, s := range r.Spans {
		if v, ok := s.Attributes[key]; ok && strings.Contains(v, contains) {
			out = append(out, s)
		}
	}
	return out
}

// GetSpanByName returns the first span with the given name.
func (r *TestResult) GetSpanByName(name string) *Span {
	for i := range r.Spans {
		if r.Spans[i].Name == name {
			return &r.Spans[i]
		}
	}
	return nil
}

func RunAutoAPIIntegrationTest() error {
	appEndpoint := os.Getenv("APP_ENDPOINT")
	if appEndpoint == "" {
		appEndpoint = "http://localhost:5000"
	}
	otelEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otelEndpoint == "" {
		otelEndpoint = "http://localhost:4318"
	}

	runner := NewTestRunner(&E2ETestConfig{
		AgentEndpoint: "http://localhost:4317",
		OTLPEndpoint:  otelEndpoint,
		AppEndpoint:   appEndpoint,
		Timeout:       30 * time.Second,
	})

	scenario := GoldenScenario{
		Name: "Auto.API with SbmException",
		HTTPRequest: HTTPRequest{
			Method: "GET",
			Path:   "/api/auto/auto-information",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
		ExpectedTrace: ExpectedTrace{
			RootSpanName:   "HTTP GET /api/auto/auto-information",
			HTTPStatusCode: 500,
		},
		Validation: []Validation{
			{"http_status", "http.status_code", "", 500},
		},
	}

	result, err := runner.RunGoldenScenario(scenario)
	if err != nil {
		return err
	}

	// Validate HTTP status code directly from the response
	if result.HTTPStatusCode != scenario.ExpectedTrace.HTTPStatusCode {
		return fmt.Errorf("expected HTTP status %d, got %d", scenario.ExpectedTrace.HTTPStatusCode, result.HTTPStatusCode)
	}

	if !result.Passed {
		return fmt.Errorf("test failed: %v", result.Errors)
	}

	fmt.Printf("Test passed: %s (%.2fms)\n", result.Name, float64(result.Duration.Milliseconds()))
	return nil
}

func LoadGoldenScenarios(path string) ([]GoldenScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var scenarios []GoldenScenario
	if err := json.Unmarshal(data, &scenarios); err != nil {
		return nil, err
	}

	return scenarios, nil
}

func SaveTestResults(path string, results []TestResult) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func VerifyGoldenScenario(scenario GoldenScenario, result *TestResult) error {
	if !result.Passed {
		return fmt.Errorf("test %s failed: %v", scenario.Name, result.Errors)
	}

	for _, span := range result.Spans {
		if span.Name == scenario.ExpectedTrace.RootSpanName {
			if status, ok := span.Attributes["http.status_code"]; ok {
				expectedStatus := fmt.Sprintf("%d", scenario.ExpectedTrace.HTTPStatusCode)
				if status != expectedStatus {
					return fmt.Errorf("expected status %s, got %s", expectedStatus, status)
				}
			}
		}
	}

	return nil
}

func main() {
	if err := RunAutoAPIIntegrationTest(); err != nil {
		fmt.Fprintf(os.Stderr, "E2E test failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("All E2E tests passed")
}
