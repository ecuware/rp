package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	Target  string
	Targets []string

	// Probing
	Protocol     Protocol
	Port         int
	Interval     time.Duration
	Timeout      time.Duration
	MaxHops      int
	BufferSize   int // samples per hop
	ProbeWorkers int

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
	ExportJSON     string
	ExportCSV      string
	ExportTXT      string
	ExportInterval time.Duration
	DiffFile       string

	// IPv6
	UseIPv6    bool   // force IPv6 (auto-detect from AAAA if false)
	IPv6Only   bool   // fail if target has no IPv6 address
	IPv6Format string // "compact" or "full"

	// Display
	ShowAll   bool // show hops that don't respond
	PanelSort string
	ViewMode  string
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

// DesktopDir returns the path to the user's Desktop directory, cross-platform.
func DesktopDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	desktop := filepath.Join(home, "Desktop")
	if _, err := os.Stat(desktop); err == nil {
		return desktop
	}
	return home
}

// Parse parses CLI flags and returns a Config
func Parse() (*Config, error) {
	cfg := &Config{}

	var showInfo bool
	var showHelp bool
	flag.BoolVar(&showInfo, "info", false, "Show developer info and exit")
	flag.BoolVar(&showHelp, "help", false, "Show help and exit")

	var targetsCSV string
	flag.StringVar(&cfg.Target, "target", "", "Target host or IP address (required)")
	flag.StringVar(&targetsCSV, "targets", "", "Comma-separated targets (overrides --target)")
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
	flag.IntVar(&cfg.ProbeWorkers, "probe-concurrency", 32, "Max concurrent probes per target")

	flag.Float64Var(&cfg.WarnLoss, "warn-loss", DefaultWarnLoss, "Warning packet loss threshold (0.0-1.0)")
	flag.Float64Var(&cfg.CriticalLoss, "critical-loss", DefaultCriticalLoss, "Critical packet loss threshold (0.0-1.0)")

	flag.BoolVar(&cfg.ResolveDNS, "dns", true, "Resolve hostnames via reverse DNS")
	flag.BoolVar(&cfg.NoColor, "no-color", false, "Disable color output")
	flag.BoolVar(&cfg.Adaptive, "adaptive", false, "Enable adaptive probing (experimental)")
	flag.BoolVar(&cfg.ShowAll, "show-all", false, "Show hops with no response")
	flag.StringVar(&cfg.PanelSort, "panel-sort", "target", "Panel sort: target, loss, avg")
	flag.StringVar(&cfg.ViewMode, "view", "all", "View mode: avg, loss, all")

	flag.StringVar(&cfg.ExportJSON, "export-json", "", "Export results to JSON file (empty = disabled, \"desktop\" = ~/Desktop/rp.json)")
	flag.StringVar(&cfg.ExportCSV, "export-csv", "", "Export results to CSV file (empty = disabled, \"desktop\" = ~/Desktop/rp.csv)")
	flag.StringVar(&cfg.ExportTXT, "export-txt", "", "Export results to TXT file (empty = disabled, \"desktop\" = ~/Desktop/rp.txt)")
	flag.StringVar(&cfg.DiffFile, "diff-file", "", "Compare against a previous JSON export (optional)")

	flag.BoolVar(&cfg.UseIPv6, "ipv6", false, "Use IPv6 (auto-detect from AAAA record if false)")
	flag.BoolVar(&cfg.IPv6Only, "ipv6-only", false, "Fail if target has no IPv6 address")
	flag.StringVar(&cfg.IPv6Format, "ipv6-format", "compact", "IPv6 address format: compact, full")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "rp - Real-time network path monitoring tool\n\n")
		fmt.Fprintf(os.Stderr, "Usage: rp [flags] --target <host>\n")
		fmt.Fprintf(os.Stderr, "       rp [flags] --targets a,b,c\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  rp --target 8.8.8.8\n")
		fmt.Fprintf(os.Stderr, "  rp --targets 8.8.8.8,1.1.1.1\n")
		fmt.Fprintf(os.Stderr, "  rp --target google.com --protocol tcp --port 443\n")
		fmt.Fprintf(os.Stderr, "  rp --target 1.1.1.1 --interval 500 --max-hops 20\n")
		fmt.Fprintf(os.Stderr, "  rp --target 8.8.8.8 --export-json desktop\n")
		fmt.Fprintf(os.Stderr, "  rp --target 8.8.8.8 --export-csv desktop --export-txt desktop\n")
	}

	flag.Parse()

	if showHelp {
		if flag.NArg() > 0 && flag.Arg(0) == "flags" {
			flag.Usage()
			os.Exit(0)
		}
		printShortHelp()
		os.Exit(0)
	}

	if showInfo {
		fmt.Println("rp (Route Print) - Real-time network path monitoring tool")
		fmt.Println()
		fmt.Println("A real-time traceroute tool that discovers and monitors the network path")
		fmt.Println("to a target host. Displays per-hop latency, packet loss, jitter, and live")
		fmt.Println("sparkline graphs. Supports IPv4/IPv6, multi-target panels, and JSON/CSV/TXT export.")
		fmt.Println()
		fmt.Println("Created By TRNOG - trnog.net")
		fmt.Println()
		fmt.Println("Contributors:")
		fmt.Println("  Osman Makal")
		fmt.Println("  Alptekin Sünnetci")
		fmt.Println("  Eren Can Uçar")
		os.Exit(0)
	}

	// Allow positional target argument
	if cfg.Target == "" && targetsCSV == "" && flag.NArg() > 0 {
		cfg.Target = flag.Arg(0)
	}

	// Interactive prompt if still no target
	if cfg.Target == "" && targetsCSV == "" {
		fmt.Print("Target host or IP: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				if strings.ContainsAny(line, ", ") {
					fields := strings.FieldsFunc(line, func(r rune) bool {
						return r == ',' || r == ' ' || r == '\t'
					})
					if len(fields) > 1 {
						targetsCSV = strings.Join(fields, ",")
					} else {
						cfg.Target = line
					}
				} else {
					cfg.Target = line
				}
			}
		}
	}

	// Build target list
	if targetsCSV != "" {
		parts := strings.Split(targetsCSV, ",")
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cfg.Targets = append(cfg.Targets, t)
			}
		}
	} else if cfg.Target != "" {
		cfg.Targets = []string{cfg.Target}
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

	desktop := DesktopDir()
	if cfg.ExportJSON == "desktop" {
		cfg.ExportJSON = filepath.Join(desktop, "rp.json")
	}
	if cfg.ExportCSV == "desktop" {
		cfg.ExportCSV = filepath.Join(desktop, "rp.csv")
	}
	if cfg.ExportTXT == "desktop" {
		cfg.ExportTXT = filepath.Join(desktop, "rp.txt")
	}

	return cfg, cfg.Validate()
}

// Validate checks configuration validity
func (c *Config) Validate() error {
	if len(c.Targets) == 0 {
		return fmt.Errorf("target is required (use --target <host> or --targets a,b,c)")
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
	if c.ProbeWorkers < 1 || c.ProbeWorkers > 1024 {
		return fmt.Errorf("probe-concurrency must be between 1 and 1024")
	}
	switch c.PanelSort {
	case "target", "loss", "avg":
		// valid
	default:
		return fmt.Errorf("panel-sort must be one of: target, loss, avg")
	}
	switch c.ViewMode {
	case "avg", "loss", "all":
		// valid
	default:
		return fmt.Errorf("view must be one of: avg, loss, all")
	}
	switch c.Protocol {
	case ProtoICMP, ProtoTCP, ProtoUDP:
		// valid
	default:
		return fmt.Errorf("protocol must be one of: icmp, tcp, udp")
	}
	switch c.IPv6Format {
	case "compact", "full":
		// valid
	default:
		return fmt.Errorf("ipv6-format must be one of: compact, full")
	}
	return nil
}

func printShortHelp() {
	out := os.Stdout
	fmt.Fprintln(out, "rp - Real-time network path monitoring tool")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  rp --target <host>")
	fmt.Fprintln(out, "  rp --targets a,b,c")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Core Options:")
	fmt.Fprintln(out, "  --target <host>              Single target")
	fmt.Fprintln(out, "  --targets a,b,c              Multiple targets")
	fmt.Fprintln(out, "  --view avg|loss|all          View mode (default: all)")
	fmt.Fprintln(out, "  --panel-sort target|loss|avg Panel ordering")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Exit:")
	fmt.Fprintln(out, "  Q    Quit the app")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  rp --target 8.8.8.8")
	fmt.Fprintln(out, "  rp --targets 8.8.8.8,1.1.1.1 --view loss")
	fmt.Fprintln(out, "  rp --target google.com --protocol tcp --port 443")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Full flag list: --help flags")
}
