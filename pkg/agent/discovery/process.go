package discovery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/process"
)

type ProcessInfo struct {
	PID         int32
	PPID        int32
	Name        string
	Exe         string
	CmdLine     []string
	Env         map[string]string
	CWD         string
	Username    string
	NumThreads  int32
	NumFDs      int32
	MemoryInfo  *MemoryInfo
	CPUPercent  float64
	Connections []*ConnectionInfo
	OpenFiles   []*OpenFileInfo
	StartTime   time.Time
	Ports       []int
}

type MemoryInfo struct {
	RSS     uint64
	VMS     uint64
	Swap    uint64
	Percent float64
}

type ConnectionInfo struct {
	Protocol string
	LAddr    string
	LPort    int
	RAddr    string
	RPort    int
	Status   string
}

type OpenFileInfo struct {
	Path string
	FD   int
	Mode string
}

type Discovery struct {
	config      *Config
	mu          sync.RWMutex
	processes   map[int32]*ProcessInfo
	watchedPIDs map[int32]bool
	stopCh      chan struct{}
}

type Config struct {
	Interval   time.Duration
	PID        int32
	Names      []string
	Ports      []int
	Envs       []string
	AutoAttach bool
	RootDir    string
}

func New(config *Config) *Discovery {
	return &Discovery{
		config:      config,
		processes:   make(map[int32]*ProcessInfo),
		watchedPIDs: make(map[int32]bool),
		stopCh:      make(chan struct{}),
	}
}

func (d *Discovery) Start(ctx context.Context) error {
	if err := d.scanProcesses(); err != nil {
		return fmt.Errorf("initial scan failed: %w", err)
	}

	if d.config.AutoAttach {
		go d.watchForNewProcesses(ctx)
	}

	go d.runPeriodicScan(ctx)

	return nil
}

func (d *Discovery) Stop() {
	close(d.stopCh)
}

func (d *Discovery) runPeriodicScan(ctx context.Context) {
	ticker := time.NewTicker(d.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.scanProcesses()
		}
	}
}

func (d *Discovery) watchForNewProcesses(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	knownPIDs := make(map[int32]bool)
	for pid := range d.processes {
		knownPIDs[pid] = true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			currentProcs, err := process.Processes()
			if err != nil {
				continue
			}

			for _, p := range currentProcs {
				pid := p.Pid
				if !knownPIDs[pid] {
					procInfo, err := d.getProcessInfo(pid)
					if err != nil {
						continue
					}

					if d.matchesFilter(procInfo) {
						d.mu.Lock()
						d.processes[pid] = procInfo
						d.watchedPIDs[pid] = true
						d.mu.Unlock()

						knownPIDs[pid] = true
						fmt.Printf("Discovered new process: %s (PID: %d)\n", procInfo.Name, pid)
					}
				}
			}
		}
	}
}

func (d *Discovery) scanProcesses() error {
	procs, err := process.Processes()
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	seen := make(map[int32]bool)

	for _, p := range procs {
		seen[p.Pid] = true

		if _, ok := d.processes[p.Pid]; !ok {
			procInfo, err := d.getProcessInfo(p.Pid)
			if err != nil {
				continue
			}

			if d.matchesFilter(procInfo) {
				d.processes[p.Pid] = procInfo
			}
		}
	}

	for pid := range d.processes {
		if !seen[pid] {
			delete(d.processes, pid)
			delete(d.watchedPIDs, pid)
		}
	}

	return nil
}

func (d *Discovery) getProcessInfo(pid int32) (*ProcessInfo, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return nil, err
	}

	info := &ProcessInfo{
		PID:        pid,
		MemoryInfo: &MemoryInfo{},
	}

	info.PPID, _ = p.Ppid()
	info.Name, _ = p.Name()
	info.NumThreads, _ = p.NumThreads()
	info.NumFDs, _ = p.NumFDs()

	if exe, err := p.Exe(); err == nil {
		info.Exe = exe
	}

	if cmdline, err := p.CmdlineSlice(); err == nil {
		info.CmdLine = cmdline
	}

	if cwd, err := p.Cwd(); err == nil {
		info.CWD = cwd
	}

	if username, err := p.Username(); err == nil {
		info.Username = username
	}

	memInfo, _ := p.MemoryInfo()
	if memInfo != nil {
		info.MemoryInfo.RSS = memInfo.RSS
		info.MemoryInfo.VMS = memInfo.VMS
		info.MemoryInfo.Swap = memInfo.Swap
	}

	if cpuPercent, err := p.CPUPercent(); err == nil {
		info.CPUPercent = cpuPercent
	}

	if createTime, err := p.CreateTime(); err == nil {
		info.StartTime = time.Unix(0, createTime*int64(time.Millisecond))
	}

	info.Ports = d.getPortsForProcess(pid)

	return info, nil
}

func (d *Discovery) getPortsForProcess(pid int32) []int {
	ports := make([]int, 0)

	netDirs := []string{"/proc", "/proc/self/fd"}
	for _, dir := range netDirs {
		fdDir := filepath.Join(dir, "fd")
		if _, err := os.Stat(fdDir); err != nil {
			continue
		}

		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if err != nil {
				continue
			}

			if strings.HasPrefix(link, "socket:[") {
				ports = append(ports, d.getPortFromSocket(link))
			}
		}
	}

	return ports
}

func (d *Discovery) getPortFromSocket(link string) int {
	socketID := strings.Trim(link, "socket:[]")
	inode, err := strconv.Atoi(socketID)
	if err != nil {
		return 0
	}

	netDir := "/proc/net/tcp"
	data, err := os.Open(netDir)
	if err != nil {
		return 0
	}
	defer data.Close()

	scanner := bufio.NewScanner(data)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		sl := strings.TrimLeft(fields[9], "0")
		sl = strings.TrimLeft(sl, "0")
		if sl == "" {
			sl = "0"
		}

		inodeNum, err := strconv.ParseInt(sl, 16, 64)
		if err != nil {
			continue
		}

		if int(inodeNum) == inode {
			localAddr := fields[1]
			parts := strings.Split(localAddr, ":")
			if len(parts) == 2 {
				port, _ := strconv.ParseInt(parts[1], 16, 64)
				return int(port)
			}
		}
	}

	return 0
}

func (d *Discovery) matchesFilter(info *ProcessInfo) bool {
	if d.config.PID > 0 && info.PID != d.config.PID {
		return false
	}

	if len(d.config.Names) > 0 {
		matched := false
		for _, name := range d.config.Names {
			if strings.Contains(info.Name, name) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(d.config.Ports) > 0 {
		matched := false
		for _, port := range d.config.Ports {
			for _, p := range info.Ports {
				if p == port {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(d.config.Envs) > 0 {
		matched := false
		for _, env := range d.config.Envs {
			for k := range info.Env {
				if strings.Contains(k, env) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched && len(d.config.Envs) > 0 {
			return false
		}
	}

	return true
}

func (d *Discovery) GetProcess(pid int32) *ProcessInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.processes[pid]
}

func (d *Discovery) GetAllProcesses() map[int32]*ProcessInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make(map[int32]*ProcessInfo)
	for k, v := range d.processes {
		result[k] = v
	}
	return result
}

func (d *Discovery) GetWatchedPIDs() []int32 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	pids := make([]int32, 0, len(d.watchedPIDs))
	for pid := range d.watchedPIDs {
		pids = append(pids, pid)
	}
	return pids
}

func (d *Discovery) Attach(pid int32) error {
	procInfo, err := d.getProcessInfo(pid)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.processes[pid] = procInfo
	d.watchedPIDs[pid] = true

	return nil
}

func (d *Discovery) Detach(pid int32) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.processes, pid)
	delete(d.watchedPIDs, pid)

	return nil
}

func (d *Discovery) FilterByPort(port int) []*ProcessInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*ProcessInfo, 0)

	for _, proc := range d.processes {
		for _, p := range proc.Ports {
			if p == port {
				result = append(result, proc)
				break
			}
		}
	}

	return result
}

func (d *Discovery) FilterByName(name string) []*ProcessInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*ProcessInfo, 0)

	for _, proc := range d.processes {
		if strings.Contains(proc.Name, name) {
			result = append(result, proc)
		}
	}

	return result
}
