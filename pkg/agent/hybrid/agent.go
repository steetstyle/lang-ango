package hybrid

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/yourorg/lang-ango/pkg/agent/config"
	"github.com/yourorg/lang-ango/pkg/otel"
)

type Agent struct {
	config   *config.Config
	exporter *otel.Exporter
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	dotnetProfiler *DotNetProfiler
	pythonTracer   *PythonTracer
	ebpfEnabled    bool
}

type DotNetProfiler struct {
	libraryPath string
	configPath  string
	enabled     bool
	processes   map[int32]*DotNetProcess
	mu          sync.Mutex
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

func New(cfg *config.Config, exporter *otel.Exporter) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
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
	}
}

func (a *Agent) Start() error {
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

	return nil
}

func (a *Agent) Stop() error {
	a.cancel()
	a.wg.Wait()

	if a.dotnetProfiler.enabled {
		a.stopDotNetProfiler()
	}

	if a.pythonTracer.enabled {
		a.stopPythonTracer()
	}

	return nil
}

func (a *Agent) startDotNetProfiler() error {
	libraryPath := filepath.Join(a.config.Hybrid.DotNet.ProfilerPath, "liblangango_profiler.so")
	if _, err := os.Stat(libraryPath); os.IsNotExist(err) {
		return fmt.Errorf("dotnet profiler library not found: %s", libraryPath)
	}

	a.dotnetProfiler.libraryPath = libraryPath
	a.dotnetProfiler.enabled = true

	fmt.Printf("Started .NET Profiler Agent: %s\n", libraryPath)
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
