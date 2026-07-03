package relay

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"
)

var tunnelMagic = []byte("GOSSTUN1")

type TunnelManager struct {
	logger  *slog.Logger
	mu      sync.Mutex
	running map[string]*tunnelState
}

type tunnelState struct {
	rule     domain.RelayRule
	listener net.Listener
	gost     *gostProcess
	cancel   context.CancelFunc
}

type tunnelRequest struct {
	RuleID        string                     `json:"ruleId"`
	Target        string                     `json:"target"`
	SourceAddr    string                     `json:"sourceAddr"`
	ProxyProtocol domain.ProxyProtocolConfig `json:"proxyProtocol"`
}

func NewTunnelManager(logger *slog.Logger) *TunnelManager {
	return &TunnelManager{
		logger:  logger,
		running: make(map[string]*tunnelState),
	}
}

func (m *TunnelManager) SyncRules(rules []domain.RelayRule) error {
	want := make(map[string]domain.RelayRule)
	for _, rule := range rules {
		if runnableTunnel(rule) {
			want[rule.ID] = rule
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, state := range m.running {
		rule, ok := want[id]
		if !ok || shouldRestartTunnel(rule, state.rule) || gostStopped(state.gost) {
			state.close()
			delete(m.running, id)
		}
	}

	for id, rule := range want {
		if _, ok := m.running[id]; ok {
			continue
		}
		state, err := m.startRule(rule)
		if err != nil {
			m.logger.Warn("client tunnel listen skipped", "rule", rule.Name, "endpoint", rule.TunnelEndpoint, "error", err)
			continue
		}
		m.running[id] = state
		m.logger.Info("client tunnel listening", "rule", rule.Name, "endpoint", rule.TunnelEndpoint, "target", rule.Target)
	}
	return nil
}

func (m *TunnelManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, state := range m.running {
		state.close()
		delete(m.running, id)
	}
}

func (m *TunnelManager) startRule(rule domain.RelayRule) (*tunnelState, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if isGost(rule) {
		gost, err := startGostClient(ctx, rule, m.logger)
		if err != nil {
			cancel()
			return nil, err
		}
		return &tunnelState{rule: rule, gost: gost, cancel: cancel}, nil
	}
	listener, err := listenTunnelTransport(rule)
	if err != nil {
		cancel()
		return nil, err
	}
	state := &tunnelState{rule: rule, listener: listener, cancel: cancel}
	go m.serve(ctx, state)
	return state, nil
}

func (s *tunnelState) close() {
	s.cancel()
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (m *TunnelManager) serve(ctx context.Context, state *tunnelState) {
	for {
		conn, err := state.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				m.logger.Warn("client tunnel accept failed", "rule", state.rule.Name, "error", err)
				continue
			}
		}
		go m.handleConn(ctx, state.rule, conn)
	}
}

func (m *TunnelManager) handleConn(ctx context.Context, rule domain.RelayRule, conn net.Conn) {
	defer conn.Close()
	tunnelConn, err := acceptTunnelTransport(conn, rule)
	if err != nil {
		m.logger.Warn("client tunnel transport failed", "rule", rule.Name, "error", err)
		return
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req, err := readTunnelRequest(tunnelConn)
	if err != nil {
		m.logger.Warn("client tunnel header failed", "rule", rule.Name, "error", err)
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if req.RuleID != rule.ID {
		m.logger.Warn("client tunnel rule mismatch", "rule", rule.Name, "got", req.RuleID)
		return
	}

	outbound, err := net.DialTimeout("tcp", rule.Target, 10*time.Second)
	if err != nil {
		m.logger.Warn("client tunnel dial failed", "rule", rule.Name, "target", rule.Target, "error", err)
		return
	}
	defer outbound.Close()

	sourceAddr := parseTCPAddr(req.SourceAddr)
	if shouldSendProxy(req.ProxyProtocol.Mode) && sourceAddr != nil {
		if err := writeProxyHeader(outbound, req.ProxyProtocol.Version, sourceAddr, outbound.RemoteAddr()); err != nil {
			m.logger.Warn("client tunnel proxy protocol failed", "rule", rule.Name, "error", err)
			return
		}
	}

	errCh := make(chan copyResult, 2)
	go proxyCopy(errCh, outbound, tunnelConn)
	go proxyCopy(errCh, tunnelConn, outbound)
	first := <-errCh
	_ = outbound.Close()
	_ = tunnelConn.Close()
	second := <-errCh
	if first.err != nil && !errors.Is(first.err, io.ErrClosedPipe) {
		m.logger.Debug("client tunnel copy finished", "rule", rule.Name, "error", first.err)
	}
	if second.err != nil && !errors.Is(second.err, io.ErrClosedPipe) {
		m.logger.Debug("client tunnel copy finished", "rule", rule.Name, "error", second.err)
	}
}

func writeTunnelRequest(w io.Writer, req tunnelRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(payload) > 1<<20 {
		return fmt.Errorf("tunnel header too large")
	}
	header := make([]byte, len(tunnelMagic)+4)
	copy(header, tunnelMagic)
	binary.BigEndian.PutUint32(header[len(tunnelMagic):], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readTunnelRequest(r io.Reader) (tunnelRequest, error) {
	header := make([]byte, len(tunnelMagic)+4)
	if _, err := io.ReadFull(r, header); err != nil {
		return tunnelRequest{}, err
	}
	if string(header[:len(tunnelMagic)]) != string(tunnelMagic) {
		return tunnelRequest{}, fmt.Errorf("invalid tunnel header")
	}
	size := binary.BigEndian.Uint32(header[len(tunnelMagic):])
	if size == 0 || size > 1<<20 {
		return tunnelRequest{}, fmt.Errorf("invalid tunnel header size")
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return tunnelRequest{}, err
	}
	var req tunnelRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return tunnelRequest{}, err
	}
	return req, nil
}

func runnableTunnel(rule domain.RelayRule) bool {
	return rule.Enabled &&
		rule.Status == domain.RuleStatusRunning &&
		(tunnelProtocol(rule.Inbound) || gostProtocol(rule.Inbound)) &&
		rule.Inbound == rule.Outbound &&
		strings.TrimSpace(rule.TunnelEndpoint) != "" &&
		strings.TrimSpace(rule.Target) != ""
}

func tunnelListenAddr(endpoint string) string {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || port == "" {
		return endpoint
	}
	if host == "" {
		return endpoint
	}
	return net.JoinHostPort("", port)
}

func parseTCPAddr(value string) *net.TCPAddr {
	addr, err := net.ResolveTCPAddr("tcp", value)
	if err != nil {
		return nil
	}
	return addr
}

func shouldRestartTunnel(next domain.RelayRule, current domain.RelayRule) bool {
	return next.TunnelEndpoint != current.TunnelEndpoint ||
		next.Target != current.Target ||
		next.Inbound != current.Inbound ||
		next.Outbound != current.Outbound ||
		!reflect.DeepEqual(next.ProxyProtocol, current.ProxyProtocol)
}
