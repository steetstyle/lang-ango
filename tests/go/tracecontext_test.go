package integration_test

import (
	"testing"

	otelpkg "github.com/yourorg/lang-ango/pkg/otel"
)

func TestTraceContext_GenerateTraceID(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	tid := tc.GenerateTraceID()

	if len(tid) != 16 {
		t.Errorf("Expected TraceID length 16, got %d", len(tid))
	}

	if tid[0]&0x80 != 0 {
		t.Error("TraceID should have most significant bit zero")
	}
}

func TestTraceContext_GenerateSpanID(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	sid := tc.GenerateSpanID()

	if len(sid) != 8 {
		t.Errorf("Expected SpanID length 8, got %d", len(sid))
	}

	if sid[0]&0x80 != 0 {
		t.Error("SpanID should have most significant bit zero")
	}
}

func TestTraceContext_ExtractTraceparent(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	tests := []struct {
		name       string
		header     string
		wantErr    bool
		traceID    string
		spanID     string
		traceFlags string
	}{
		{
			name:       "valid traceparent",
			header:     "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			wantErr:    false,
			traceID:    "0af7651916cd43dd8448eb211c80319c",
			spanID:     "b7ad6b7169203331",
			traceFlags: "01",
		},
		{
			name:    "missing traceparent",
			header:  "",
			wantErr: true,
		},
		{
			name:    "invalid format - missing parts",
			header:  "00-0af7651916cd43dd8448eb211c80319c",
			wantErr: true,
		},
		{
			name:    "invalid version",
			header:  "01-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{otelpkg.TraceparentHeader: tt.header}
			result, err := tc.ExtractTraceparent(headers)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result.TraceID != tt.traceID {
				t.Errorf("Expected TraceID %s, got %s", tt.traceID, result.TraceID)
			}

			if result.SpanID != tt.spanID {
				t.Errorf("Expected SpanID %s, got %s", tt.spanID, result.SpanID)
			}

			if result.TraceFlags != tt.traceFlags {
				t.Errorf("Expected TraceFlags %s, got %s", tt.traceFlags, result.TraceFlags)
			}
		})
	}
}

func TestTraceContext_GenerateTraceparent(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	traceID, _ := tc.ParseW3CTraceID("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := tc.ParseW3CSpanID("b7ad6b7169203331")

	result := tc.GenerateTraceparent(traceID, spanID, true)

	expected := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestTraceContext_InjectTraceparent(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	traceID, _ := tc.ParseW3CTraceID("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := tc.ParseW3CSpanID("b7ad6b7169203331")

	result := tc.InjectTraceparent(traceID, spanID, true)

	if result[otelpkg.TraceparentHeader] == "" {
		t.Error("traceparent should not be empty")
	}

	if len(result[otelpkg.TraceparentHeader]) != 55 {
		t.Errorf("Expected traceparent length 55, got %d", len(result[otelpkg.TraceparentHeader]))
	}
}

func TestTraceContext_StartNewSpan(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	ctx, span := tc.StartNewSpan("test-span")

	if span == nil {
		t.Fatal("span should not be nil")
	}

	if span.Name != "test-span" {
		t.Errorf("Expected span name 'test-span', got '%s'", span.Name)
	}

	if span.TraceID == [16]byte{} {
		t.Error("TraceID should not be zero")
	}

	if span.SpanID == [8]byte{} {
		t.Error("SpanID should not be zero")
	}

	if ctx == nil {
		t.Error("context should not be nil")
	}
}

func TestTraceContext_StartChildSpan(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	parentCtx, parent := tc.StartNewSpan("parent-span")
	parent.TraceID = [16]byte{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd, 0x84, 0x48, 0xeb, 0x21, 0x1c, 0x80, 0x31, 0x9c}

	ctx, child := tc.StartChildSpan(parentCtx, "child-span", parent)

	if child == nil {
		t.Fatal("child span should not be nil")
	}

	if child.TraceID != parent.TraceID {
		t.Error("child should inherit parent's TraceID")
	}

	if child.ParentID != parent.SpanID {
		t.Error("child should have parent's SpanID as ParentID")
	}

	if child.Name != "child-span" {
		t.Errorf("Expected child name 'child-span', got '%s'", child.Name)
	}

	_ = ctx
}

func TestTraceContext_ParseW3CTraceID(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantLen int
	}{
		{"valid 32 char", "0af7651916cd43dd8448eb211c80319c", false, 16},
		{"valid with dashes", "0af76-5191-6cd4-3dd8-448e-b211-c803-19c", false, 16},
		{"too short", "0af7651916cd43dd8448eb211c803", true, 0},
		{"too long", "0af7651916cd43dd8448eb211c80319c00", true, 0},
		{"invalid hex", "0af7651916cd43dd8448eb211c80319g", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tc.ParseW3CTraceID(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(result) != tt.wantLen {
				t.Errorf("Expected length %d, got %d", tt.wantLen, len(result))
			}
		})
	}
}

func TestTraceContext_ParseW3CSpanID(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantLen int
	}{
		{"valid 16 char", "b7ad6b7169203331", false, 8},
		{"too short", "b7ad6b71692033", true, 0},
		{"too long", "b7ad6b716920333100", true, 0},
		{"invalid hex", "b7ad6b716920333g", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tc.ParseW3CSpanID(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(result) != tt.wantLen {
				t.Errorf("Expected length %d, got %d", tt.wantLen, len(result))
			}
		})
	}
}

func TestTraceContext_IsSampled(t *testing.T) {
	tc := otelpkg.NewTraceContext()

	tests := []struct {
		name     string
		flags    string
		expected bool
	}{
		{"sampled", "01", true},
		{"not sampled", "00", false},
		{"with trace flags", "03", true},
		{"empty", "", false},
		{"invalid", "xx", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tc.IsSampled(tt.flags)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestSpanData_GetTraceIDHex(t *testing.T) {
	sd := &otelpkg.SpanData{
		TraceID: [16]byte{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd, 0x84, 0x48, 0xeb, 0x21, 0x1c, 0x80, 0x31, 0x9c},
	}

	expected := "0af7651916cd43dd8448eb211c80319c"
	result := sd.GetTraceIDHex()

	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestSpanData_GetSpanIDHex(t *testing.T) {
	sd := &otelpkg.SpanData{
		SpanID: [8]byte{0xb7, 0xad, 0x6b, 0x71, 0x69, 0x20, 0x33, 0x31},
	}

	expected := "b7ad6b7169203331"
	result := sd.GetSpanIDHex()

	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestSpanData_SetAttribute(t *testing.T) {
	sd := &otelpkg.SpanData{
		Attributes: make(map[string]string),
	}

	sd.SetAttribute("http.method", "GET")
	sd.SetAttribute("http.url", "/api/test")

	if sd.Attributes["http.method"] != "GET" {
		t.Error("http.method should be GET")
	}

	if sd.Attributes["http.url"] != "/api/test" {
		t.Error("http.url should be /api/test")
	}
}

func TestSpanData_SetStatus(t *testing.T) {
	sd := &otelpkg.SpanData{}

	sd.SetStatus("error")

	if sd.Status != "error" {
		t.Errorf("Expected status 'error', got '%s'", sd.Status)
	}
}

func TestW3CPropagator_Fields(t *testing.T) {
	p := otelpkg.NewW3CPropagator()

	fields := p.Fields()

	if len(fields) != 3 {
		t.Errorf("Expected 3 fields, got %d", len(fields))
	}

	expected := []string{otelpkg.TraceparentHeader, otelpkg.TraceStateHeader, otelpkg.BaggageHeader}
	for i, field := range expected {
		if fields[i] != field {
			t.Errorf("Expected field %d to be %s, got %s", i, field, fields[i])
		}
	}
}

func BenchmarkTraceContext_GenerateTraceID(b *testing.B) {
	tc := otelpkg.NewTraceContext()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.GenerateTraceID()
	}
}

func BenchmarkTraceContext_ExtractTraceparent(b *testing.B) {
	tc := otelpkg.NewTraceContext()
	header := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.ExtractTraceparent(map[string]string{otelpkg.TraceparentHeader: header})
	}
}

func BenchmarkTraceContext_ParseW3CTraceID(b *testing.B) {
	tc := otelpkg.NewTraceContext()
	traceID := "0af7651916cd43dd8448eb211c80319c"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.ParseW3CTraceID(traceID)
	}
}
