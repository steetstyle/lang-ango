package hybrid

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/yourorg/lang-ango/pkg/agent/config"
	"github.com/yourorg/lang-ango/pkg/otel"
)

// ============================================================================
// IPC Protocol Constants
// ============================================================================

const (
	IPCMagic   = 0x4C414E47 // "LANG"
	IPCVersion = 1
	IPCMaxSize = 65536
)

// IPC Message Types
const (
	IPCTypeSpanWithStack uint8 = 1
	IPCTypeTraceContext  uint8 = 2
	IPCTypeThreadSample  uint8 = 3
	IPCTypeException     uint8 = 4
	IPCTypeHeartbeat     uint8 = 5
	IPCTypeSymbolUpdate  uint8 = 6 // New: address -> name mapping
	IPCTypeCmdSetFilter  uint8 = 10
	IPCTypeCmdStartStack uint8 = 11
	IPCTypeCmdStopStack  uint8 = 12
)

// ============================================================================
// IPC Data Structures
// ============================================================================

// SpanWithStackData matches C++ struct
type SpanWithStackData struct {
	TraceID         [32]byte
	SpanID          [16]byte
	ParentSpanID    [16]byte
	OperationName   [256]byte
	StartTimeNS     uint64
	EndTimeNS       uint64
	ThreadID        uint32
	StackFrameCount uint32
	StackFrames     []uint64 // Parsed from payload after fixed struct
}

// SpanWithFullStack includes stack frame details for child spans
type SpanWithFullStack struct {
	SpanWithStackData
	StackIPs []uint64 // Instruction pointers for each frame
}

// ThreadSampleData matches C++ struct
type ThreadSampleData struct {
	TraceID         [32]byte
	OSThreadID      uint32
	StackFrameCount uint32
	TimestampNS     uint64
}

// IPC Message Header
type IPCMessageHeader struct {
	Magic       uint32
	Version     uint16
	Type        uint8
	PayloadSize uint32
	Sequence    uint64
}

// ============================================================================
// Agent with IPC Server
// ============================================================================

type Agent struct {
	config   *config.Config
	exporter *otel.Exporter
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	dotnetProfiler *DotNetProfiler
	pythonTracer   *PythonTracer
	ebpfEnabled    bool

	// Symbol table: address -> method name (for IP-based symbolication)
	symbolTable map[uint64]string
	symbolMu    sync.RWMutex

	// IPC Server
	ipcServer     *IPCServer
	ipcListener   net.Listener
	spanProcessor *SpanProcessor
}

type DotNetProfiler struct {
	libraryPath string
	configPath  string
	enabled     bool
	processes   map[int32]*DotNetProcess
	mu          sync.Mutex

	// Configuration from Go Agent
	samplingConfig config.DotNetSamplingConfig
}

type DotNetProcess struct {
	PID         int32
	CommandLine string
	StartedAt   time.Time
}

type PythonTracer struct {
	modulePath string
	enabled    bool
	agentPort  string
	processes  map[int32]*PythonProcess
	mu         sync.Mutex
}

type PythonProcess struct {
	PID         int32
	CommandLine string
	StartedAt   time.Time
}

// ============================================================================
// IPC Server
// ============================================================================

type IPCServer struct {
	socketPath string
	debug      bool
	listener   net.Listener
	spans      chan *SpanWithStackData
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	agent      *Agent // Reference to agent for symbol table
}

type SpanProcessor struct {
	mu            sync.Mutex
	spans         map[string]*SpanWithStackData // traceID -> span
	threadSamples map[string][]ThreadSampleData // traceID -> samples
	exceptions    map[string]ExceptionData      // traceID -> exception
	agent         *Agent                        // Reference to agent for symbol table
}

type ExceptionData struct {
	Type      string
	Message   string
	Timestamp uint64
}

func NewSpanProcessor(agent *Agent) *SpanProcessor {
	return &SpanProcessor{
		spans:         make(map[string]*SpanWithStackData),
		threadSamples: make(map[string][]ThreadSampleData),
		exceptions:    make(map[string]ExceptionData),
		agent:         agent,
	}
}

func (sp *SpanProcessor) AddSpan(span *SpanWithStackData) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	traceID := string(span.TraceID[:])
	sp.spans[traceID] = span
}

func (sp *SpanProcessor) AddThreadSample(sample *ThreadSampleData) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	traceID := string(sample.TraceID[:])
	if traceID == "" {
		return // No active trace for this sample
	}

	sp.threadSamples[traceID] = append(sp.threadSamples[traceID], *sample)
}

func (sp *SpanProcessor) AddException(traceID, exceptionType, message string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	sp.exceptions[traceID] = ExceptionData{
		Type:    exceptionType,
		Message: message,
	}
}

func (sp *SpanProcessor) GetException(traceID string) (ExceptionData, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	ex, ok := sp.exceptions[traceID]
	return ex, ok
}

func (sp *SpanProcessor) GetSpanWithSamples(traceID string) (*SpanWithStackData, []ThreadSampleData) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	span := sp.spans[traceID]
	samples := sp.threadSamples[traceID]

	return span, samples
}

func (sp *SpanProcessor) Clear(traceID string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	delete(sp.spans, traceID)
	delete(sp.threadSamples, traceID)
}

func NewIPCServer(socketPath string, debug bool, agent *Agent) *IPCServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &IPCServer{
		socketPath: socketPath,
		debug:      debug,
		spans:      make(chan *SpanWithStackData, 1000),
		ctx:        ctx,
		cancel:     cancel,
		agent:      agent,
	}
}

func (s *IPCServer) Start(spanProcessor *SpanProcessor) error {
	// Remove existing socket file
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	// Create Unix socket
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	s.listener = listener

	// Set socket permissions (optional)
	os.Chmod(s.socketPath, 0777)

	s.wg.Add(1)
	go s.acceptLoop(spanProcessor)

	if s.debug {
		fmt.Printf("[IPC] Server listening on %s\n", s.socketPath)
	}

	return nil
}

func (s *IPCServer) Stop() {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
	s.wg.Wait()
}

// SendCommand sends a command to connected .NET profiler/bridge
func (s *IPCServer) SendCommand(cmdType uint8, payload []byte) error {
	// For now, commands are sent via the existing connection
	// This would require bidirectional communication
	if s.debug {
		fmt.Printf("[IPC] Command sent: type=%d, payload=%d bytes\n", cmdType, len(payload))
	}
	return nil
}

// UpdateSamplingConfig sends new sampling configuration to .NET profiler
func (a *Agent) UpdateSamplingConfig(slowThresholdMs int, captureStack bool, endpointFilter string) error {
	if a.ipcServer == nil {
		return nil
	}

	// Build command payload
	payload := make([]byte, 260)
	// slowThresholdMs (4 bytes)
	binary.LittleEndian.PutUint32(payload[0:4], uint32(slowThresholdMs))
	// captureStack (1 byte)
	if captureStack {
		payload[4] = 1
	}
	// endpointFilter (255 bytes)
	if len(endpointFilter) > 255 {
		endpointFilter = endpointFilter[:255]
	}
	copy(payload[5:], endpointFilter)

	return a.ipcServer.SendCommand(IPCTypeCmdSetFilter, payload)
}

func (a *Agent) AddSymbol(address uint64, name string) {
	a.symbolMu.Lock()
	defer a.symbolMu.Unlock()
	a.symbolTable[address] = name
	fmt.Printf("[AGENT] Symbol added: addr=%x, name=%s\n", address, name)
}

func (a *Agent) GetSymbol(address uint64) string {
	a.symbolMu.RLock()
	defer a.symbolMu.RUnlock()
	name := a.symbolTable[address]
	if name == "" {
		fmt.Printf("[DEBUG-SYMBOL] Address 0x%x not found in symbol table (size=%d)\n", address, len(a.symbolTable))
	}
	return name
}

func parseW3CTraceID(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			return r
		}
		return -1
	}, raw)

	if len(cleaned) > 32 {
		if strings.HasPrefix(cleaned, "00") && len(cleaned) >= 34 {
			cleaned = cleaned[2:34]
		} else {
			cleaned = cleaned[:32]
		}
	}

	for len(cleaned) < 32 {
		cleaned = "0" + cleaned
	}

	return cleaned
}

func parseW3CSpanID(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			return r
		}
		return -1
	}, raw)

	if len(cleaned) > 16 {
		cleaned = cleaned[:16]
	}

	if len(cleaned) < 16 {
		cleaned = strings.Repeat("0", 16-len(cleaned)) + cleaned
	}

	return cleaned
}

func (s *IPCServer) acceptLoop(spanProcessor *SpanProcessor) {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				if s.debug {
					fmt.Printf("[IPC] Accept error: %v\n", err)
				}
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn, spanProcessor)
	}
}

func (s *IPCServer) handleConnection(conn net.Conn, spanProcessor *SpanProcessor) {
	defer s.wg.Done()
	defer conn.Close()

	buf := make([]byte, IPCMaxSize)

	for {
		// Read header first
		headerBuf := make([]byte, 19)
		n, err := conn.Read(headerBuf)
		if err != nil || n == 0 {
			return
		}

		if n < 19 {
			continue
		}

		header := IPCMessageHeader{
			Magic:       binary.LittleEndian.Uint32(headerBuf[0:4]),
			Version:     binary.LittleEndian.Uint16(headerBuf[4:6]),
			Type:        headerBuf[6],
			PayloadSize: binary.LittleEndian.Uint32(headerBuf[7:11]),
			Sequence:    binary.LittleEndian.Uint64(headerBuf[11:19]),
		}

		// Verify magic
		if header.Magic != IPCMagic {
			if s.debug {
				fmt.Printf("[IPC] Invalid magic: 0x%X\n", header.Magic)
			}
			continue
		}

		// Read payload
		if header.PayloadSize > 0 && header.PayloadSize <= IPCMaxSize {
			n, err = conn.Read(buf[:header.PayloadSize])
			if err != nil || n != int(header.PayloadSize) {
				continue
			}

			s.processMessage(header.Type, buf[:n], spanProcessor)
		}
	}
}

func (s *IPCServer) processMessage(msgType uint8, data []byte, spanProcessor *SpanProcessor) {
	switch msgType {
	case IPCTypeSpanWithStack:
		if len(data) >= 344 { // Size of SpanWithStackData
			var span SpanWithStackData
			copy(span.TraceID[:], data[0:32])
			copy(span.SpanID[:], data[32:48])
			copy(span.ParentSpanID[:], data[48:64])
			copy(span.OperationName[:], data[64:320])
			span.StartTimeNS = binary.LittleEndian.Uint64(data[320:328])
			span.EndTimeNS = binary.LittleEndian.Uint64(data[328:336])
			span.ThreadID = binary.LittleEndian.Uint32(data[336:340])
			span.StackFrameCount = binary.LittleEndian.Uint32(data[340:344])

			// Parse stack frame IPs if available
			if len(data) > 344 {
				frameCount := int(span.StackFrameCount)
				for i := 0; i < frameCount && (344+i*8+8) <= len(data); i++ {
					ip := binary.LittleEndian.Uint64(data[344+i*8:])
					span.StackFrames = append(span.StackFrames, ip)
					fmt.Printf("[DEBUG-FRAME] Frame %d: addr=0x%x\n", i, ip)
				}
			}

			spanProcessor.AddSpan(&span)

			if s.debug {
				fmt.Printf("[IPC] Span: %s, op: %s, frames=%d\n",
					string(span.TraceID[:]), string(span.OperationName[:]), span.StackFrameCount)
			}
		}

	case IPCTypeThreadSample:
		if len(data) >= 52 { // Size of ThreadSampleData
			var sample ThreadSampleData
			copy(sample.TraceID[:], data[0:32])
			sample.OSThreadID = binary.LittleEndian.Uint32(data[32:36])
			sample.StackFrameCount = binary.LittleEndian.Uint32(data[36:40])
			sample.TimestampNS = binary.LittleEndian.Uint64(data[40:48])

			spanProcessor.AddThreadSample(&sample)

			if s.debug {
				fmt.Printf("[IPC] Thread sample: trace=%s, thread=%d\n", string(sample.TraceID[:]), sample.OSThreadID)
			}
		}

	case IPCTypeHeartbeat:
		if s.debug {
			fmt.Printf("[IPC] Heartbeat received\n")
		}

	case IPCTypeSymbolUpdate:
		// Symbol update: 8 bytes address + null-terminated name
		fmt.Printf("[IPC-DEBUG] Received SymbolUpdate, len(data)=%d\n", len(data))
		if len(data) >= 9 && s.agent != nil {
			address := binary.LittleEndian.Uint64(data[0:8])
			name := string(data[8:])
			if idx := strings.Index(name, "\x00"); idx > 0 {
				name = name[:idx]
			}
			fmt.Printf("[IPC-DEBUG] SymbolUpdate: addr=0x%x, name=%s\n", address, name)
			// Store in agent's symbol table via SpanProcessor
			s.agent.AddSymbol(address, name)
		}

	case IPCTypeException:
		if len(data) >= 424 { // traceId(32) + exceptionType(128) + message(256) + timestamp(8)
			var exceptionData struct {
				TraceID       [32]byte
				ExceptionType [128]byte
				Message       [256]byte
				TimestampNS   uint64
			}
			copy(exceptionData.TraceID[:], data[0:32])
			copy(exceptionData.ExceptionType[:], data[32:160])
			copy(exceptionData.Message[:], data[160:416])
			exceptionData.TimestampNS = binary.LittleEndian.Uint64(data[416:424])

			exceptionType := string(exceptionData.ExceptionType[:])
			msg := string(exceptionData.Message[:])

			if s.debug {
				fmt.Printf("[IPC] Exception: type=%s, msg=%s\n", exceptionType, msg)
			}

			// Add exception event to span processor
			spanProcessor.AddException(string(exceptionData.TraceID[:]), exceptionType, msg)
		}
	}
}

// ============================================================================
// Agent Methods
// ============================================================================

func New(cfg *config.Config, exporter *otel.Exporter) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	agent := &Agent{
		config:   cfg,
		exporter: exporter,
		ctx:      ctx,
		cancel:   cancel,
		dotnetProfiler: &DotNetProfiler{
			processes: make(map[int32]*DotNetProcess),
		},
		pythonTracer: &PythonTracer{
			processes: make(map[int32]*PythonProcess),
		},
		spanProcessor: NewSpanProcessor(nil), // Will be set below
		symbolTable:   make(map[uint64]string),
	}
	agent.spanProcessor.agent = agent // Set back-reference
	return agent
}

func (a *Agent) Start() error {
	// Start IPC Server
	if a.config.IPC.SocketPath != "" {
		a.ipcServer = NewIPCServer(a.config.IPC.SocketPath, a.config.IPC.Debug, a)
		if err := a.ipcServer.Start(a.spanProcessor); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start IPC server: %v\n", err)
			// Continue without IPC - not critical
		}
	}

	if a.config.Hybrid.EBPF.Enabled {
		a.ebpfEnabled = true
		fmt.Println("Hybrid agent: eBPF enabled")
	}

	if a.config.Hybrid.DotNet.Enabled {
		if err := a.startDotNetProfiler(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start .NET profiler: %v\n", err)
		}
	}

	if a.config.Hybrid.Python.Enabled {
		if err := a.startPythonTracer(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start Python tracer: %v\n", err)
		}
	}

	a.wg.Add(1)
	go a.watchProcesses()

	// Start span processor (sends merged data to OTLP)
	a.wg.Add(1)
	go a.processSpanLoop()

	return nil
}

func (a *Agent) Stop() error {
	a.cancel()

	if a.ipcServer != nil {
		a.ipcServer.Stop()
	}

	a.wg.Wait()

	if a.dotnetProfiler.enabled {
		a.stopDotNetProfiler()
	}

	if a.pythonTracer.enabled {
		a.stopPythonTracer()
	}

	return nil
}

func (a *Agent) processSpanLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			// Process collected spans and samples
			// In production, this would merge spans with their thread samples
			// and export via OTLP
			a.flushSpanData()
		}
	}
}

func (a *Agent) flushSpanData() {
	a.spanProcessor.mu.Lock()
	defer a.spanProcessor.mu.Unlock()

	if len(a.spanProcessor.spans) > 0 {
		fmt.Printf("[DEBUG-FLUSH] Processing %d spans\n", len(a.spanProcessor.spans))
	}

	for traceID, span := range a.spanProcessor.spans {
		attrs := []attribute.KeyValue{
			attribute.String("profiler.type", "dotnet"),
			attribute.Int("process.pid", int(span.ThreadID)),
			attribute.Int64("duration.ns", int64(span.EndTimeNS-span.StartTimeNS)),
		}

		operationName := string(span.OperationName[:])
		if idx := strings.Index(operationName, "\x00"); idx > 0 {
			operationName = operationName[:idx]
		}

		if span.StackFrameCount > 0 {
			attrs = append(attrs, attribute.Int("stack.frame.count", int(span.StackFrameCount)))
		}

		if exc, ok := a.spanProcessor.exceptions[traceID]; ok {
			attrs = append(attrs,
				attribute.String("error", "true"),
				attribute.String("exception.type", exc.Type),
				attribute.String("exception.message", exc.Message),
			)
		}

		traceIDRaw := strings.TrimRight(string(span.TraceID[:]), "\x00")
		spanIDRaw := strings.TrimRight(string(span.SpanID[:]), "\x00")
		parentIDRaw := strings.TrimRight(string(span.ParentSpanID[:]), "\x00")

		fmt.Printf("[DEBUG-OTLP] Raw IDs: TraceID='%s', SpanID='%s', ParentID='%s'\n",
			traceIDRaw, spanIDRaw, parentIDRaw)

		traceIDStr := parseW3CTraceID(traceIDRaw)
		spanIDStr := parseW3CSpanID(spanIDRaw)
		parentIDStr := parseW3CSpanID(parentIDRaw)

		fmt.Printf("[DEBUG-OTLP] Parsed IDs: TraceID=%s (len=%d), SpanID=%s, ParentID=%s\n",
			traceIDStr, len(traceIDStr), spanIDStr, parentIDStr)

		var mainSpan otel.DirectSpan
		mainSpan.Name = operationName
		mainSpan.StartTime = time.Unix(0, int64(span.StartTimeNS))
		mainSpan.EndTime = time.Unix(0, int64(span.EndTimeNS))
		mainSpan.Attrs = attrs

		if traceIDBytes, err := hex.DecodeString(traceIDStr); err == nil && len(traceIDBytes) == 16 {
			copy(mainSpan.TraceID[:], traceIDBytes)
		}
		if spanIDBytes, err := hex.DecodeString(spanIDStr); err == nil && len(spanIDBytes) == 8 {
			copy(mainSpan.SpanID[:], spanIDBytes)
		}
		if parentIDBytes, err := hex.DecodeString(parentIDStr); err == nil && len(parentIDBytes) == 8 {
			copy(mainSpan.ParentID[:], parentIDBytes)
		}

		directSpans := []otel.DirectSpan{mainSpan}

		if len(span.StackFrames) > 0 {
			// Calculate frame duration from parent span
			// Each frame gets proportional slice of parent duration
			parentDuration := span.EndTimeNS - span.StartTimeNS
			numFrames := uint64(len(span.StackFrames))
			frameDuration := parentDuration / numFrames
			if frameDuration < 1000 { // Minimum 1µs per frame
				frameDuration = 1000
			}

			for i, ip := range span.StackFrames {
				frameName := a.GetSymbol(ip)
				if frameName == "" {
					frameName = fmt.Sprintf("frame_%d", i)
				}

				var childSpanID [8]byte
				rand.Read(childSpanID[:])

				// Calculate frame timing (distribute across parent timeline)
				frameStartNs := span.StartTimeNS + uint64(i)*frameDuration
				frameEndNs := frameStartNs + frameDuration
				if frameEndNs > span.EndTimeNS {
					frameEndNs = span.EndTimeNS
				}

				childSpan := otel.DirectSpan{
					IPCSpanContext: otel.IPCSpanContext{
						TraceID:  mainSpan.TraceID,
						SpanID:   childSpanID,
						ParentID: mainSpan.SpanID,
					},
					Name:      "fn:" + frameName,
					StartTime: time.Unix(0, int64(frameStartNs)),
					EndTime:   time.Unix(0, int64(frameEndNs)),
					Attrs:     attrs,
				}

				// Add frame-specific attributes
				frameCategory := categorizeFrame(frameName)
				childSpan.Attrs = append(childSpan.Attrs,
					attribute.String("frame.name", frameName),
					attribute.Int64("frame.ip", int64(ip)),
					attribute.Int("frame.depth", i),
					attribute.String("frame.category", frameCategory),
					attribute.Bool("frame.is_db", frameCategory == "database"),
					attribute.Bool("frame.is_async", frameCategory == "async"),
					attribute.Bool("frame.is_business", frameCategory == "business"),
				)

				// Mark slow frames (>100ms)
				frameDurationMs := frameDuration / 1e6 // ns to ms
				if frameDurationMs > 100 {
					childSpan.Attrs = append(childSpan.Attrs,
						attribute.Bool("frame.slow", true),
						attribute.Int64("frame.duration_ms", int64(frameDurationMs)),
					)
				}
				directSpans = append(directSpans, childSpan)
			}
		}

		if err := a.exporter.ExportSpansDirect(directSpans); err != nil {
			fmt.Printf("[DEBUG-OTLP] Export error: %v\n", err)
		}

		delete(a.spanProcessor.spans, traceID)
		delete(a.spanProcessor.threadSamples, traceID)
		delete(a.spanProcessor.exceptions, traceID)
	}
}

func (a *Agent) startDotNetProfiler() error {
	libraryPath := filepath.Join(a.config.Hybrid.DotNet.ProfilerPath, "liblangango_profiler.so")
	if _, err := os.Stat(libraryPath); os.IsNotExist(err) {
		return fmt.Errorf("dotnet profiler library not found: %s", libraryPath)
	}

	a.dotnetProfiler.libraryPath = libraryPath
	a.dotnetProfiler.enabled = true
	a.dotnetProfiler.samplingConfig = a.config.Hybrid.DotNet.SamplingConfig

	fmt.Printf("Started .NET Profiler Agent: %s\n", libraryPath)
	fmt.Printf("  Selective Sampling: threshold=%dms, interval=%dms\n",
		a.dotnetProfiler.samplingConfig.SlowRequestThresholdMs,
		a.dotnetProfiler.samplingConfig.SamplingIntervalMs)

	return nil
}

func (a *Agent) stopDotNetProfiler() {
	a.dotnetProfiler.mu.Lock()
	defer a.dotnetProfiler.mu.Unlock()

	for pid := range a.dotnetProfiler.processes {
		a.detachDotNetProfiler(pid)
	}
	a.dotnetProfiler.enabled = false
}

func (a *Agent) attachDotNetProfiler(pid int32) error {
	if !a.dotnetProfiler.enabled {
		return nil
	}

	env := os.Getenv("DOTNET_Profiler_Path")
	if env == "" {
		return fmt.Errorf("DOTNET_Profiler_Path not set")
	}

	cmd := exec.Command("kill", "-STOP", fmt.Sprintf("%d", pid))
	if err := cmd.Run(); err != nil {
		return err
	}
	defer exec.Command("kill", "-CONT", fmt.Sprintf("%d", pid)).Run()

	a.dotnetProfiler.mu.Lock()
	a.dotnetProfiler.processes[pid] = &DotNetProcess{
		PID:       pid,
		StartedAt: time.Now(),
	}
	a.dotnetProfiler.mu.Unlock()

	fmt.Printf("Attached .NET profiler to PID %d\n", pid)
	return nil
}

func (a *Agent) detachDotNetProfiler(pid int32) error {
	a.dotnetProfiler.mu.Lock()
	defer a.dotnetProfiler.mu.Unlock()

	if _, ok := a.dotnetProfiler.processes[pid]; !ok {
		return nil
	}

	delete(a.dotnetProfiler.processes, pid)
	fmt.Printf("Detached .NET profiler from PID %d\n", pid)
	return nil
}

func (a *Agent) startPythonTracer() error {
	modulePath := filepath.Join(a.config.Hybrid.Python.ModulePath, "langango")
	if _, err := os.Stat(modulePath); os.IsNotExist(err) {
		return fmt.Errorf("python tracer module not found: %s", modulePath)
	}

	a.pythonTracer.modulePath = modulePath
	a.pythonTracer.enabled = true
	a.pythonTracer.agentPort = a.config.Hybrid.Python.AgentPort

	fmt.Printf("Started Python Tracer: %s\n", modulePath)
	return nil
}

func (a *Agent) stopPythonTracer() {
	a.pythonTracer.mu.Lock()
	defer a.pythonTracer.mu.Unlock()

	for pid := range a.pythonTracer.processes {
		a.detachPythonTracer(pid)
	}
	a.pythonTracer.enabled = false
}

func (a *Agent) attachPythonTracer(pid int32) error {
	if !a.pythonTracer.enabled {
		return nil
	}

	cmd := exec.Command("python3", "-c", fmt.Sprintf(
		"import sys; sys.path.insert(0, '%s'); from langango import instrument; instrument('%s')",
		a.pythonTracer.modulePath, a.pythonTracer.agentPort,
	))

	env := os.Environ()
	env = append(env, fmt.Sprintf("PYTHONTRACEMASK=%d", pid))
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to attach python tracer: %w, output: %s", err, output)
	}

	a.pythonTracer.mu.Lock()
	a.pythonTracer.processes[pid] = &PythonProcess{
		PID:       pid,
		StartedAt: time.Now(),
	}
	a.pythonTracer.mu.Unlock()

	fmt.Printf("Attached Python tracer to PID %d\n", pid)
	return nil
}

func (a *Agent) detachPythonTracer(pid int32) error {
	a.pythonTracer.mu.Lock()
	defer a.pythonTracer.mu.Unlock()

	if _, ok := a.pythonTracer.processes[pid]; !ok {
		return nil
	}

	delete(a.pythonTracer.processes, pid)
	fmt.Printf("Detached Python tracer from PID %d\n", pid)
	return nil
}

func (a *Agent) watchProcesses() {
	defer a.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.checkForNewProcesses()
		}
	}
}

func (a *Agent) checkForNewProcesses() {
	if a.dotnetProfiler.enabled {
		a.checkDotNetProcesses()
	}
	if a.pythonTracer.enabled {
		a.checkPythonProcesses()
	}
}

func (a *Agent) checkDotNetProcesses() {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		_ = pid
	}
}

func (a *Agent) checkPythonProcesses() {
	// Similar logic for Python
}

// categorizeFrame determines the category of a frame based on its name
// and detects slow operations for better observability
func categorizeFrame(name string) string {
	lowerName := strings.ToLower(name)
	switch {
	// Database operations
	case strings.Contains(lowerName, "database") || strings.Contains(lowerName, "db") ||
		strings.Contains(lowerName, "query") || strings.Contains(lowerName, "sql") ||
		strings.Contains(lowerName, "entityframework") || strings.Contains(lowerName, "dapper") ||
		strings.Contains(lowerName, "npgsql") || strings.Contains(lowerName, "mysql") ||
		strings.Contains(lowerName, "sqlite") || strings.Contains(lowerName, "mongodb") ||
		strings.Contains(lowerName, "redis") || strings.Contains(lowerName, "connection"):
		return "database"

	// HTTP/API operations
	case strings.Contains(lowerName, "http") || strings.Contains(lowerName, "request") ||
		strings.Contains(lowerName, "client") || strings.Contains(lowerName, "api") ||
		strings.Contains(lowerName, "controller") || strings.Contains(lowerName, "middleware") ||
		strings.Contains(lowerName, "endpoint") || strings.Contains(lowerName, "router"):
		return "http"

	// Async operations
	case strings.Contains(lowerName, "async") || strings.Contains(lowerName, "task") ||
		strings.Contains(lowerName, "await") || strings.Contains(lowerName, "movenext") ||
		strings.Contains(lowerName, "statemachine") || strings.Contains(lowerName, "continuation") ||
		strings.Contains(lowerName, "taskcompletionsource"):
		return "async"

	// Business logic
	case strings.Contains(lowerName, "service") || strings.Contains(lowerName, "repository") ||
		strings.Contains(lowerName, "handler") || strings.Contains(lowerName, "manager") ||
		strings.Contains(lowerName, "validator") || strings.Contains(lowerName, "calculator") ||
		strings.Contains(lowerName, "processor") || strings.Contains(lowerName, "factory"):
		return "business"

	// Exception handling
	case strings.Contains(lowerName, "exception") || strings.Contains(lowerName, "error") ||
		strings.Contains(lowerName, "throw") || strings.Contains(lowerName, "catch") ||
		strings.Contains(lowerName, "fault") || strings.Contains(lowerName, "fail"):
		return "exception"

	// Serialization/IO
	case strings.Contains(lowerName, "serializ") || strings.Contains(lowerName, "deserializ") ||
		strings.Contains(lowerName, "json") || strings.Contains(lowerName, "xml") ||
		strings.Contains(lowerName, "file") || strings.Contains(lowerName, "stream"):
		return "io"

	default:
		return "runtime"
	}
}

// getFrameCategoryIcon returns an icon for frame category
func getFrameCategoryIcon(category string) string {
	switch category {
	case "database":
		return "🗄"
	case "http":
		return "🌐"
	case "async":
		return "⚡"
	case "business":
		return "💼"
	case "exception":
		return "❌"
	case "io":
		return "📁"
	default:
		return "⚙"
	}
}
