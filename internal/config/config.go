package config

import (
	"errors"
	"flag"
	"fmt"
)

type Config struct {
	SerialDevice         string
	Baud                 int
	ListenAddress        string
	MaxConnections       int
	MaxRemoteConnections int
	LogLevel             string
}

func Parse(args []string, target string) (Config, error) {
	cfg := Config{
		Baud:                 9600,
		ListenAddress:        "0.0.0.0:29501",
		MaxConnections:       5,
		MaxRemoteConnections: 4,
		LogLevel:             "info",
	}
	if target == "multiband-radio" {
		cfg.SerialDevice = "/dev/ttyUL4"
	}

	fs := flag.NewFlagSet("gnssagent", flag.ContinueOnError)
	fs.StringVar(&cfg.SerialDevice, "serial", cfg.SerialDevice, "GNSS UART device")
	fs.IntVar(&cfg.Baud, "baud", cfg.Baud, "GNSS UART baud")
	fs.StringVar(&cfg.ListenAddress, "listen", cfg.ListenAddress, "TCP listen address")
	fs.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections, "maximum total TCP connections")
	fs.IntVar(&cfg.MaxRemoteConnections, "max-remote-connections", cfg.MaxRemoteConnections, "maximum non-loopback TCP connections")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug, info, warn, or error")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if cfg.SerialDevice == "" {
		return Config{}, errors.New("serial device is required for this target")
	}
	if cfg.Baud <= 0 {
		return Config{}, fmt.Errorf("baud must be positive: %d", cfg.Baud)
	}
	if cfg.MaxConnections < 1 {
		return Config{}, errors.New("max-connections must be positive")
	}
	if cfg.MaxRemoteConnections < 0 || cfg.MaxRemoteConnections >= cfg.MaxConnections {
		return Config{}, errors.New("max-remote-connections must leave at least one loopback slot")
	}
	return cfg, nil
}
