package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nopityNop/nnu/internal/core/config"
	"github.com/nopityNop/nnu/internal/core/ping"
	"github.com/nopityNop/nnu/internal/core/traceroute"
	platformping "github.com/nopityNop/nnu/internal/platform/ping"
	platformtraceroute "github.com/nopityNop/nnu/internal/platform/traceroute"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Default()

	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return nil
	}

	cmd := strings.ToLower(args[0])
	switch cmd {
	case "ping":
		return runPing(cfg, args[1:])
	case "trace", "traceroute":
		return runTrace(cfg, args[1:])
	case "config":
		return runConfig(cfg)
	case "help", "-h", "--help":
		printHelp()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func runPing(cfg config.Config, args []string) error {
	input, cycles, err := parsePingArgs(args)
	if err != nil {
		return err
	}
	targets, err := loadPingTargets(input)
	if err != nil {
		return fmt.Errorf("load targets: %w", err)
	}
	adapter, err := platformping.NewAdapter()
	if err != nil {
		return fmt.Errorf("create ICMP adapter: %w", err)
	}
	defer adapter.Close()

	pingCfg := ping.Config{
		Count:               1,
		Timeout:             time.Duration(cfg.Ping.TimeoutMs) * time.Millisecond,
		Delay:               0,
		MaxConcurrent:       cfg.Ping.MaxConcurrent,
		ConsecutiveTimeouts: 1,
	}
	echoFn := func(host string, ttl int, timeout time.Duration) (ping.EchoResult, error) {
		return adapter.Echo(host, ttl, timeout)
	}
	engine := ping.NewEngine(pingCfg)
	aggregate := make(map[string]*pingAggregate, len(targets))
	order := make([]string, 0, len(targets))
	for _, target := range targets {
		if _, exists := aggregate[target.IP]; exists {
			continue
		}
		aggregate[target.IP] = &pingAggregate{}
		order = append(order, target.IP)
	}
	for cycle := 1; cycle <= cycles; cycle++ {
		results := engine.Ping(context.Background(), targets, echoFn)
		updatePingAggregate(aggregate, results)
		printPingCycle(cycle, cycles, aggregate, order)
	}
	return nil
}

func runTrace(cfg config.Config, args []string) error {
	input, cycles, err := parseTraceArgs(args)
	if err != nil {
		return err
	}
	hosts, err := loadTraceHosts(input)
	if err != nil {
		return fmt.Errorf("load trace hosts: %w", err)
	}
	traceCfg := traceroute.Config{
		MaxHops:    cfg.Traceroute.MaxHops,
		Timeout:    time.Duration(cfg.Traceroute.TimeoutMs) * time.Millisecond,
		Interval:   time.Duration(cfg.Traceroute.IntervalMs) * time.Millisecond,
		PacketSize: 64,
		Probes:     cfg.Traceroute.Probes,
		Passes:     cycles,
		UseDNS:     cfg.Traceroute.UseDNS,
	}
	adapter, err := platformtraceroute.NewAdapter()
	if err != nil {
		return fmt.Errorf("create traceroute adapter: %w", err)
	}
	defer adapter.Close()
	engine, err := traceroute.NewEngine(traceCfg, adapter, traceroute.NetResolver{}, nil)
	if err != nil {
		return fmt.Errorf("create traceroute engine: %w", err)
	}
	for _, host := range hosts {
		snapshots, traceErr := engine.Trace(context.Background(), host)
		if traceErr != nil {
			fmt.Printf("trace host=%s failed: %v\n", host, traceErr)
			continue
		}
		for snapshot := range snapshots {
			printTraceSnapshot(snapshot, cycles)
		}
	}
	return nil
}

func runConfig(cfg config.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printHelp() {
	fmt.Println("NNU - Nope Network Utility")
	fmt.Println()
	fmt.Println("Usage: nnu <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  ping [flags] <host|targets.txt>    Ping a host or host list")
	fmt.Println("  trace [flags] <host|targets.txt>    Traceroute to a host or host list")
	fmt.Println("  config          Print configuration")
	fmt.Println("  help            Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  nnu ping 8.8.8.8")
	fmt.Println("  nnu ping -c 10 8.8.8.8")
	fmt.Println("  nnu ping targets.txt")
	fmt.Println("  nnu ping -c 100 targets.txt")
	fmt.Println("  nnu trace google.com")
	fmt.Println("  nnu trace endpoints.txt")
	fmt.Println("  nnu trace -c 100 google.com")
	fmt.Println("  nnu trace -c 10 endpoints.txt")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -c, --cycles <n>    Number of ping/trace cycles (default 1)")
}

func loadTargets(path string) ([]ping.Target, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	targets := make([]ping.Target, 0, 32)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		target := ping.Target{IP: strings.TrimSpace(parts[0])}
		if len(parts) > 1 {
			target.Location = strings.TrimSpace(parts[1])
		}
		targets = append(targets, target)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no valid targets found")
	}
	return targets, nil
}

type pingAggregate struct {
	Sent    int
	Recv    int
	Min     time.Duration
	Max     time.Duration
	Total   time.Duration
	Samples int
	Last    string
}

func parsePingArgs(args []string) (string, int, error) {
	target := ""
	cycles := 1
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "-c" || arg == "--cycles" {
			if i+1 >= len(args) {
				return "", 0, fmt.Errorf("ping: missing value for %s", arg)
			}
			value, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil || value < 1 {
				return "", 0, fmt.Errorf("ping: cycles must be a positive integer")
			}
			cycles = value
			i++
			continue
		}
		if strings.HasPrefix(arg, "-c=") || strings.HasPrefix(arg, "--cycles=") {
			parts := strings.SplitN(arg, "=", 2)
			value, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || value < 1 {
				return "", 0, fmt.Errorf("ping: cycles must be a positive integer")
			}
			cycles = value
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", 0, fmt.Errorf("ping: unknown flag %s", arg)
		}
		if target != "" {
			return "", 0, fmt.Errorf("ping: too many positional arguments")
		}
		target = arg
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 0, fmt.Errorf("ping requires a host or .txt file")
	}
	return target, cycles, nil
}

func loadPingTargets(input string) ([]ping.Target, error) {
	value := strings.TrimSpace(input)
	if strings.HasSuffix(strings.ToLower(value), ".txt") {
		return loadTargets(value)
	}
	return []ping.Target{{IP: value}}, nil
}

func updatePingAggregate(aggregate map[string]*pingAggregate, results []ping.Result) {
	for _, result := range results {
		entry := aggregate[result.Target.IP]
		if entry == nil {
			entry = &pingAggregate{}
			aggregate[result.Target.IP] = entry
		}
		entry.Sent++
		entry.Last = result.Status
		if !result.IsReachable {
			continue
		}
		entry.Recv++
		rtt := result.AvgTime
		entry.Total += rtt
		entry.Samples++
		if entry.Min == 0 || rtt < entry.Min {
			entry.Min = rtt
		}
		if rtt > entry.Max {
			entry.Max = rtt
		}
	}
}

func printPingCycle(cycle int, cycles int, aggregate map[string]*pingAggregate, order []string) {
	fmt.Printf("\nping cycle=%d/%d\n", cycle, cycles)
	fmt.Println("host                          sent  recv  loss%  min    avg    max    last")
	for _, host := range order {
		entry := aggregate[host]
		if entry == nil {
			continue
		}
		name := host
		if len(name) > 28 {
			name = name[:28]
		}
		loss := 0.0
		if entry.Sent > 0 {
			loss = 100 - (100*float64(entry.Recv))/float64(entry.Sent)
		}
		avg := time.Duration(0)
		if entry.Samples > 0 {
			avg = entry.Total / time.Duration(entry.Samples)
		}
		fmt.Printf(
			"%-28s %5d %5d %6.1f %6s %6s %6s %s\n",
			name,
			entry.Sent,
			entry.Recv,
			loss,
			formatDurationMs(entry.Min),
			formatDurationMs(avg),
			formatDurationMs(entry.Max),
			entry.Last,
		)
	}
}

func parseTraceArgs(args []string) (string, int, error) {
	host := ""
	cycles := 1
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "-c" || arg == "--cycles" {
			if i+1 >= len(args) {
				return "", 0, fmt.Errorf("trace: missing value for %s", arg)
			}
			value, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil || value < 1 {
				return "", 0, fmt.Errorf("trace: cycles must be a positive integer")
			}
			cycles = value
			i++
			continue
		}
		if strings.HasPrefix(arg, "-c=") || strings.HasPrefix(arg, "--cycles=") {
			parts := strings.SplitN(arg, "=", 2)
			value, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || value < 1 {
				return "", 0, fmt.Errorf("trace: cycles must be a positive integer")
			}
			cycles = value
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", 0, fmt.Errorf("trace: unknown flag %s", arg)
		}
		if host != "" {
			return "", 0, fmt.Errorf("trace: too many positional arguments")
		}
		host = arg
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0, fmt.Errorf("trace requires a host")
	}
	return host, cycles, nil
}

func loadTraceHosts(input string) ([]string, error) {
	value := strings.TrimSpace(input)
	if strings.HasSuffix(strings.ToLower(value), ".txt") {
		targets, err := loadTargets(value)
		if err != nil {
			return nil, err
		}
		hosts := make([]string, 0, len(targets))
		for _, target := range targets {
			if target.IP == "" {
				continue
			}
			hosts = append(hosts, target.IP)
		}
		if len(hosts) == 0 {
			return nil, fmt.Errorf("no valid hosts found")
		}
		return hosts, nil
	}
	return []string{value}, nil
}

func printTraceSnapshot(snapshot traceroute.Snapshot, cycles int) {
	fmt.Printf(
		"\ntrace host=%s destination=%s pass=%d/%d time=%s\n",
		snapshot.Host,
		snapshot.Destination,
		snapshot.Pass,
		cycles,
		snapshot.Timestamp.Format(time.RFC3339),
	)
	fmt.Println("ttl  host                          sent  recv  loss%  min    avg    max")
	for _, hop := range snapshot.Hops {
		name := hop.Address
		if hop.Name != "" {
			name = hop.Name + " (" + hop.Address + ")"
		}
		if len(name) > 28 {
			name = name[:28]
		}
		fmt.Printf(
			"%-4d %-28s %5d %5d %6.1f %6s %6s %6s\n",
			hop.TTL,
			name,
			hop.Sent,
			hop.Received,
			hop.Loss,
			formatDurationMs(hop.Min),
			formatDurationMs(hop.Avg),
			formatDurationMs(hop.Max),
		)
	}
}

func formatDurationMs(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
