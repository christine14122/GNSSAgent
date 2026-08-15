package observe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const MaxLogRecordSize = 4096

type RotatingFile struct {
	mu          sync.Mutex
	path        string
	maxBytes    int64
	size        int64
	file        *os.File
	terminalErr error
}

func OpenRotatingFile(path string, maxBytes int64) (*RotatingFile, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("log path must not be empty")
	}
	if maxBytes <= 0 {
		return nil, errors.New("log max bytes must be greater than zero")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := openActiveLog(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat active log: %w", err)
	}
	return &RotatingFile{
		path:     path,
		maxBytes: maxBytes,
		size:     info.Size(),
		file:     file,
	}, nil
}

func openActiveLog(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open active log: %w", err)
	}
	return file, nil
}

func (w *RotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminalErr != nil {
		return 0, w.terminalErr
	}
	if w.file == nil {
		return 0, errors.New("log file is closed")
	}
	if len(p) > MaxLogRecordSize {
		return 0, fmt.Errorf("log record is %d bytes, maximum is %d", len(p), MaxLogRecordSize)
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	if err != nil {
		return n, w.failLocked(fmt.Errorf("write active log: %w", err))
	}
	return n, nil
}

func (w *RotatingFile) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		w.file = nil
		return w.failLocked(fmt.Errorf("close active log for rotation: %w", err))
	}
	w.file = nil

	backup := w.path + ".1"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return w.failLocked(fmt.Errorf("remove prior log backup: %w", err))
	}
	if err := os.Rename(w.path, backup); err != nil {
		return w.failLocked(fmt.Errorf("rename active log to backup: %w", err))
	}
	file, err := openActiveLog(w.path)
	if err != nil {
		return w.failLocked(err)
	}
	w.file = file
	w.size = 0
	return nil
}

func (w *RotatingFile) failLocked(err error) error {
	if w.terminalErr == nil {
		w.terminalErr = err
		message := err.Error()
		if len(message) > 512 {
			message = message[:512]
		}
		_, _ = fmt.Fprintf(os.Stderr, "gnssagent: persistent log disabled after terminal error: %s\n", message)
	}
	return w.terminalErr
}

func (w *RotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return w.terminalErr
	}
	err := w.file.Close()
	w.file = nil
	if err != nil {
		return fmt.Errorf("close active log: %w", err)
	}
	return nil
}
