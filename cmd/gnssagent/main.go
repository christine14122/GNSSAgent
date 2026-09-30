package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gnssagent/internal/app"
	"gnssagent/internal/buildinfo"
	"gnssagent/internal/config"
	"gnssagent/internal/observe"
	"gnssagent/internal/server"
	"gnssagent/internal/udpinput"
)

const serviceVersion = "1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "GNSSAgent: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Parse(args, buildinfo.Target)
	if err != nil {
		return err
	}
	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return err
	}

	var (
		writer  io.Writer = os.Stderr
		logFile *observe.RotatingFile
	)
	if cfg.LogFile != "" {
		logFile, err = observe.OpenRotatingFile(cfg.LogFile, cfg.LogMaxBytes)
		if err != nil {
			return fmt.Errorf("open configured log file: %w", err)
		}
		defer logFile.Close()
		writer = logFile
	}
	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level}))
	now := time.Now()
	logDestination := cfg.LogFile
	if logDestination == "" {
		logDestination = "stderr"
	}
	logger.Info("GNSSAgent starting",
		"version", serviceVersion,
		"target", buildinfo.Target,
		"udp_listen", cfg.UDPListenAddress,
		"tcp_listen", cfg.TCPListenAddress,
		"max_connections", cfg.MaxConnections,
		"max_remote_connections", cfg.MaxRemoteConnections,
		"system_unix_ms", now.UnixMilli(),
		"system_utc", now.UTC().Format(time.RFC3339Nano),
		"log_destination", logDestination,
		"log_max_bytes", cfg.LogMaxBytes,
		"time_rms_enter", cfg.TimeQuality.EnterRMS,
		"time_rms_exit", cfg.TimeQuality.ExitRMS,
		"time_rms_spread", cfg.TimeQuality.StableRange,
		"time_confirm_cycles", cfg.TimeQuality.Window,
		"time_exit_cycles", cfg.TimeQuality.ExitSamples,
		"time_gst_timeout", cfg.TimeQuality.Timeout,
		"time_step_tolerance", cfg.TimeQuality.MaxTimeStepError,
		"log_backups", 1)

	stats := observe.NewStats()
	statusServer := server.New(cfg.TCPListenAddress, cfg.MaxConnections, cfg.MaxRemoteConnections)
	statusServer.SetObserver(stats)
	udpManager := udpinput.NewManager(cfg.UDPListenAddress)
	service := app.New(udpManager, statusServer, stats, logger, cfg.TimeQuality)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := service.Run(ctx); err != nil {
		logger.Error("GNSSAgent stopped with error", "error", err)
		return err
	}
	logger.Info("GNSSAgent stopped")
	return nil
}

func parseLogLevel(text string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", text)
	}
}
