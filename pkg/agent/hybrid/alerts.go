package hybrid

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ServiceMap struct {
	Services map[string]*ServiceNode `json:"services"`
	Edges    []ServiceEdge           `json:"edges"`
}

type ServiceNode struct {
	Name       string  `json:"name"`
	Requests   int64   `json:"requests"`
	Errors     int64   `json:"errors"`
	LatencyP50 float64 `json:"latency_p50"`
	LatencyP95 float64 `json:"latency_p95"`
	LatencyP99 float64 `json:"latency_p99"`
}

type ServiceEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Calls  int64  `json:"calls"`
	Errors int64  `json:"errors"`
}

type AnomalyAlert struct {
	Service         string    `json:"service"`
	Endpoint        string    `json:"endpoint"`
	LatencyMs       float64   `json:"latency_ms"`
	NormalLatencyMs float64   `json:"normal_latency_ms"`
	TraceID         string    `json:"trace_id"`
	Severity        string    `json:"severity"`
	Timestamp       time.Time `json:"timestamp"`
}

type AlertManager struct {
	slackWebhook string
	opsgenieAPI  string
	jaegerURL    string
}

func NewAlertManager(slackWebhook, opsgenieAPI, jaegerURL string) *AlertManager {
	return &AlertManager{
		slackWebhook: slackWebhook,
		opsgenieAPI:  opsgenieAPI,
		jaegerURL:    jaegerURL,
	}
}

func (a *AlertManager) SendAnomalyAlert(alert AnomalyAlert) error {
	msg := fmt.Sprintf("🚨 *Anomali Tespit Edildi*\n\n*Service:* %s\n*Endpoint:* %s\n*Latency:* %.2fms (normal: %.2fms)\n*Trace:* `%s`\n\n<%s/traces/%s|Jaeger'da Görüntüle>",
		alert.Service,
		alert.Endpoint,
		alert.LatencyMs,
		alert.NormalLatencyMs,
		alert.TraceID,
		a.jaegerURL,
		alert.TraceID,
	)

	if a.slackWebhook != "" {
		a.sendSlack(msg)
	}

	return nil
}

func (a *AlertManager) sendSlack(msg string) {
	payload := map[string]interface{}{
		"text":   msg,
		"mrkdwn": true,
	}

	jsonBytes, _ := json.Marshal(payload)
	http.Post(a.slackWebhook, "application/json", bytes.NewBuffer(jsonBytes))
}

type ServiceMapFetcher struct {
	jaegerURL string
	client    *http.Client
}

func NewServiceMapFetcher(jaegerURL string) *ServiceMapFetcher {
	return &ServiceMapFetcher{
		jaegerURL: jaegerURL,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *ServiceMapFetcher) Fetch() (*ServiceMap, error) {
	url := s.jaegerURL + "/api/services"
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var services []string
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, err
	}

	sm := &ServiceMap{
		Services: make(map[string]*ServiceNode),
		Edges:    []ServiceEdge{},
	}

	for _, svc := range services {
		sm.Services[svc] = &ServiceNode{Name: svc}
	}

	return sm, nil
}
