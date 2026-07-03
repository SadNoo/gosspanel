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
)

type OnlineIPRecorder interface {
	RecordOnlineIP(context.Context, domain.OnlineIP) error
}

type Manager struct {
	logger   *slog.Logger
	recorder OnlineIPRecorder
	mu       sync.Mutex
	running  map[string]*listenerState
}

type listenerState struct {
	rule        domain.RelayRule
	tcpListener net.Listener
	udpConn     *net.UDPConn
	cancel      context.CancelFunc
}

func NewManager(logger *slog.Logger, recorder OnlineIPRecorder) *Manager {
	return &Manager{
		logger:   logger,
		recorder: recorder,
		running:  make(map[string]*listenerState),
	}
}

func (m *Manager) SyncRules(rules []domain.RelayRule) error {
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
		if !ok || shouldRestart(rule, state.rule) {
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
			m.logger.Warn("relay listen skipped", "rule", rule.Name, "listen", rule.Listen, "protocol", rule.Inbound, "error", err)
			continue
		}
		m.running[id] = state
		m.logger.Info("relay listening", "rule", rule.Name, "listen", rule.Listen, "target", rule.Target, "protocol", rule.Inbound)
	}
	return nil
}

func (m *Manager) startRule(rule domain.RelayRule) (*listenerState, error) {
	runCtx, cancel := context.WithCancel(context.Background())
	state := &listenerState{rule: rule, cancel: cancel}
	if isUDP(rule) {
		conn, err := listenUDP(rule.Listen)
		if err != nil {
			cancel()
			return nil, err
		}
		state.udpConn = conn
		go m.serveUDP(runCtx, state)
		return state, nil
	}

	listener, err := net.Listen("tcp", rule.Listen)
	if err != nil {
		cancel()
		return nil, err
	}
	state.tcpListener = listener
	go m.serveTCP(runCtx, state)

	if isTCP(rule) {
		conn, err := listenUDP(rule.Listen)
		if err != nil {
			m.logger.Warn("tcp relay udp companion skipped", "rule", rule.Name, "listen", rule.Listen, "error", err)
		} else {
			state.udpConn = conn
			go m.serveUDP(runCtx, state)
		}
	}
	return state, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, state := range m.running {
		state.close()
		delete(m.running, id)
	}
}

func (s *listenerState) close() {
	s.cancel()
	if s.tcpListener != nil {
		_ = s.tcpListener.Close()
	}
	if s.udpConn != nil {
		_ = s.udpConn.Close()
	}
}

func (m *Manager) serveTCP(ctx context.Context, state *listenerState) {
	for {
		conn, err := state.tcpListener.Accept()
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

func (m *Manager) serveUDP(ctx context.Context, state *listenerState) {
	buffer := make([]byte, 65535)
	for {
		n, clientAddr, err := state.udpConn.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				m.logger.Warn("udp relay read failed", "rule", state.rule.Name, "error", err)
				continue
			}
		}
		payload := make([]byte, n)
		copy(payload, buffer[:n])
		go m.handleUDP(ctx, state.rule, state.udpConn, clientAddr, payload)
	}
}

func (m *Manager) handleUDP(ctx context.Context, rule domain.RelayRule, inbound *net.UDPConn, clientAddr *net.UDPAddr, payload []byte) {
	targetAddr, err := net.ResolveUDPAddr("udp", rule.Target)
	if err != nil {
		m.logger.Warn("udp relay target resolve failed", "rule", rule.Name, "target", rule.Target, "error", err)
		return
	}
	outbound, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		m.logger.Warn("udp relay dial failed", "rule", rule.Name, "target", rule.Target, "error", err)
		return
	}
	defer outbound.Close()

	if _, err := outbound.Write(payload); err != nil {
		m.logger.Warn("udp relay write failed", "rule", rule.Name, "target", rule.Target, "error", err)
		return
	}
	m.recordOnlineAddr(ctx, rule, clientAddr)

	response := make([]byte, 65535)
	_ = outbound.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err := outbound.ReadFromUDP(response)
	if err != nil {
		if !errors.Is(err, net.ErrClosed) {
			m.logger.Debug("udp relay response skipped", "rule", rule.Name, "error", err)
		}
		return
	}
	if _, err := inbound.WriteToUDP(response[:n], clientAddr); err != nil {
		m.logger.Warn("udp relay response write failed", "rule", rule.Name, "client", clientAddr.String(), "error", err)
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
		m.recordOnlineAddr(ctx, rule, tcpAddr)
	}

	errCh := make(chan error, 2)
	go proxyCopy(errCh, outbound, sourceConn)
	go proxyCopy(errCh, sourceConn, outbound)
	<-errCh
}

func (m *Manager) recordOnlineAddr(ctx context.Context, rule domain.RelayRule, addr net.Addr) {
	if m.recorder == nil {
		return
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	if err := m.recorder.RecordOnlineIP(ctx, domain.OnlineIP{
		IP:          host,
		EntryNode:   rule.Listen,
		RuleName:    rule.Name,
		Connections: 1,
		LastActive:  time.Now().Format("15:04:05"),
	}); err != nil {
		m.logger.Warn("online ip report failed", "rule", rule.Name, "ip", host, "error", err)
	}
}

func runnable(rule domain.RelayRule) bool {
	return rule.Enabled &&
		rule.Status == domain.RuleStatusRunning &&
		((rule.Inbound == domain.RelayProtocolDirectTCP && rule.Outbound == domain.RelayProtocolDirectTCP) ||
			(rule.Inbound == domain.RelayProtocolDirectUDP && rule.Outbound == domain.RelayProtocolDirectUDP)) &&
		rule.Listen != "" &&
		rule.Target != ""
}

func isUDP(rule domain.RelayRule) bool {
	return rule.Inbound == domain.RelayProtocolDirectUDP && rule.Outbound == domain.RelayProtocolDirectUDP
}

func isTCP(rule domain.RelayRule) bool {
	return rule.Inbound == domain.RelayProtocolDirectTCP && rule.Outbound == domain.RelayProtocolDirectTCP
}

func listenUDP(listen string) (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp", addr)
}

func shouldRestart(next domain.RelayRule, current domain.RelayRule) bool {
	return next.Listen != current.Listen ||
		next.Target != current.Target ||
		next.Inbound != current.Inbound ||
		next.Outbound != current.Outbound ||
		!reflect.DeepEqual(next.ProxyProtocol, current.ProxyProtocol)
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
