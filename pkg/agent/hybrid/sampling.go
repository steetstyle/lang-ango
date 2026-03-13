package hybrid

import (
	"fmt"
	"sync"
	"time"
)

type MovingAverage struct {
	values []float64
	index  int
	size   int
	count  int
	mu     sync.Mutex
}

func NewMovingAverage(size int) *MovingAverage {
	return &MovingAverage{
		values: make([]float64, size),
		size:   size,
	}
}

func (m *MovingAverage) Add(value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[m.index] = value
	m.index = (m.index + 1) % m.size
	if m.count < m.size {
		m.count++
	}
}

func (m *MovingAverage) Get() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.count == 0 {
		return 0
	}

	sum := 0.0
	for i := 0; i < m.count; i++ {
		sum += m.values[i]
	}
	return sum / float64(m.count)
}

func (m *MovingAverage) GetPercentile(p float64) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.count == 0 {
		return 0
	}

	// Copy and sort
	sorted := make([]float64, m.count)
	copy(sorted, m.values[:m.count])
	for i := 0; i < m.count-1; i++ {
		for j := i + 1; j < m.count; j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	idx := int(float64(m.count) * p)
	if idx >= m.count {
		idx = m.count - 1
	}
	return sorted[idx]
}

type SamplingController struct {
	mu                  sync.RWMutex
	latencyMap          map[string]*MovingAverage
	thresholdMultiplier float64
	sampleWindows       int
	samplingEnabled     map[string]bool
	lastAnomalyTime     time.Time
	cooldownDuration    time.Duration
	ipcServer           *IPCServer
}

func NewSamplingController(ipcServer *IPCServer) *SamplingController {
	return &SamplingController{
		latencyMap:          make(map[string]*MovingAverage),
		thresholdMultiplier: 2.0,
		sampleWindows:       60,
		samplingEnabled:     make(map[string]bool),
		cooldownDuration:    5 * time.Minute,
		ipcServer:           ipcServer,
	}
}

func (s *SamplingController) GetOrCreateMA(operationName string) *MovingAverage {
	s.mu.Lock()
	defer s.mu.Unlock()

	ma, ok := s.latencyMap[operationName]
	if !ok {
		ma = NewMovingAverage(s.sampleWindows)
		s.latencyMap[operationName] = ma
	}
	return ma
}

func (s *SamplingController) OnSpanReceived(operationName string, durationMs float64, traceID string) {
	ma := s.GetOrCreateMA(operationName)
	ma.Add(durationMs)

	avg := ma.Get()
	if avg == 0 {
		return
	}

	p95 := ma.GetPercentile(0.95)
	threshold := p95 * s.thresholdMultiplier

	// Check if this span is anomalous
	if durationMs > threshold && durationMs > 50 {
		s.triggerDeepSampling(operationName, durationMs, avg, traceID)
	}
}

func (s *SamplingController) triggerDeepSampling(operationName string, durationMs float64, avg float64, traceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check cooldown
	if time.Since(s.lastAnomalyTime) < s.cooldownDuration {
		return
	}

	// Check if already sampling this endpoint
	if s.samplingEnabled[operationName] {
		return
	}

	s.lastAnomalyTime = time.Now()
	s.samplingEnabled[operationName] = true

	fmt.Printf("[SMART] 🚨 Anomali tespit edildi: %s\n", operationName)
	fmt.Printf("[SMART]   Süre: %.2fms, Ortalama: %.2fms, Eşik: %.2fms\n",
		durationMs, avg, avg*s.thresholdMultiplier)
	fmt.Printf("[SMART]   → Derin analiz başlatılıyor (traceID: %s)\n", traceID[:min(16, len(traceID))])

	// Send command to C++ Bridge to enable deep sampling
	if s.ipcServer != nil {
		// Set filter to this endpoint
		payload := make([]byte, 260)
		thresholdMs := uint32(100) // Capture anything > 100ms
		payload[0] = byte(thresholdMs)
		payload[1] = byte(thresholdMs >> 8)
		payload[2] = byte(thresholdMs >> 16)
		payload[3] = byte(thresholdMs >> 24)
		payload[4] = 1 // capture_stack = true
		copy(payload[5:], operationName)

		s.ipcServer.SendCommand(IPCTypeCmdSetFilter, payload)
	}
}

func (s *SamplingController) CheckCooldown(operationName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	enabled, ok := s.samplingEnabled[operationName]
	if !ok {
		return false
	}

	if enabled && time.Since(s.lastAnomalyTime) > s.cooldownDuration {
		// Cool down - disable sampling
		s.mu.RUnlock()
		s.mu.Lock()
		s.samplingEnabled[operationName] = false
		s.mu.Unlock()
		fmt.Printf("[SMART] ✅ %s normale döndü, derin analiz kapatıldı\n", operationName)
		return true
	}

	return enabled
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
