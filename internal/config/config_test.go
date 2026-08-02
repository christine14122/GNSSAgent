package config

import "testing"

func requireError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q", want)
	}
	if err.Error() != want {
		t.Fatalf("unexpected error: got %q, want %q", err, want)
	}
}

func TestParseMultibandDefaults(t *testing.T) {
	cfg, err := Parse(nil, "multiband-radio")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SerialDevice != "/dev/ttyUL4" || cfg.Baud != 9600 {
		t.Fatalf("unexpected serial defaults: %+v", cfg)
	}
	if cfg.ListenAddress != "0.0.0.0:29501" {
		t.Fatalf("unexpected listen address: %s", cfg.ListenAddress)
	}
	if cfg.MaxConnections != 5 || cfg.MaxRemoteConnections != 4 {
		t.Fatalf("unexpected limits: %+v", cfg)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("unexpected log level: %s", cfg.LogLevel)
	}
}

func TestParseUnknownTargetRequiresSerial(t *testing.T) {
	_, err := Parse(nil, "hf")
	requireError(t, err, "serial device is required for this target")
}

func TestParseOverridesUnknownTarget(t *testing.T) {
	cfg, err := Parse([]string{"--serial", "/dev/ttyS2", "--baud", "19200"}, "hf")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SerialDevice != "/dev/ttyS2" || cfg.Baud != 19200 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseFlagOverrides(t *testing.T) {
	defaults := Config{
		SerialDevice:         "/dev/ttyUL4",
		Baud:                 9600,
		ListenAddress:        "0.0.0.0:29501",
		MaxConnections:       5,
		MaxRemoteConnections: 4,
		LogLevel:             "info",
	}

	serial := defaults
	serial.SerialDevice = "/dev/ttyS2"
	baud := defaults
	baud.Baud = 19200
	listen := defaults
	listen.ListenAddress = "127.0.0.1:12345"
	maxConnections := defaults
	maxConnections.MaxConnections = 6
	maxRemoteConnections := defaults
	maxRemoteConnections.MaxRemoteConnections = 3
	logLevel := defaults
	logLevel.LogLevel = "debug"

	tests := []struct {
		name   string
		args   []string
		target string
		want   Config
	}{
		{name: "serial", args: []string{"--serial", "/dev/ttyS2"}, target: "hf", want: serial},
		{name: "baud", args: []string{"--baud", "19200"}, target: "multiband-radio", want: baud},
		{name: "listen", args: []string{"--listen", "127.0.0.1:12345"}, target: "multiband-radio", want: listen},
		{name: "max total", args: []string{"--max-connections", "6"}, target: "multiband-radio", want: maxConnections},
		{name: "max remote", args: []string{"--max-remote-connections", "3"}, target: "multiband-radio", want: maxRemoteConnections},
		{name: "log level", args: []string{"--log-level", "debug"}, target: "multiband-radio", want: logLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.args, tt.target)
			if err != nil {
				t.Fatal(err)
			}
			if cfg != tt.want {
				t.Fatalf("unexpected config: got %+v, want %+v", cfg, tt.want)
			}
		})
	}
}

func TestParseRejectsPositionalArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "before flags retains ignored flags in error",
			args:    []string{"typo", "--baud", "19200"},
			wantErr: "unexpected positional arguments: [typo --baud 19200]",
		},
		{
			name:    "after valid flags",
			args:    []string{"--baud", "19200", "typo"},
			wantErr: "unexpected positional arguments: [typo]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args, "multiband-radio")
			requireError(t, err, tt.wantErr)
		})
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "zero baud", args: []string{"--baud", "0"}, wantErr: "baud must be positive: 0"},
		{name: "negative baud", args: []string{"--baud", "-1"}, wantErr: "baud must be positive: -1"},
		{name: "zero max total", args: []string{"--max-connections", "0"}, wantErr: "max-connections must be positive"},
		{name: "negative max remote", args: []string{"--max-remote-connections", "-1"}, wantErr: "max-remote-connections must be non-negative"},
		{name: "max remote equals total", args: []string{"--max-connections", "5", "--max-remote-connections", "5"}, wantErr: "max-remote-connections must leave at least one loopback slot"},
		{name: "max remote exceeds total", args: []string{"--max-connections", "5", "--max-remote-connections", "6"}, wantErr: "max-remote-connections must leave at least one loopback slot"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args, "multiband-radio")
			requireError(t, err, tt.wantErr)
		})
	}
}

func TestParseAcceptsBoundaryValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Config
	}{
		{
			name: "minimum positive baud and total with zero remote",
			args: []string{"--baud", "1", "--max-connections", "1", "--max-remote-connections", "0"},
			want: Config{
				SerialDevice:         "/dev/ttyUL4",
				Baud:                 1,
				ListenAddress:        "0.0.0.0:29501",
				MaxConnections:       1,
				MaxRemoteConnections: 0,
				LogLevel:             "info",
			},
		},
		{
			name: "remote one below total",
			args: []string{"--max-connections", "2", "--max-remote-connections", "1"},
			want: Config{
				SerialDevice:         "/dev/ttyUL4",
				Baud:                 9600,
				ListenAddress:        "0.0.0.0:29501",
				MaxConnections:       2,
				MaxRemoteConnections: 1,
				LogLevel:             "info",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.args, "multiband-radio")
			if err != nil {
				t.Fatal(err)
			}
			if cfg != tt.want {
				t.Fatalf("unexpected config: got %+v, want %+v", cfg, tt.want)
			}
		})
	}
}
