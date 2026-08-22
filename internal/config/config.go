package config

import (
	"flag"
	"fmt"
	"io"
	"net/netip"
)

const defaultLogMaxBytes int64 = 8 * 1024 * 1024

type Config struct {
	UDPListenAddress     string
	TCPListenAddress     string
	MaxConnections       int
	MaxRemoteConnections int
	LogLevel             string
	LogFile              string
	LogMaxBytes          int64
}

func Parse(args []string, target string) (Config, error) {
	_ = target

	cfg := Config{
		UDPListenAddress:     "0.0.0.0:29501",
		TCPListenAddress:     "0.0.0.0:29501",
		MaxConnections:       5,
		MaxRemoteConnections: 4,
		LogLevel:             "info",
		LogMaxBytes:          defaultLogMaxBytes,
	}

	fs := flag.NewFlagSet("gnssagent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.UDPListenAddress, "udp-listen", cfg.UDPListenAddress, "IPv4 UDP address for raw NMEA input")
	fs.StringVar(&cfg.TCPListenAddress, "tcp-listen", cfg.TCPListenAddress, "TCP address for GNSS status subscribers")
	fs.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections, "maximum total TCP connections")
	fs.IntVar(&cfg.MaxRemoteConnections, "max-remote-connections", cfg.MaxRemoteConnections, "maximum non-loopback TCP connections")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level")
	fs.StringVar(&cfg.LogFile, "log-file", cfg.LogFile, "rotating log file; empty writes to stderr")
	fs.Int64Var(&cfg.LogMaxBytes, "log-max-bytes", cfg.LogMaxBytes, "maximum active log size before rotation")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %q", fs.Args())
	}

	udpAddress, err := netip.ParseAddrPort(cfg.UDPListenAddress)
	if err != nil || !udpAddress.Addr().Is4() || udpAddress.Port() == 0 {
		return Config{}, fmt.Errorf("udp-listen must be a non-zero IPv4 address: %q", cfg.UDPListenAddress)
	}
	if cfg.MaxConnections <= 0 {
		return Config{}, fmt.Errorf("max-connections must be greater than zero")
	}
	if cfg.MaxRemoteConnections < 0 || cfg.MaxRemoteConnections >= cfg.MaxConnections {
		return Config{}, fmt.Errorf("max-remote-connections must be non-negative and less than max-connections")
	}
	if cfg.LogMaxBytes <= 0 {
		return Config{}, fmt.Errorf("log-max-bytes must be greater than zero")
	}

	return cfg, nil
}
