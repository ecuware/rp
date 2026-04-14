package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Protocol defines the probing protocol
type Protocol string

const (
	ProtoICMP Protocol = "icmp"
	ProtoTCP  Protocol = "tcp"
	ProtoUDP  Protocol = "udp"
)

// Config holds all runtime configuration
type Config struct {
	// Target
	Target string

	// Probing
	Protocol     Protocol
	Port         int
	Interval     time.Duration
	Timeout      time.Duration
	MaxHops      int
	BufferSize   int // samples per hop

	// Traceroute
	RouteRefresh time.Duration

	// DNS
	ResolveDNS bool
	DNSTimeout time.Duration

	// Renderer
	RenderInterval time.Duration
	NoColor        bool

	// Thresholds (ms)
	WarnLatency     time.Duration
	CriticalLatency time.Duration
	WarnLoss        float64 // 0.0-1.0
	CriticalLoss    float64

	// Adaptive probing
	Adaptive bool

	// Export
	ExportJSON string
	ExportCSV  string
	ExportInterval time.Duration

	// Display
	ShowAll bool // show hops that don't respond
}

// Default values
const (
	DefaultInterval        = 1 * time.Second
	DefaultTimeout         = 3 * time.Second
	DefaultMaxHops         = 30
	DefaultBufferSize      = 100
	DefaultRouteRefresh    = 60 * time.Second
	DefaultRenderInterval  = 250 * time.Millisecond
	DefaultDNSTimeout      = 2 * time.Second
	DefaultWarnLatency     = 100 * time.Millisecond
	DefaultCriticalLatency = 300 * time.Millisecond
	DefaultWarnLoss        = 0.05
	DefaultCriticalLoss    = 0.20
	DefaultExportInterval  = 10 * time.Second
)

// Parse parses CLI flags and returns a Config
func Parse() (*Config, error) {
	cfg := &Config{}

	var showInfo bool
	flag.BoolVar(&showInfo, "info", false, "Show developer info and exit")

	flag.StringVar(&cfg.Target, "target", "", "Target host or IP address (required)")
	flag.StringVar((*string)(&cfg.Protocol), "protocol", string(ProtoICMP), "Probe protocol: icmp, tcp, udp")
	flag.IntVar(&cfg.Port, "port", 80, "Port for TCP/UDP probing")

	var intervalMs, timeoutMs, routeRefreshMs, renderMs, dnsToutMs int
	var warnMs, critMs int
	var exportIntervalMs int

	flag.IntVar(&intervalMs, "interval", 1000, "Probe interval in milliseconds")
	flag.IntVar(&timeoutMs, "timeout", 3000, "Probe timeout in milliseconds")
	flag.IntVar(&routeRefreshMs, "route-refresh", 60000, "Route refresh interval in milliseconds")
	flag.IntVar(&renderMs, "render-interval", 250, "Render interval in milliseconds")
	flag.IntVar(&dnsToutMs, "dns-timeout", 2000, "DNS timeout in milliseconds")
	flag.IntVar(&warnMs, "warn-latency", 100, "Warning latency threshold in milliseconds")
	flag.IntVar(&critMs, "critical-latency", 300, "Critical latency threshold in milliseconds")
	flag.IntVar(&exportIntervalMs, "export-interval", 10000, "Export interval in milliseconds")

	flag.IntVar(&cfg.MaxHops, "max-hops", DefaultMaxHops, "Maximum number of hops")
	flag.IntVar(&cfg.BufferSize, "buffer", DefaultBufferSize, "Number of samples to keep per hop")

	flag.Float64Var(&cfg.WarnLoss, "warn-loss", DefaultWarnLoss, "Warning packet loss threshold (0.0-1.0)")
	flag.Float64Var(&cfg.CriticalLoss, "critical-loss", DefaultCriticalLoss, "Critical packet loss threshold (0.0-1.0)")

	flag.BoolVar(&cfg.ResolveDNS, "dns", true, "Resolve hostnames via reverse DNS")
	flag.BoolVar(&cfg.NoColor, "no-color", false, "Disable color output")
	flag.BoolVar(&cfg.Adaptive, "adaptive", false, "Enable adaptive probing (experimental)")
	flag.BoolVar(&cfg.ShowAll, "show-all", false, "Show hops with no response")

	flag.StringVar(&cfg.ExportJSON, "export-json", "", "Export results to JSON file (empty = disabled)")
	flag.StringVar(&cfg.ExportCSV, "export-csv", "", "Export results to CSV file (empty = disabled)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "netplotter - Real-time network path monitoring tool\n\n")
		fmt.Fprintf(os.Stderr, "Usage: netplotter [flags] --target <host>\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  netplotter --target 8.8.8.8\n")
		fmt.Fprintf(os.Stderr, "  netplotter --target google.com --protocol tcp --port 443\n")
		fmt.Fprintf(os.Stderr, "  netplotter --target 1.1.1.1 --interval 500 --max-hops 20\n")
		fmt.Fprintf(os.Stderr, "  netplotter --target 8.8.8.8 --export-json /tmp/results.json\n")
	}

	flag.Parse()

	if showInfo {
		fmt.Println("Alptekin Sünnetci")
		os.Exit(0)
	}

	// Allow positional target argument
	if cfg.Target == "" && flag.NArg() > 0 {
		cfg.Target = flag.Arg(0)
	}

	// Interactive prompt if still no target
	if cfg.Target == "" {
		fmt.Print("Target host or IP: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			cfg.Target = strings.TrimSpace(scanner.Text())
		}
	}

	// Convert ms to durations
	cfg.Interval = time.Duration(intervalMs) * time.Millisecond
	cfg.Timeout = time.Duration(timeoutMs) * time.Millisecond
	cfg.RouteRefresh = time.Duration(routeRefreshMs) * time.Millisecond
	cfg.RenderInterval = time.Duration(renderMs) * time.Millisecond
	cfg.DNSTimeout = time.Duration(dnsToutMs) * time.Millisecond
	cfg.WarnLatency = time.Duration(warnMs) * time.Millisecond
	cfg.CriticalLatency = time.Duration(critMs) * time.Millisecond
	cfg.ExportInterval = time.Duration(exportIntervalMs) * time.Millisecond

	return cfg, cfg.Validate()
}

// Validate checks configuration validity
func (c *Config) Validate() error {
	if c.Target == "" {
		return fmt.Errorf("target is required (use --target <host>)")
	}
	if c.MaxHops < 1 || c.MaxHops > 64 {
		return fmt.Errorf("max-hops must be between 1 and 64")
	}
	if c.BufferSize < 10 || c.BufferSize > 10000 {
		return fmt.Errorf("buffer must be between 10 and 10000")
	}
	if c.Interval < 100*time.Millisecond {
		return fmt.Errorf("interval must be at least 100ms")
	}
	if c.Timeout < 100*time.Millisecond {
		return fmt.Errorf("timeout must be at least 100ms")
	}
	if c.WarnLoss < 0 || c.WarnLoss > 1 {
		return fmt.Errorf("warn-loss must be between 0.0 and 1.0")
	}
	if c.CriticalLoss < 0 || c.CriticalLoss > 1 {
		return fmt.Errorf("critical-loss must be between 0.0 and 1.0")
	}
	if c.WarnLoss > c.CriticalLoss {
		return fmt.Errorf("warn-loss must be less than or equal to critical-loss")
	}
	switch c.Protocol {
	case ProtoICMP, ProtoTCP, ProtoUDP:
		// valid
	default:
		return fmt.Errorf("protocol must be one of: icmp, tcp, udp")
	}
	return nil
}
