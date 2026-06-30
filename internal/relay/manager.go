package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"
	"github.com/SadNoo/gosspanel/internal/store"
)

type Manager struct {
	store   store.Store
	logger  *slog.Logger
	mu      sync.Mutex
	running map[string]*listenerState
}

type listenerState struct {
	rule     domain.RelayRule
	listener net.Listener
	cancel   context.CancelFunc
}

func NewManager(store store.Store, logger *slog.Logger) *Manager {
	return &Manager{
		store:   store,
		logger:  logger,
		running: make(map[string]*listenerState),
	}
}

func (m *Manager) Sync(ctx context.Context) error {
	rules, err := m.store.EnabledRules(ctx)
	if err != nil {
		return err
	}

	want := make(map[string]domain.RelayRule)
	for _, rule := range rules {
		if runnable(rule) {
			want[rule.ID] = rule
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, state := range m.running {
		rule, ok := want[id]
		if !ok || rule.Listen != state.rule.Listen || rule.Target != state.rule.Target || !reflect.DeepEqual(rule.ProxyProtocol, state.rule.ProxyProtocol) {
			state.cancel()
			_ = state.listener.Close()
			delete(m.running, id)
		}
	}

	for id, rule := range want {
		if _, ok := m.running[id]; ok {
			continue
		}
		listener, err := net.Listen("tcp", rule.Listen)
		if err != nil {
			m.logger.Warn("relay listen skipped", "rule", rule.Name, "listen", rule.Listen, "error", err)
			continue
		}
		runCtx, cancel := context.WithCancel(context.Background())
		state := &listenerState{rule: rule, listener: listener, cancel: cancel}
		m.running[id] = state
		go m.serve(runCtx, state)
		m.logger.Info("relay listening", "rule", rule.Name, "listen", rule.Listen, "target", rule.Target)
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, state := range m.running {
		state.cancel()
		_ = state.listener.Close()
		delete(m.running, id)
	}
}

func (m *Manager) serve(ctx context.Context, state *listenerState) {
	for {
		conn, err := state.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				m.logger.Warn("relay accept failed", "rule", state.rule.Name, "error", err)
				continue
			}
		}
		go m.handleConn(ctx, state.rule, conn)
	}
}

func (m *Manager) handleConn(ctx context.Context, rule domain.RelayRule, inbound net.Conn) {
	defer inbound.Close()

	sourceConn := inbound
	sourceAddr := inbound.RemoteAddr()
	if shouldReceiveProxy(rule.ProxyProtocol.Mode) && trusted(rule.ProxyProtocol.TrustedCIDRs, inbound.RemoteAddr()) {
		wrapped, info, err := readProxyHeader(inbound, rule.ProxyProtocol.Version)
		if err != nil {
			m.logger.Warn("proxy protocol receive failed", "rule", rule.Name, "remote", inbound.RemoteAddr(), "error", err)
			return
		}
		sourceConn = wrapped
		sourceAddr = &net.TCPAddr{IP: info.srcIP, Port: info.srcPort}
	}

	outbound, err := net.DialTimeout("tcp", rule.Target, 10*time.Second)
	if err != nil {
		m.logger.Warn("relay dial failed", "rule", rule.Name, "target", rule.Target, "error", err)
		return
	}
	defer outbound.Close()

	if shouldSendProxy(rule.ProxyProtocol.Mode) {
		if err := writeProxyHeader(outbound, rule.ProxyProtocol.Version, sourceAddr, outbound.RemoteAddr()); err != nil {
			m.logger.Warn("proxy protocol send failed", "rule", rule.Name, "error", err)
			return
		}
	}

	if tcpAddr, ok := sourceAddr.(*net.TCPAddr); ok {
		_ = m.store.RecordOnlineIP(ctx, domain.OnlineIP{
			IP:          tcpAddr.IP.String(),
			EntryNode:   rule.Listen,
			RuleName:    rule.Name,
			Connections: 1,
			LastActive:  time.Now().Format("15:04:05"),
		})
	}

	errCh := make(chan error, 2)
	go proxyCopy(errCh, outbound, sourceConn)
	go proxyCopy(errCh, sourceConn, outbound)
	<-errCh
}

func runnable(rule domain.RelayRule) bool {
	return rule.Enabled &&
		rule.Status == domain.RuleStatusRunning &&
		rule.Inbound == domain.RelayProtocolDirectTCP &&
		rule.Outbound == domain.RelayProtocolDirectTCP &&
		rule.Listen != "" &&
		rule.Target != ""
}

func shouldReceiveProxy(mode domain.ProxyProtocolMode) bool {
	return mode == domain.ProxyProtocolReceive || mode == domain.ProxyProtocolBoth
}

func shouldSendProxy(mode domain.ProxyProtocolMode) bool {
	return mode == domain.ProxyProtocolSend || mode == domain.ProxyProtocolBoth
}

func trusted(cidrs []string, addr net.Addr) bool {
	if len(cidrs) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if cidr == host {
			return true
		}
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func proxyCopy(errCh chan<- error, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	errCh <- err
}
