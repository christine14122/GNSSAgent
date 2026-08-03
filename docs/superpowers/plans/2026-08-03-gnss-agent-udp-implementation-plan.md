# GNSSAgent UDP Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete GNSSAgent as a Go service that receives one raw NMEA sentence per local UDP datagram, preserves the completed parser/aggregator/status protocol baseline, and publishes SIMPLE/FULL status over bounded TCP subscriptions.

**Architecture:** The existing `internal/nmea`, `internal/aggregate`, `internal/model`, and `internal/protocol` packages remain the immutable data path from a validated NMEA sentence to a v1 status frame. A new loopback-only UDP input manager owns datagram validation, socket recovery, receive-buffer/drop telemetry, and delivery timestamps; the application composes it with the existing aggregator and a subscription-only TCP server. GNSS UART ownership, serial configuration, GNSS commands, GPIO, and control acknowledgements remain entirely outside GNSSAgent.

**Tech Stack:** Go 1.23 language baseline; Go 1.25.5 and 1.23.12 build toolchains; standard library `net`, `net/netip`, `log/slog`, and `context`; `golang.org/x/sys/unix` only for Linux socket options/ancillary data and `/proc` socket correlation; IPv4 UDP; TCP; SysV init; PowerShell build/contract tests.

---

## Authority, supersession, and frozen baseline

This plan supersedes the unfinished portion of `docs/superpowers/plans/2026-08-02-gnss-agent-implementation-plan.md`. Do not execute its Tasks 7–13.

Use these documents in this order:

1. `docs/superpowers/specs/2026-08-03-gnss-agent-udp-requirements-design.md` for the UDP architecture and remaining service behavior.
2. `docs/protocol/GNSSAgent-UDP-NMEA-Protocol-v1.md` for the local UDP datagram contract.
3. `docs/protocol/GNSSAgent-Binary-Protocol-v1.md` only for `SUBSCRIBE_REQUEST`, `SUBSCRIBE_ACK`, `GNSS_STATUS_SIMPLE`, `GNSS_STATUS_FULL`, the common frame header, and stream resynchronization. Its switch request/ACK sections are not GNSSAgent capabilities in this architecture.
4. `docs/superpowers/specs/2026-08-02-gnss-agent-requirements-design.md` v1.2 §8.2 for the complete aggregation algorithms.

The UDP requirements §9.4 and §9.5 summarize, but do not replace, the 2026-08-02 v1.2 §8.2 rules. In particular, preserve:

- decimal DOP comparison by padding to three fractional digits, using the fourth digit for half-up, and carrying into the integer part;
- the `0.4895 -> 490`, `0.5005 -> 501`, and carry behavior;
- GSA System ID/talker/unique-complete-GSV identity priority;
- unresolved PRN collision and known/unknown raw-PRN ambiguity handling;
- complete GSV generation, Signal ID, BD/GB alias, and C/N0 reconciliation behavior;
- completed-second duplicate/stale protection and long-outage recovery.

### Completed Tasks 1–6 — do not reimplement

| Original task | Completed capability | Status |
|---:|---|---|
| 1 | Go module, build target metadata, initial configuration package | Complete |
| 2 | FULL/SIMPLE model and independent validity masks | Complete |
| 3 | NMEA framing, XOR checksum, lexical milli-decimal conversion | Complete |
| 4 | RMC/GGA/GSA/GSV/GST parser and fixtures | Complete |
| 5 | UTC-cycle aggregation, DOP, satellite identity, GSV/CN0 rules | Complete |
| 6 | Exact v1 frame/status protocol and bounded decoder | Complete |

The following directories are frozen for this plan. Do not edit source or tests in them:

```text
internal/model/
internal/nmea/
internal/aggregate/
internal/protocol/
internal/buildinfo/
```

`internal/protocol` intentionally still knows the numeric `0x10`/`0x11` frame layouts from the original v1 package. Do not delete or refactor them. The TCP session layer added by this plan must silently skip `0x10` and must never call the switch parser, send `0x11`, or create side effects.

Before and after every remaining task, run:

```powershell
go test -count=1 ./internal/model ./internal/nmea ./internal/aggregate ./internal/protocol ./internal/buildinfo
```

Expected: all existing tests pass unchanged. The buildinfo package reports no test files.

## Planned file structure

```text
internal/config/config.go
internal/config/config_test.go
internal/udpinput/datagram.go
internal/udpinput/datagram_test.go
internal/udpinput/backoff.go
internal/udpinput/backoff_test.go
internal/udpinput/socket.go
internal/udpinput/socket_linux.go
internal/udpinput/socket_other.go
internal/udpinput/proc.go
internal/udpinput/proc_linux.go
internal/udpinput/manager.go
internal/udpinput/manager_test.go
internal/server/limits.go
internal/server/hub.go
internal/server/session.go
internal/server/server.go
internal/server/server_test.go
internal/observe/stats.go
internal/observe/stats_test.go
internal/app/app.go
internal/app/app_test.go
cmd/gnssagent/main.go
build/scripts/build.ps1
build/scripts/build-hf.ps1
build/scripts/build.bat
tests/build.test.ps1
deploy/default/gnssagent
deploy/init.d/gnssagent
tests/deploy.test.ps1
tests/device/smoke.sh
tests/protocol_doc.test.ps1
tests/architecture.test.ps1
tests/regression/udp_baseline_test.go
```

Do not create `internal/serial`, `internal/control`, a UART endpoint, termios helpers, link-occupancy trackers, ICount readers, or a link-budget capture script.

---

### Task 7A: Revise configuration for loopback UDP input

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Replace serial-default tests with common UDP/TCP defaults**

Rewrite the configuration tests around this expected shape:

```go
func TestParseDefaultsAreTargetIndependent(t *testing.T) {
	targets := []string{"ccu", "hf", "multiband-radio", "multiband-handheld", "unknown"}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			cfg, err := Parse(nil, target)
			if err != nil {
				t.Fatal(err)
			}
			want := Config{
				UDPListenAddress:     "127.0.0.1:29501",
				TCPListenAddress:     "0.0.0.0:29501",
				MaxConnections:       5,
				MaxRemoteConnections: 4,
				LogLevel:             "info",
			}
			if cfg != want {
				t.Fatalf("got %+v want %+v", cfg, want)
			}
		})
	}
}

func TestParseRejectsNonLoopbackUDPListen(t *testing.T) {
	for _, value := range []string{
		"0.0.0.0:29501",
		"192.168.7.2:29501",
		"[::1]:29501",
		"localhost:29501",
		"127.0.0.1:0",
		"bad-address",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse([]string{"--udp-listen", value}, "ccu")
			if err == nil {
				t.Fatal("expected loopback UDP validation error")
			}
		})
	}
}

func TestParseAcceptsIPv4LoopbackRange(t *testing.T) {
	cfg, err := Parse([]string{
		"--udp-listen", "127.10.20.30:40000",
		"--tcp-listen", "127.0.0.1:41000",
	}, "hf")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UDPListenAddress != "127.10.20.30:40000" || cfg.TCPListenAddress != "127.0.0.1:41000" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestSerialFlagsNoLongerExist(t *testing.T) {
	for _, flag := range []string{"--serial", "--baud"} {
		if _, err := Parse([]string{flag, "value"}, "multiband-radio"); err == nil {
			t.Fatalf("%s must be rejected", flag)
		}
	}
}
```

Keep the existing exact tests for positional arguments and TCP connection-limit boundaries, but update their expected `Config` values and replace `--listen` with `--tcp-listen`.

- [ ] **Step 2: Run tests and verify the intended RED state**

```powershell
go test -count=1 ./internal/config -v
```

Expected: tests fail because `UDPListenAddress` and `TCPListenAddress` do not exist, serial fields still exist, and unknown targets still require a serial path.

- [ ] **Step 3: Implement the exact UDP-oriented configuration**

Replace `config.go` with:

```go
package config

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
)

type Config struct {
	UDPListenAddress     string
	TCPListenAddress     string
	MaxConnections       int
	MaxRemoteConnections int
	LogLevel             string
}

func Parse(args []string, target string) (Config, error) {
	_ = target // build target no longer changes network defaults
	cfg := Config{
		UDPListenAddress:     "127.0.0.1:29501",
		TCPListenAddress:     "0.0.0.0:29501",
		MaxConnections:       5,
		MaxRemoteConnections: 4,
		LogLevel:             "info",
	}

	fs := flag.NewFlagSet("gnssagent", flag.ContinueOnError)
	fs.StringVar(&cfg.UDPListenAddress, "udp-listen", cfg.UDPListenAddress, "loopback IPv4 UDP NMEA listen address")
	fs.StringVar(&cfg.TCPListenAddress, "tcp-listen", cfg.TCPListenAddress, "TCP status listen address")
	fs.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections, "maximum total TCP connections")
	fs.IntVar(&cfg.MaxRemoteConnections, "max-remote-connections", cfg.MaxRemoteConnections, "maximum non-loopback TCP connections")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug, info, warn, or error")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	address, err := netip.ParseAddrPort(cfg.UDPListenAddress)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsLoopback() || address.Port() == 0 {
		return Config{}, fmt.Errorf("udp-listen must be a non-zero IPv4 loopback address: %q", cfg.UDPListenAddress)
	}
	if cfg.MaxConnections < 1 {
		return Config{}, errors.New("max-connections must be positive")
	}
	if cfg.MaxRemoteConnections < 0 {
		return Config{}, errors.New("max-remote-connections must be non-negative")
	}
	if cfg.MaxRemoteConnections >= cfg.MaxConnections {
		return Config{}, errors.New("max-remote-connections must leave at least one loopback slot")
	}
	return cfg, nil
}
```

Do not retain compatibility aliases for `--serial`, `--baud`, or `--listen`; silently accepting obsolete UART arguments would conceal deployment mistakes.

- [ ] **Step 4: Verify configuration and the frozen baseline**

```powershell
gofmt -w internal/config/config.go internal/config/config_test.go
go test -count=1 ./internal/config -v
go test -count=1 ./internal/model ./internal/nmea ./internal/aggregate ./internal/protocol
go vet ./internal/config
git diff --check
```

Expected: configuration and frozen-package tests pass; vet and diff checks are silent.

- [ ] **Step 5: Commit the configuration revision**

```powershell
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor: configure GNSS UDP input"
```

---

### Task 7B: Replace UART access with validated UDP datagram input

This task replaces the old serial Task 7. It must not create or use termios, `TIOCEXCL`, `TIOCGICOUNT`, UART occupancy tracking, serial reconnection, or a UART writer.

**Files:**
- Create: `internal/udpinput/datagram.go`
- Create: `internal/udpinput/datagram_test.go`
- Create: `internal/udpinput/backoff.go`
- Create: `internal/udpinput/backoff_test.go`
- Create: `internal/udpinput/socket.go`
- Create: `internal/udpinput/socket_linux.go`
- Create: `internal/udpinput/socket_other.go`
- Create: `internal/udpinput/proc.go`
- Create: `internal/udpinput/proc_linux.go`
- Create: `internal/udpinput/manager.go`
- Create: `internal/udpinput/manager_test.go`

- [ ] **Step 1: Write failing UDP datagram-boundary tests**

Define same-package tests for this contract:

```go
func TestNormalizeDatagram(t *testing.T) {
	valid := []struct {
		name string
		data []byte
		want string
	}{
		{"no terminator", []byte("$GPGSV,1,1,00*79"), "$GPGSV,1,1,00*79"},
		{"LF", []byte("$GPGSV,1,1,00*79\n"), "$GPGSV,1,1,00*79"},
		{"CRLF", []byte("$GPGSV,1,1,00*79\r\n"), "$GPGSV,1,1,00*79"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := NormalizeDatagram(tc.data, len(tc.data), false)
			if reason != RejectNone || string(got) != tc.want {
				t.Fatalf("got %q reason=%v", got, reason)
			}
		})
	}
}

func TestNormalizeDatagramRejectsInvalidBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		n         int
		truncated bool
		reason    RejectReason
	}{
		{"empty", nil, 0, false, RejectEmpty},
		{"not dollar", []byte("GPGSV*00"), len("GPGSV*00"), false, RejectStart},
		{"tail NUL", []byte{'$', 'X', '*', '0', '0', 0}, 6, false, RejectNUL},
		{"multiple LF sentences", []byte("$A*00\n$B*00"), 11, false, RejectMultiple},
		{"multiple dollar sentences", []byte("$A*00$B*00"), 10, false, RejectMultiple},
		{"truncated", []byte("$A*00"), 5, true, RejectTruncated},
		{"overlong", make([]byte, MaxDatagramSize+1), MaxDatagramSize + 1, false, RejectTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := NormalizeDatagram(tc.data, tc.n, tc.truncated); got != tc.reason {
				t.Fatalf("reason=%v want=%v", got, tc.reason)
			}
		})
	}
}

func TestNormalizeUsesOnlyReturnedLength(t *testing.T) {
	buf := append([]byte("$GPGSV,1,1,00*79"), []byte("\x00$STALE*00")...)
	got, reason := NormalizeDatagram(buf, len("$GPGSV,1,1,00*79"), false)
	if reason != RejectNone || string(got) != "$GPGSV,1,1,00*79" {
		t.Fatalf("got %q reason=%v", got, reason)
	}
}
```

Also prove that two separate calls containing two halves of a sentence are rejected independently and are never concatenated.

- [ ] **Step 2: Implement datagram normalization without a stream framer**

Create these exact constants/types:

```go
const (
	MaxDatagramSize = 1024
	ReadBufferSize  = 256 * 1024
)

type RejectReason uint8

const (
	RejectNone RejectReason = iota
	RejectEmpty
	RejectStart
	RejectTooLong
	RejectTruncated
	RejectNUL
	RejectMultiple
	RejectTerminator
)
```

`NormalizeDatagram(buffer, n, truncated)` must:

1. use only `buffer[:n]` and reject `n < 0`, `n > len(buffer)`, `n == 0`, `n > 1024`, or `truncated`;
2. require byte zero to be `$`;
3. reject any NUL and more than one `$`;
4. remove at most one terminal LF or one terminal CRLF;
5. reject every remaining CR/LF;
6. return an owned copy so the receive buffer can be reused.

Do not call `nmea.Framer`; UDP datagrams are independent records.

- [ ] **Step 3: Write and implement exact retry-backoff tests**

```go
func TestBackoffSequenceAndReset(t *testing.T) {
	b := newBackoff()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, duration := range want {
		if got := b.Next(); got != duration {
			t.Fatalf("step %d got %v want %v", i, got, duration)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after reset got %v", got)
	}
}
```

Implement `backoff` with the fixed sequence above. It contains no random jitter because the service has one local UDP listener, while the increasing delay already prevents init restart/log storms.

- [ ] **Step 4: Define a testable socket boundary and drop telemetry**

Use a platform-neutral interface:

```go
type DropSource uint8

const (
	DropUnavailable DropSource = iota
	DropRXQOverflow
	DropProcUDP
)

type SocketInfo struct {
	RequestedReadBuffer int
	ActualReadBuffer    int
	DropSource          DropSource
}

type ReadResult struct {
	N             int
	Source        netip.AddrPort
	Truncated     bool
	RXQOverflow   *uint32
	ReceivedAt    time.Time
}

type packetSocket interface {
	Read([]byte) (ReadResult, error)
	ProcDrops() (uint64, bool)
	Info() SocketInfo
	Close() error
}

type socketFactory interface {
	Listen(address string) (packetSocket, error)
}
```

The production Linux factory must call:

```go
packetConn, err := net.ListenPacket("udp4", address)
udpConn, ok := packetConn.(*net.UDPConn)
```

It must fail and close the socket if the returned object is not `*net.UDPConn`. It then:

- calls `SetReadBuffer(ReadBufferSize)`;
- reads actual `SO_RCVBUF` with `unix.GetsockoptInt`;
- enables `SO_RXQ_OVFL` with `unix.SetsockoptInt`;
- records the socket inode with `unix.Fstat` for `/proc/net/udp` fallback;
- uses `ReadMsgUDP` with a `MaxDatagramSize+1` payload buffer and OOB buffer;
- sets `Truncated` from `MSG_TRUNC` and returns `N` exactly as reported;
- parses `SO_RXQ_OVFL` control messages as native-endian uint32 without `unsafe`;
- records `ReceivedAt = time.Now()` immediately after the read returns.

`ReadMsgUDP` is used on Linux because truncation flags and `SO_RXQ_OVFL` ancillary data are only available through the `recvmsg` path. Its returned `N` is the socket-returned datagram length required by the UDP contract and is the sole length passed to normalization; no byte in the payload is interpreted as an application-layer length.

`socket_other.go` must compile host-side tests and use `net.ListenPacket`/`ReadFrom` with the same `MaxDatagramSize+1` payload rule, while reporting `DropUnavailable`. It is not a target implementation.

- [ ] **Step 5: Implement and test `/proc/net/udp` fallback parsing**

Put the text parser in `proc.go` so it is unit-testable on every host:

```go
func parseProcUDPDrops(text string, inode uint64) (uint64, bool)
```

Tests must include a realistic header, unrelated sockets, the matching inode, malformed lines, and a kernel layout without a `drops` column. Match by inode, not merely by local port. `proc_linux.go` reads `/proc/net/udp` only when `SO_RXQ_OVFL` could not be enabled.

- [ ] **Step 6: Write failing manager recovery and delivery tests**

Use fake factories/sockets and an injectable clock/sleeper to prove:

- bind failures sleep 1, 2, 4, 8, 16, 30, 30 seconds without returning from `Run`;
- successful bind resets backoff;
- a fatal read error calls `Sink.Reset()`, closes the socket, and rebinds;
- context cancellation closes the current socket and exits without retry;
- non-loopback source addresses are rejected even though the listener is loopback-bound;
- empty, overlong, truncated, multi-sentence, NUL, bad-checksum, and unsupported NMEA do not reach the sink;
- valid no-newline/LF/CRLF inputs reach `nmea.Parse` as one sentence with `ReceivedAt` from the socket read;
- split datagrams are never joined;
- an RXQ overflow counter increment produces a kernel-drop delta, including uint32 wrap;
- `/proc` fallback and fully-unobservable states are surfaced distinctly.
- a socket whose actual `SO_RCVBUF` is below 256 KiB remains usable and surfaces enough `SocketInfo` for the app to emit the required warning.

Use this sink contract:

```go
type Sink interface {
	Sentence(nmea.Sentence)
	Reset()
	DatagramRejected(RejectReason)
	NMEARejected(checksum bool)
	KernelDrops(delta uint64, source DropSource)
	SocketReady(SocketInfo)
}
```

- [ ] **Step 7: Implement the retrying manager**

`Manager.Run(ctx, sink)` must own one socket at a time. It validates datagram framing, calls `nmea.ValidateChecksum`, then calls `nmea.Parse` with the socket read timestamp. It never uses an application-layer length field, never joins datagrams, never calls the frozen stream framer, and never exits merely because bind/read failed.

On a fatal read error:

1. close the socket;
2. call `sink.Reset()` so the app drops the incomplete aggregate cycle;
3. retry the bind with the same sequence.

Use a context-triggered close to unblock `Read` during shutdown. Bind/read failures may log at each backoff step; the backoff caps them at one message per 30 seconds.

- [ ] **Step 8: Verify host tests, Linux compilation, and module dependencies**

```powershell
gofmt -w internal/udpinput
go test -count=1 ./internal/udpinput -v
go test -count=1 ./...
go vet ./...
$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
try {
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    go test -c ./internal/udpinput -o build/dist/udpinput-linux.test
} finally {
    if ($null -eq $oldGOOS) { Remove-Item Env:GOOS } else { $env:GOOS = $oldGOOS }
    if ($null -eq $oldGOARCH) { Remove-Item Env:GOARCH } else { $env:GOARCH = $oldGOARCH }
}
Remove-Item -LiteralPath "build/dist/udpinput-linux.test"
go mod tidy
go mod verify
git diff --check
```

Expected: tests and Linux compilation pass. Because `socket_linux.go` uses `golang.org/x/sys/unix`, `go mod tidy` retains `golang.org/x/sys`. If the final implementation removes every `x/sys` import, `go mod tidy` must remove the dependency instead; do not keep an unused dependency manually.

- [ ] **Step 9: Commit UDP input**

```powershell
git add go.mod go.sum internal/udpinput
git commit -m "feat: receive GNSS NMEA over UDP"
```

---

## Removed Task 8: No GNSS control service

Do not create `internal/control`, a command controller, UART writer, GPIO interface, or control ACK workflow. No implementation commit corresponds to the old Task 8.

The only remaining requirement related to `0x10` belongs to Task 9: a correctly framed `GNSS_SWITCH_REQ` is silently consumed/skipped, produces no bytes, does not close the connection, and does not change service state.

---

### Task 9: Implement bounded subscription-only TCP service

**Files:**
- Create: `internal/server/limits.go`
- Create: `internal/server/hub.go`
- Create: `internal/server/session.go`
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`

- [ ] **Step 1: Write failing connection-limit tests**

Test a limiter configured for total 5 and non-loopback 4:

```go
func TestLimiterReservesOneLoopbackSlot(t *testing.T) {
	limiter := newLimiter(5, 4)
	for i := 0; i < 4; i++ {
		if release, ok := limiter.acquire(false); !ok {
			t.Fatalf("remote %d rejected", i)
		} else {
			defer release()
		}
	}
	if _, ok := limiter.acquire(false); ok {
		t.Fatal("fifth remote must be rejected")
	}
	if release, ok := limiter.acquire(true); !ok {
		t.Fatal("loopback fifth connection must be accepted")
	} else {
		release()
	}
}
```

Also test release idempotence and total-limit enforcement.

- [ ] **Step 2: Write failing subscription/session tests**

Using `net.Pipe` and the existing `protocol.Decoder`, prove:

- the first successful subscription selects SIMPLE or FULL;
- a second subscription returns `ALREADY_SUBSCRIBED` and does not change format;
- invalid status type returns `INVALID_STATUS_TYPE`;
- an otherwise recognizable subscribe with unsupported version returns `UNSUPPORTED_VERSION`;
- unknown messages and protocol garbage are skipped without closing;
- no valid subscription within 5 seconds closes the connection;
- a canonical six-byte `0x10` frame and a correctly framed `0x10` with another payload length up to 1024 both produce no response, no state change, and allow a following subscribe to succeed;
- a client-sent `0x11` is skipped as unsupported input;
- there is no import or dependency on an `internal/control` package.

The `0x10` tests must set a short client read deadline and first assert a timeout, not EOF or an ACK; each then sends a legal subscription and reads the normal subscribe ACK on the same connection. The frozen decoder may discard a known wrong-length `0x10` before the session switch; that still satisfies silent whole-frame skipping and must not be changed in `internal/protocol`.

- [ ] **Step 3: Implement latest-value publication tests**

Create a per-session output queue with capacity one. Tests must prove:

- a pending old status is replaced by the newest status;
- a SIMPLE subscriber receives exactly 66-byte frames;
- a FULL subscriber receives exactly 132-byte frames;
- a 3-second write deadline closes only the blocked client;
- a blocked subscriber cannot block `Hub.Publish` or another subscriber.

Use `model.FullStatus.Simple()` and the frozen `protocol.EncodeSimple`/`EncodeFull`; do not duplicate field encoding.

- [ ] **Step 4: Implement the session frame policy**

The receive policy must be explicit:

```go
switch frame.Type {
case protocol.TypeSubscribeRequest:
	// parse and process subscription
case protocol.TypeSwitchRequest:
	// unsupported in UDP architecture: silently skip, write nothing
default:
	// unknown or client-inappropriate type: skip the complete frame
}
```

Never call `protocol.ParseSwitchRequest` and never encode `SwitchACK`. Protocol framing errors remain recoverable through the existing decoder.

- [ ] **Step 5: Implement capacity rejection without subscription waiting**

The accept loop checks total/remote capacity immediately. A rejected connection must not enter the 5-second subscription path. It may perform one nonblocking read only:

- if a complete legal subscribe request is already buffered, make a best-effort `SERVER_FULL` ACK with a short write deadline;
- otherwise close immediately.

Tests cover both direct close and buffered `SERVER_FULL`, plus four continuously reading LAN clients followed by one accepted loopback subscriber.

- [ ] **Step 6: Verify and commit the TCP server**

```powershell
gofmt -w internal/server
go test -count=1 ./internal/server -v
go test -count=1 ./...
go vet ./...
git diff --check
git add internal/server
git commit -m "feat: publish GNSS status to subscribers"
```

Expected: all session, limit, latest-value, timeout, and silent-`0x10` tests pass.

---

### Task 10: Compose UDP input, aggregation, TCP publication, and observability

**Files:**
- Create: `internal/observe/stats.go`
- Create: `internal/observe/stats_test.go`
- Create: `internal/app/app.go`
- Create: `internal/app/app_test.go`
- Create: `cmd/gnssagent/main.go`

- [ ] **Step 1: Write atomic observability tests**

`observe.Stats` must record and snapshot/reset interval counters for:

- UDP datagrams and received bytes;
- UDP reject reasons;
- checksum and parser failures;
- valid NMEA by talker and sentence kind;
- kernel drop delta and source;
- complete/incomplete GSV cycles as exposed by application events;
- published cycles;
- TCP connections/rejections/subscriptions;
- slow-client replacements;
- last checksum-valid supported NMEA time.

Tests must update counters concurrently and run with `go test -race` when a CGO-enabled host is available. Do not add UART bytes, baud occupancy, overrun/frame/parity, or ICount fields.

- [ ] **Step 2: Define app boundaries and write failing assembly tests**

Use interfaces so tests do not open real ports:

```go
type UDPInput interface {
	Run(context.Context, udpinput.Sink) error
}

type StatusServer interface {
	Run(context.Context) error
	Publish(model.FullStatus)
}
```

Tests prove:

- TCP `Run` starts even while UDP bind attempts fail/retry;
- valid UDP sentences feed the existing aggregator and publish at most once per UTC second;
- the 1.5-second flush publishes without a next-second sentence;
- a UDP read-reset event calls `aggregate.Clear` and prevents pre-error fields from leaking;
- no UDP input produces no status or heartbeat;
- no-fix input publishes `valid=0`;
- a slow TCP fake cannot block the UDP sink;
- shutdown cancels UDP, stops TCP, and does not call UART/GPIO/clock APIs.

- [ ] **Step 3: Implement concurrency without a serial supervisor**

`App` owns one aggregator guarded by a mutex. UDP sink callbacks perform only validation-complete aggregate work and a nonblocking server `Publish`. A short ticker calls `FlushExpired`; it does not create empty states. UDP socket reset clears the current cycle.

Run TCP and UDP components independently:

- TCP listen failure is returned as a service-start failure;
- UDP bind/read failures remain inside `udpinput.Manager` and do not stop TCP;
- context cancellation stops both.

There is no serial open/reopen loop, endpoint writer, controller, power-state observer, or control connection.

- [ ] **Step 4: Add required periodic and transition logging**

Use `log/slog` text output. Log at startup:

- version/build target;
- UDP/TCP addresses and TCP limits;
- system Unix milliseconds and formatted UTC time;
- requested/actual `SO_RCVBUF` and drop-observation source.

Every second log an interval summary of received UDP datagrams/bytes and valid NMEA counts by talker/kind. Rate-limit repeated invalid-datagram/checksum/parser/bind/drop warnings. Emit one input-interrupted message after 5 seconds without a checksum-valid supported NMEA and one recovery message when input resumes.

Do not log a fixed expected sentence count, UART occupancy, ICount, parity/frame/overrun, or raw NMEA by default. Debug logging may sample at most one raw sentence per rate-limit interval.

- [ ] **Step 5: Implement `main` with UDP/TCP defaults from config**

`cmd/gnssagent/main.go` must:

1. parse `config.Parse(os.Args[1:], buildinfo.Target)`;
2. create text `slog` with the requested level;
3. construct the UDP manager for `UDPListenAddress`;
4. construct the TCP server for `TCPListenAddress` and limits;
5. run the app under `signal.NotifyContext` for SIGINT/SIGTERM;
6. never import a serial/control package or modify the system clock.

- [ ] **Step 6: Add an actual UDP-to-TCP integration test**

Use loopback ephemeral ports. Start the app, subscribe SIMPLE over TCP, send checksum-correct fixture lines as separate UDP datagrams, and assert one 66-byte status frame with the expected valid fields. Additional cases:

- send nothing: TCP remains connected and read times out with no frame;
- send no-fix NMEA: receive a frame whose valid mask bit is set and value is zero;
- send a split NMEA as two UDP datagrams: receive no state from those fragments;
- stop/restart the UDP sender: later complete cycles publish without stale fields;
- send a valid `0x10` over TCP before subscription: no response, then normal subscription works.

- [ ] **Step 7: Verify and commit app composition**

```powershell
gofmt -w cmd/gnssagent internal/app internal/observe
go test -count=1 ./internal/observe ./internal/app -v
go test -count=1 ./...
go vet ./...
git diff --check
git add cmd/gnssagent internal/app internal/observe
git commit -m "feat: run GNSSAgent over UDP input"
```

Expected: app, integration, frozen baseline, and vet checks pass.

---

### Task 11: Add reproducible target builds

**Files:**
- Create: `build/scripts/build.ps1`
- Create: `build/scripts/build-hf.ps1`
- Create: `build/scripts/build.bat`
- Create: `tests/build.test.ps1`

- [ ] **Step 1: Write the build contract test**

The PowerShell test must assert:

```powershell
$required = @(
    'go1.25.5',
    'GNSSAgent-CCU',
    'GNSSAgent-MultibandRadio',
    'GNSSAgent-MultibandHandheld',
    '-GOARM64 "v8.0"',
    '-GOARM "7"',
    'CGO_ENABLED',
    '-trimpath',
    '-s -w',
    'gnssagent/internal/buildinfo.Target'
)
```

It also asserts that the HF script contains Go 1.23.12 and `GNSSAgent-HF`, and that neither script contains `CCU-Audio`, a serial-device argument, or a baud argument.

- [ ] **Step 2: Implement the Go 1.25.5 build matrix**

Follow the compiler archive/cache pattern used by `D:\CPD\CPDC`. Validate the exact toolchain version, set `CGO_ENABLED=0`, restore all environment variables in `finally`, and call one build function for:

```powershell
Build-GNSSAgent -Name "GNSSAgent-CCU" -GOARCH "amd64" -Target "ccu"
Build-GNSSAgent -Name "GNSSAgent-MultibandRadio" -GOARCH "arm64" -GOARM64 "v8.0" -Target "multiband-radio"
Build-GNSSAgent -Name "GNSSAgent-MultibandHandheld" -GOARCH "arm" -GOARM "7" -Target "multiband-handheld"
```

Build with:

```powershell
& $goExe build -trimpath -ldflags "-s -w -X gnssagent/internal/buildinfo.Target=$Target" `
    -o $destination ./cmd/gnssagent
```

Use `build/compiler/go1.25.5.windows-amd64.zip`; output to `build/dist/bin`.

- [ ] **Step 3: Implement the isolated HF build**

Use `build/compiler/go1.23.12.windows-amd64.zip`, `GOOS=linux`, `GOARCH=arm`, `GOARM=7`, and the same `CGO_ENABLED=0`, trimpath, stripping, and target link variable. Output `GNSSAgent-HF`. Do not build `GNSSAgent-CCU-Audio`.

- [ ] **Step 4: Tidy modules and verify Linux dependency use**

```powershell
go mod tidy
go mod verify
$oldGOOS = $env:GOOS
try {
    $env:GOOS = "linux"
    go list -deps ./cmd/gnssagent | Select-String -SimpleMatch "golang.org/x/sys/unix"
} finally {
    if ($null -eq $oldGOOS) { Remove-Item Env:GOOS } else { $env:GOOS = $oldGOOS }
}
```

Expected: `x/sys/unix` appears because Linux UDP socket telemetry uses it. If implementation no longer imports it, the Select-String assertion must instead be removed and `go mod tidy` must remove `golang.org/x/sys` from `go.mod`/`go.sum`.

- [ ] **Step 5: Run build contracts and all cross-builds**

```powershell
pwsh -File tests/build.test.ps1
pwsh -File build/scripts/build.ps1
pwsh -File build/scripts/build-hf.ps1
```

Expected artifacts:

```text
build/dist/bin/GNSSAgent-CCU
build/dist/bin/GNSSAgent-HF
build/dist/bin/GNSSAgent-MultibandRadio
build/dist/bin/GNSSAgent-MultibandHandheld
```

- [ ] **Step 6: Commit build support**

```powershell
git add go.mod go.sum build/scripts tests/build.test.ps1
git commit -m "build: add GNSSAgent target matrix"
```

---

### Task 12: Add SysV deployment and UDP smoke checks

**Files:**
- Create: `deploy/default/gnssagent`
- Create: `deploy/init.d/gnssagent`
- Create: `tests/deploy.test.ps1`
- Create: `tests/device/smoke.sh`

- [ ] **Step 1: Write deployment contract tests**

Assert the defaults contain:

```text
UDP_LISTEN=127.0.0.1:29501
TCP_LISTEN=0.0.0.0:29501
MAX_CONNECTIONS=5
MAX_REMOTE_CONNECTIONS=4
LOG_LEVEL=info
```

Assert init/smoke scripts contain no `/dev/tty`, `ttyUL`, baud, termios, `TIOC`, `CFGSYS`, `CFGSAVE`, GPIO, link budget, or UART capture command.

- [ ] **Step 2: Implement the SysV init script**

The start command passes only:

```sh
--udp-listen "$UDP_LISTEN" \
--tcp-listen "$TCP_LISTEN" \
--max-connections "$MAX_CONNECTIONS" \
--max-remote-connections "$MAX_REMOTE_CONNECTIONS" \
--log-level "$LOG_LEVEL"
```

Use the target's existing `start-stop-daemon` pattern, PID file, stdout/stderr log destination, TERM stop, and restart. UDP bind failure must not terminate the process; recovery is internal to the service.

- [ ] **Step 3: Implement UDP/TCP smoke checks without serial inspection**

`tests/device/smoke.sh` must:

1. verify the process is running;
2. verify `127.0.0.1:29501/UDP` and the configured TCP port are listening;
3. send each line of a known fixture as a separate UDP datagram using an available UDP-capable `nc` command;
4. send a binary SIMPLE subscription and verify a 66-byte response;
5. report `SO_RCVBUF`/kernel-drop information from service logs;
6. skip with a clear prerequisite error if the target has no compatible `nc`/`od` tools.

It must not inspect which process owns a UART, calculate UART bandwidth, read `/proc/tty`, or collect overrun/frame/parity counters. Those are outside GNSSAgent's revised responsibility.

- [ ] **Step 4: Verify scripts and commit**

```powershell
pwsh -File tests/deploy.test.ps1
git diff --check
git add deploy tests/deploy.test.ps1 tests/device/smoke.sh
git commit -m "deploy: add UDP GNSSAgent SysV service"
```

---

### Task 13: Final UDP architecture conformance and regression verification

**Files:**
- Create: `tests/protocol_doc.test.ps1`
- Create: `tests/architecture.test.ps1`
- Create: `tests/regression/udp_baseline_test.go`

- [ ] **Step 1: Add external frozen-baseline regression tests**

Create a test package outside the frozen directories:

```go
package regression

import (
	"testing"

	"gnssagent/internal/nmea"
)

func TestFrozenDOPLexicalCarry(t *testing.T) {
	tests := map[string]int64{
		"0.4895": 490,
		"0.5005": 501,
		"0.9995": 1000,
	}
	for input, want := range tests {
		got, err := nmea.ParseMilliDecimal(input)
		if err != nil || got != want {
			t.Fatalf("%s: got %d err=%v want %d", input, got, err, want)
		}
	}
}
```

Do not move this test into `internal/nmea` or modify its existing tests. Existing `internal/aggregate` tests remain the authority for PRN collision rules.

- [ ] **Step 2: Verify frozen packages were not changed**

Use the completed Task 6 commit as the code baseline:

```powershell
git diff --exit-code 54e11598b0c237ff27940ce791e73424b6231b31..HEAD -- `
    internal/model internal/nmea internal/aggregate internal/protocol internal/buildinfo
```

Expected: no output and exit code 0. A failure must be resolved by reverting the later change, not by updating the baseline SHA.

- [ ] **Step 3: Implement binary protocol document checks for only active sections**

`tests/protocol_doc.test.ps1` parses the binary protocol tables and checks only:

- common header size 8 and magic/version;
- `0x01`, `0x02`, `0x03`, `0x04` payload/frame lengths;
- SIMPLE offsets total 58 and validity bits 0–8;
- FULL offsets total 124 and validity bits 0–28;
- SIMPLE/FULL/subscribe golden bytes.

It must deliberately ignore the document's `0x10`/`0x11` payload definitions. Their continued presence in the frozen protocol package is not a conformance failure; service behavior is tested separately.

- [ ] **Step 4: Implement architecture guards**

`tests/architecture.test.ps1` scans production `.go` files, deploy scripts, and build scripts and fails if it finds:

- an `internal/serial` or `internal/control` directory;
- `SerialDevice`, a baud flag, `/dev/tty`, termios, `TIOCEXCL`, `TIOCGICOUNT`, `CFGSYS`, `CFGSAVE`, GPIO control, or system-clock mutation;
- a server call to `ParseSwitchRequest`, `EncodeSwitchACK`, or a control handler;
- UDP binding other than a validated IPv4 loopback address;
- a UART/link-budget device script.

Exclude approved historical/spec/protocol documents and the frozen `internal/protocol` package from string checks for `0x10` definitions; otherwise the guard would reject the preserved baseline intentionally.

- [ ] **Step 5: Run the full verification matrix**

```powershell
go test -count=1 ./...
go vet ./...
go mod verify
pwsh -File tests/protocol_doc.test.ps1
pwsh -File tests/architecture.test.ps1
pwsh -File tests/build.test.ps1
pwsh -File tests/deploy.test.ps1
pwsh -File build/scripts/build.ps1
pwsh -File build/scripts/build-hf.ps1
git diff --check
git status --short
```

Expected: all commands pass. `git status --short` shows only the three intentional Task 13 test files until Step 7 commits them; generated build artifacts are ignored.

- [ ] **Step 6: Run host integration and device acceptance**

Host integration must cover UDP bind retry, datagram rejection, actual UDP-to-TCP publication, no-input silence, silent `0x10`, connection limits, subscription timeout, and slow-client isolation.

On every target, archive:

- a side-by-side UART-owner `read()` capture and loopback UDP capture proving one complete UART NMEA maps to exactly one UDP datagram, in order, with zero loss/duplicates/reordering; reject the run as invalid if the UART-side capture itself is incomplete;
- actual `SO_RCVBUF`;
- `SO_RXQ_OVFL` or `/proc/net/udp` drop source and totals;
- UART-owner-to-UDP `forward_delay` p50/p95/p99/max from the external UART-owner validation process;
- GPS/BeiDou/combined-mode status samples;
- 24-hour memory, process-exit, old-field, and UDP-drop results.

This repository task does not add UART inspection or link-budget scripts. The external owner-process team performs UART-to-UDP fidelity capture described by the requirements.

- [ ] **Step 7: Commit final conformance tests**

```powershell
git add tests/protocol_doc.test.ps1 tests/architecture.test.ps1 tests/regression/udp_baseline_test.go
git commit -m "test: verify UDP GNSSAgent conformance"
```

- [ ] **Step 8: Confirm the final repository state**

```powershell
git diff --check
git status --short
```

Expected: both checks are silent. If build tools left ignored artifacts, list them separately with `git status --short --ignored build/dist` and do not add them.

---

## Completion gate

Implementation is complete only when:

- frozen Tasks 1–6 packages have no diff from `54e11598b0c237ff27940ce791e73424b6231b31`;
- config has no UART path/baud concept and rejects non-loopback UDP addresses;
- UDP uses one datagram/one NMEA, the socket-returned length, truncation detection, 256 KiB `SO_RCVBUF`, kernel-drop telemetry, and in-process bind/read recovery;
- no serial/control package or side effect exists;
- a TCP `0x10` frame is silent and the connection remains usable;
- subscription limits, timeout, latest-value queue, and slow-client isolation pass;
- no-input silence and no-fix publication pass;
- four target binaries build reproducibly without CGO or CCU-Audio;
- SysV deployment contains no UART checks or link-budget collection;
- all automated and device acceptance evidence is archived.
