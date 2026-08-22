package main

import (
	"log/slog"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	for _, tc := range []struct {
		text string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, err := parseLogLevel(tc.text)
			if err != nil || got != tc.want {
				t.Fatalf("parseLogLevel(%q) = (%v, %v), want %v", tc.text, got, err, tc.want)
			}
		})
	}
	if _, err := parseLogLevel("verbose"); err == nil {
		t.Fatal("unsupported level accepted")
	}
}

func TestRunRejectsInvalidConfigurationBeforeStarting(t *testing.T) {
	if err := run([]string{"--udp-listen", "[::1]:29501"}); err == nil {
		t.Fatal("invalid UDP listener accepted")
	}
	if err := run([]string{"--log-level", "verbose"}); err == nil {
		t.Fatal("invalid log level accepted")
	}
}
