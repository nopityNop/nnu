# NNU - Nope Network Utility

A unified network diagnostic tool combining ping and traceroute functionality with a simple CLI.

## Features

- **Ping Command**: Ping a single host or `.txt` target list, with optional `-c/--cycles`
- **Traceroute Command**: Trace a single host or `.txt` target list, with optional `-c/--cycles`

## Installation

```bash
go install github.com/nopityNop/nnu/cmd/nnu@latest
```

Or build from source:

```bash
git clone https://github.com/nopityNop/nnu.git
cd nnu
go build -o nnu.exe ./cmd/nnu
```

## Usage

```bash
# Ping targets from file
nnu ping targets.txt

# Single trace
nnu trace google.com

# Multi-trace (100 cumulative cycles)
nnu trace -c 100 google.com

# View active defaults
nnu config
```

## Configuration

Current defaults:

```json
{
  "ping": {
    "count": 1,
    "timeout_ms": 1000,
    "delay_ms": 100,
    "max_concurrent": 10,
    "consecutive_timeouts": 3
  },
  "traceroute": {
    "max_hops": 30,
    "interval_ms": 1000,
    "timeout_ms": 5000,
    "probes": 1,
    "use_dns": true
  }
}
```

`trace -c <n>` runs `n` traceroute cycles and prints cumulative hop stats each cycle.
`ping -c <n>` runs `n` ping cycles and prints cumulative host stats each cycle.

## License

MIT
