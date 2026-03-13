package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yourorg/lang-ango/pkg/agent/config"
	"github.com/yourorg/lang-ango/pkg/agent/hybrid"
	"github.com/yourorg/lang-ango/pkg/ebpf"
	"github.com/yourorg/lang-ango/pkg/otel"
	"github.com/yourorg/lang-ango/pkg/processor"
)

var (
	Version    string
	Build      string
	configFile string
	pid        int
	port       int
	bpfDir     string
	testMode   bool
)

func init() {
	flag.StringVar(&configFile, "config", "", "Configuration file path")
	flag.IntVar(&pid, "pid", 0, "Process ID to instrument (0 for all)")
	flag.IntVar(&port, "port", 0, "Port to instrument (0 for all)")
	flag.StringVar(&bpfDir, "bpf-dir", "/var/run/lang-ango/bpf", "Directory containing compiled eBPF objects")
	flag.BoolVar(&testMode, "test-mode", false, "Run in test mode with fake spans for testing")
}

func main() {
	flag.Parse()

	// Fall back to LANG_ANGO_CONFIG env var when -config flag is not provided.
	if configFile == "" {
		configFile = os.Getenv("LANG_ANGO_CONFIG")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := loadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// OTEL_EXPORTER_OTLP_ENDPOINT overrides the config file value.
	// The standard env var uses a full URL (e.g. http://host:4317) but
	// otlptracegrpc expects host:port, so strip the scheme.
	if ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); ep != "" {
		ep = strings.TrimPrefix(ep, "https://")
		ep = strings.TrimPrefix(ep, "http://")
		cfg.OTel.Endpoint = ep
		cfg.OTel.Insecure = true
	}

	if Version != "" {
		fmt.Printf("Lang-Ango v%s (build: %s)\n", Version, Build)
	}

	otelExporter, err := otel.NewExporter(&otel.Config{
		OTelEndpoint:   cfg.OTel.Endpoint,
		OTelInsecure:   cfg.OTel.Insecure,
		ServiceName:    cfg.Service.Name,
		ServiceVersion: cfg.Service.Version,
		Environment:    cfg.Service.Environment,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize OTel exporter: %v\n", err)
		os.Exit(1)
	}
	defer otelExporter.Close()

	proc := processor.New(otelExporter, cfg.Service.Name, cfg.Service.Environment)

	// Start hybrid agent (includes IPC server for .NET profiler)
	agent := hybrid.New(cfg, otelExporter)
	if err := agent.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start hybrid agent: %v\n", err)
	}
	defer agent.Stop()

	if testMode {
		fmt.Println("[TEST MODE] Starting test span generator")
		go runTestMode(ctx, proc)
	} else {
		if err := ebpf.EnsurePermissions(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to ensure eBPF permissions: %v\n", err)
			os.Exit(1)
		}

		loader := ebpf.New()
		defer loader.Close()

		if err := setupEBPF(loader, bpfDir, proc); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to setup eBPF: %v\n", err)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	fmt.Println("Lang-Ango agent started successfully")
	fmt.Printf("Listening on ports: %v\n", cfg.Discovery.Ports)
	fmt.Printf("OTLP endpoint: %s\n", cfg.OTel.Endpoint)

	<-ctx.Done()
	fmt.Println("Agent stopped")
}

func loadConfig(path string) (*config.Config, error) {
	cfg := config.DefaultConfig()

	if path != "" {
		if err := cfg.LoadFromFile(path); err != nil {
			return nil, err
		}
	}

	if pid > 0 {
		cfg.Discovery.PID = pid
	}
	if port > 0 {
		cfg.Discovery.Ports = []int{port}
	}

	return cfg, nil
}

func setupEBPF(loader *ebpf.Loader, bpfDir string, proc *processor.Processor) error {
	programs := []string{
		"socktrace",
		"ssltrace",
		"tracepoint",
		"dotnet",
		"postgres",
		"sqlserver",
		"golang",
		"python",
		"stack",
		"dotnet_hooks",
	}

	for _, prog := range programs {
		objPath := filepath.Join(bpfDir, prog+".o")
		if _, err := os.Stat(objPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: BPF object not found: %s\n", objPath)
			continue
		}

		if err := loader.LoadBPF(bpfDir, prog); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to load %s: %v\n", prog, err)
			continue
		}

		fmt.Printf("Loaded eBPF program: %s\n", prog)
	}

	setupRingBufHandlers(loader, proc)

	return nil
}

func setupRingBufHandlers(loader *ebpf.Loader, proc *processor.Processor) {
	ringBufMaps := []string{
		"http_events",
		"db_events",
		"tls_events",
		"exception_events",
		"go_events",
		"python_events",
		"cpu_profile_events",
		"dotnet_method_events",
	}

	for _, mapName := range ringBufMaps {
		err := loader.StartRingBufHandler(mapName, func(data []byte) error {
			return handleEvent(mapName, data, proc)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to start handler for %s: %v\n", mapName, err)
			continue
		}
		fmt.Printf("Started handler for: %s\n", mapName)
	}
}

func handleEvent(mapName string, data []byte, proc *processor.Processor) error {
	switch mapName {
	case "http_events", "http_events_ringbuf":
		return proc.HandleHTTP(data)
	case "db_events", "db_events_ringbuf":
		return proc.HandleDB(data)
	case "tls_events", "tls_events_ringbuf":
		return proc.HandleTLS(data)
	case "exception_events", "dotnet_exceptions", "go_events", "python_events":
		return proc.HandleException(data)
	case "cpu_profile_events":
		return proc.HandleCPUProfile(data)
	case "dotnet_method_events":
		return proc.HandleMethodEvent(data)
	default:
		fmt.Printf("Unknown event map: %s\n", mapName)
	}
	return nil
}

func runTestMode(ctx context.Context, proc *processor.Processor) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			generateTestSpan(proc)
		}
	}
}

func generateTestSpan(proc *processor.Processor) {
	if err := proc.GenerateTestHTTP(); err != nil {
		fmt.Fprintf(os.Stderr, "Test HTTP span error: %v\n", err)
	} else {
		fmt.Println("[TEST MODE] Generated test HTTP span")
	}

	if err := proc.GenerateTestDB(); err != nil {
		fmt.Fprintf(os.Stderr, "Test DB span error: %v\n", err)
	} else {
		fmt.Println("[TEST MODE] Generated test DB span")
	}

	if err := proc.GenerateTestException(); err != nil {
		fmt.Fprintf(os.Stderr, "Test Exception span error: %v\n", err)
	} else {
		fmt.Println("[TEST MODE] Generated test Exception span")
	}

	if err := proc.GenerateTestMethod(); err != nil {
		fmt.Fprintf(os.Stderr, "Test Method span error: %v\n", err)
	} else {
		fmt.Println("[TEST MODE] Generated test Method span")
	}

	if err := proc.GenerateTestCPUProfile(); err != nil {
		fmt.Fprintf(os.Stderr, "Test CPU Profile span error: %v\n", err)
	} else {
		fmt.Println("[TEST MODE] Generated test CPU Profile span")
	}
}
