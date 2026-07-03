package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"
)

type gostProcess struct {
	cmd       *exec.Cmd
	done      chan error
	localAddr string
}

func startGostRelay(ctx context.Context, rule domain.RelayRule, logger *slog.Logger) (*gostProcess, error) {
	localAddr, err := reserveLocalTCPAddr()
	if err != nil {
		return nil, err
	}
	forwardURL, err := gostForwardURL(rule)
	if err != nil {
		return nil, err
	}
	process, err := startGostProcess(ctx, rule, logger, []string{
		"-L", "tcp://" + localAddr,
		"-F", forwardURL,
	})
	if err != nil {
		return nil, err
	}
	process.localAddr = localAddr
	if err := waitTCPReady(ctx, localAddr, 5*time.Second); err != nil {
		return nil, err
	}
	return process, nil
}

func startGostClient(ctx context.Context, rule domain.RelayRule, logger *slog.Logger) (*gostProcess, error) {
	listenURL, err := gostListenURL(rule)
	if err != nil {
		return nil, err
	}
	process, err := startGostProcess(ctx, rule, logger, []string{"-L", listenURL})
	if err != nil {
		return nil, err
	}
	if err := waitTCPReady(ctx, localTunnelDialAddr(rule.TunnelEndpoint), 5*time.Second); err != nil {
		return nil, err
	}
	return process, nil
}

func startGostProcess(ctx context.Context, rule domain.RelayRule, logger *slog.Logger, args []string) (*gostProcess, error) {
	binary := gostBinary()
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("gost binary not found: %w", err)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &gostProcess{cmd: cmd, done: make(chan error, 1)}
	go streamGostLog(logger, rule, stdout)
	go streamGostLog(logger, rule, stderr)
	go func() {
		err := cmd.Wait()
		if err != nil && ctx.Err() == nil {
			logger.Warn("gost process exited", "rule", rule.Name, "protocol", rule.Inbound, "error", err)
		}
		process.done <- err
		close(process.done)
	}()
	logger.Info("gost process started", "rule", rule.Name, "protocol", rule.Inbound)
	return process, nil
}

func (p *gostProcess) running() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func streamGostLog(logger *slog.Logger, rule domain.RelayRule, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			logger.Debug("gost", "rule", rule.Name, "line", line)
		}
	}
}

func gostBinary() string {
	if value := strings.TrimSpace(os.Getenv("GOSS_GOST_BIN")); value != "" {
		return value
	}
	return "gost"
}

func gostForwardURL(rule domain.RelayRule) (string, error) {
	prefix, err := gostProtocolPrefix(rule.Inbound)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("forward+%s://%s", prefix, rule.TunnelEndpoint)
	if rule.Inbound == domain.RelayProtocolGOSTWSS {
		url += "?secure=false"
	}
	return url, nil
}

func gostListenURL(rule domain.RelayRule) (string, error) {
	prefix, err := gostProtocolPrefix(rule.Inbound)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("forward+%s://%s/%s", prefix, tunnelListenAddr(rule.TunnelEndpoint), rule.Target), nil
}

func gostProtocolPrefix(protocol domain.RelayProtocol) (string, error) {
	switch protocol {
	case domain.RelayProtocolGOSTTCP:
		return "tcp", nil
	case domain.RelayProtocolGOSTWS:
		return "ws", nil
	case domain.RelayProtocolGOSTWSS:
		return "wss", nil
	default:
		return "", fmt.Errorf("unsupported gost protocol %s", protocol)
	}
}

func reserveLocalTCPAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func waitTCPReady(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		dialer := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for %s: %w", addr, lastErr)
}

func localTunnelDialAddr(endpoint string) string {
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil || port == "" {
		return endpoint
	}
	return net.JoinHostPort("127.0.0.1", port)
}
