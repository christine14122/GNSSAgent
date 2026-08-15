package config

import (
	"fmt"
	"reflect"
	"testing"
)

func requireError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestParseDefaultsAreTargetIndependent(t *testing.T) {
	want := Config{
		UDPListenAddress:     "127.0.0.1:29501",
		TCPListenAddress:     "0.0.0.0:29501",
		MaxConnections:       5,
		MaxRemoteConnections: 4,
		LogLevel:             "info",
		LogFile:              "",
		LogMaxBytes:          8 * 1024 * 1024,
	}

	for _, target := range []string{
		"multiband-radio",
		"ccu",
		"hf-radio",
		"unknown-target",
	} {
		t.Run(target, func(t *testing.T) {
			got, err := Parse(nil, target)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Parse() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestParseUDPListenRequiresNonZeroIPv4Loopback(t *testing.T) {
	for _, address := range []string{
		"0.0.0.0:29501",
		"192.168.7.2:29501",
		"[::1]:29501",
		"localhost:29501",
		"127.0.0.1:0",
		"bad-address",
	} {
		t.Run(address, func(t *testing.T) {
			_, err := Parse([]string{"--udp-listen", address}, "multiband-radio")
			requireError(t, err, fmt.Sprintf("udp-listen must be a non-zero IPv4 loopback address: %q", address))
		})
	}
}

func TestParseAcceptsIPv4LoopbackRange(t *testing.T) {
	got, err := Parse([]string{
		"--udp-listen", "127.10.20.30:40000",
		"--tcp-listen", "192.168.7.2:30000",
	}, "ccu")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.UDPListenAddress != "127.10.20.30:40000" {
		t.Fatalf("UDPListenAddress = %q", got.UDPListenAddress)
	}
	if got.TCPListenAddress != "192.168.7.2:30000" {
		t.Fatalf("TCPListenAddress = %q", got.TCPListenAddress)
	}
}

func TestSerialFlagsNoLongerExist(t *testing.T) {
	for _, args := range [][]string{
		{"--serial", "/dev/ttyUL4"},
		{"--baud", "9600"},
	} {
		if _, err := Parse(args, "multiband-radio"); err == nil {
			t.Fatalf("Parse(%v) unexpectedly succeeded", args)
		}
	}
}

func TestParseFlagOverrides(t *testing.T) {
	got, err := Parse([]string{
		"--udp-listen", "127.0.0.2:31001",
		"--tcp-listen", "127.0.0.1:31002",
		"--max-connections", "7",
		"--max-remote-connections", "3",
		"--log-level", "debug",
		"--log-file", "/tmp/gnssagent.log",
		"--log-max-bytes", "4096",
	}, "hf-radio")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Config{
		UDPListenAddress:     "127.0.0.2:31001",
		TCPListenAddress:     "127.0.0.1:31002",
		MaxConnections:       7,
		MaxRemoteConnections: 3,
		LogLevel:             "debug",
		LogFile:              "/tmp/gnssagent.log",
		LogMaxBytes:          4096,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseRejectsPositionalArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "single", args: []string{"extra"}, want: `unexpected positional arguments: ["extra"]`},
		{name: "after flag", args: []string{"--log-level", "debug", "extra"}, want: `unexpected positional arguments: ["extra"]`},
		{name: "multiple", args: []string{"one", "two"}, want: `unexpected positional arguments: ["one" "two"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args, "multiband-radio")
			requireError(t, err, tt.want)
		})
	}
}

func TestParseRejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "zero total", args: []string{"--max-connections", "0"}, want: "max-connections must be greater than zero"},
		{name: "negative total", args: []string{"--max-connections", "-1"}, want: "max-connections must be greater than zero"},
		{name: "negative remote", args: []string{"--max-remote-connections", "-1"}, want: "max-remote-connections must be non-negative and less than max-connections"},
		{name: "remote equals total", args: []string{"--max-connections", "5", "--max-remote-connections", "5"}, want: "max-remote-connections must be non-negative and less than max-connections"},
		{name: "remote exceeds total", args: []string{"--max-connections", "5", "--max-remote-connections", "6"}, want: "max-remote-connections must be non-negative and less than max-connections"},
		{name: "zero log max", args: []string{"--log-max-bytes", "0"}, want: "log-max-bytes must be greater than zero"},
		{name: "negative log max", args: []string{"--log-max-bytes", "-1"}, want: "log-max-bytes must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args, "multiband-radio")
			requireError(t, err, tt.want)
		})
	}
}

func TestParseAcceptsBoundaryConnectionLimits(t *testing.T) {
	for _, args := range [][]string{
		{"--max-connections", "1", "--max-remote-connections", "0"},
		{"--max-connections", "2", "--max-remote-connections", "1"},
	} {
		if _, err := Parse(args, "unknown-target"); err != nil {
			t.Fatalf("Parse(%v) error = %v", args, err)
		}
	}
}
