package config

import "testing"

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
}

func TestParseUnknownTargetRequiresSerial(t *testing.T) {
	_, err := Parse(nil, "hf")
	if err == nil {
		t.Fatal("expected missing serial device error")
	}
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

func TestParseRejectsLimitsWithoutLoopbackReservation(t *testing.T) {
	_, err := Parse([]string{"--max-connections", "5", "--max-remote-connections", "5"}, "multiband-radio")
	if err == nil {
		t.Fatal("expected remote limit validation error")
	}
}
