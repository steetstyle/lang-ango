package config

import (
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Service struct {
		Name        string `mapstructure:"name"`
		Version     string `mapstructure:"version"`
		Instance    string `mapstructure:"instance"`
		Environment string `mapstructure:"environment"`
	} `mapstructure:"service"`

	Discovery struct {
		PID      int           `mapstructure:"pid"`
		Ports    []int         `mapstructure:"ports"`
		Names    []string      `mapstructure:"names"`
		Interval time.Duration `mapstructure:"interval"`
	} `mapstructure:"discovery"`

	OTel struct {
		Endpoint string `mapstructure:"endpoint"`
		Insecure bool   `mapstructure:"insecure"`
	} `mapstructure:"otel"`

	Prometheus struct {
		Enabled bool `mapstructure:"enabled"`
		Port    int  `mapstructure:"port"`
	} `mapstructure:"prometheus"`

	Kubernetes struct {
		Enabled      bool          `mapstructure:"enabled"`
		ResyncPeriod time.Duration `mapstructure:"resync_period"`
		Namespace    string        `mapstructure:"namespace"`
	} `mapstructure:"kubernetes"`

	EBPF struct {
		BPFDir         string `mapstructure:"bpf_dir"`
		BufferSize     int    `mapstructure:"buffer_size"`
		PerfBufferSize int    `mapstructure:"perf_buffer_size"`
		MaxFlows       int    `mapstructure:"max_flows"`
	} `mapstructure:"ebpf"`

	Sampling struct {
		Traces struct {
			Initial     float64 `mapstructure:"initial"`
			Probability float64 `mapstructure:"probability"`
			RateLimit   int     `mapstructure:"rate_limit"`
		} `mapstructure:"traces"`
		Profiles struct {
			CPUHz int `mapstructure:"cpu_hz"`
		} `mapstructure:"profiles"`
	} `mapstructure:"sampling"`

	Protocols struct {
		HTTP     bool `mapstructure:"http"`
		GRPC     bool `mapstructure:"grpc"`
		Postgres bool `mapstructure:"postgres"`
		MySQL    bool `mapstructure:"mysql"`
		Redis    bool `mapstructure:"redis"`
		Kafka    bool `mapstructure:"kafka"`
	} `mapstructure:"protocols"`

	Hybrid struct {
		Enabled bool `mapstructure:"enabled"`

		EBPF struct {
			Enabled bool `mapstructure:"enabled"`
		} `mapstructure:"ebpf"`

		DotNet struct {
			Enabled        bool                 `mapstructure:"enabled"`
			ProfilerPath   string               `mapstructure:"profiler_path"`
			EventPipe      bool                 `mapstructure:"event_pipe"`
			SocketPath     string               `mapstructure:"socket_path"`
			SamplingConfig DotNetSamplingConfig `mapstructure:"selective_sampling"`
		} `mapstructure:"dotnet"`

		Python struct {
			Enabled    bool   `mapstructure:"enabled"`
			ModulePath string `mapstructure:"module_path"`
			AgentPort  string `mapstructure:"agent_port"`
		} `mapstructure:"python"`
	} `mapstructure:"hybrid"`

	IPC struct {
		SocketPath   string `mapstructure:"socket_path"`
		BufferSizeMB int    `mapstructure:"buffer_size_mb"`
		Debug        bool   `mapstructure:"debug"`
	} `mapstructure:"ipc"`
}

type DotNetSamplingConfig struct {
	SlowRequestThresholdMs uint32 `mapstructure:"slow_request_threshold_ms"`
	CaptureOnException     bool   `mapstructure:"capture_on_exception"`
	SamplingIntervalMs     uint32 `mapstructure:"sampling_interval_ms"`
	MaxStackDepth          uint32 `mapstructure:"max_stack_depth"`
}

func DefaultConfig() *Config {
	cfg := &Config{}

	cfg.Service.Name = "lang-ango"
	cfg.Service.Version = "0.1.0"
	cfg.Service.Environment = "development"

	cfg.Discovery.Interval = 10 * time.Second

	cfg.OTel.Endpoint = "localhost:4317"
	cfg.OTel.Insecure = true

	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Port = 9400

	cfg.Kubernetes.Enabled = false
	cfg.Kubernetes.ResyncPeriod = 30 * time.Second

	cfg.EBPF.BufferSize = 4096
	cfg.EBPF.PerfBufferSize = 8192
	cfg.EBPF.MaxFlows = 65536

	cfg.Sampling.Traces.Initial = 1.0
	cfg.Sampling.Traces.Probability = 0.01
	cfg.Sampling.Traces.RateLimit = 100

	cfg.Sampling.Profiles.CPUHz = 19

	cfg.Protocols.HTTP = true
	cfg.Protocols.GRPC = true
	cfg.Protocols.Postgres = true
	cfg.Protocols.MySQL = true
	cfg.Protocols.Redis = true
	cfg.Protocols.Kafka = false

	cfg.Hybrid.Enabled = true
	cfg.Hybrid.EBPF.Enabled = true
	cfg.Hybrid.DotNet.Enabled = true
	cfg.Hybrid.DotNet.ProfilerPath = "/usr/local/lib/lang-ango"
	cfg.Hybrid.DotNet.EventPipe = true
	cfg.Hybrid.DotNet.SocketPath = "/tmp/langango.sock"
	cfg.Hybrid.DotNet.SamplingConfig = DotNetSamplingConfig{
		SlowRequestThresholdMs: 2000,
		CaptureOnException:     true,
		SamplingIntervalMs:     100,
		MaxStackDepth:          64,
	}
	cfg.Hybrid.Python.Enabled = true
	cfg.Hybrid.Python.ModulePath = "/usr/local/lib/python3.10/site-packages"
	cfg.Hybrid.Python.AgentPort = "localhost:4317"

	cfg.IPC.SocketPath = "/tmp/langango.sock"
	cfg.IPC.BufferSizeMB = 64
	cfg.IPC.Debug = false

	return cfg
}

func (c *Config) LoadFromFile(path string) error {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	return viper.Unmarshal(c)
}

func (c *Config) LoadFromEnv() error {
	viper.AutomaticEnv()
	return viper.Unmarshal(c)
}
