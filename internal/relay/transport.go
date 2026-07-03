package relay

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"
)

var tunnelTLSCertificate tls.Certificate

func init() {
	cert, err := generateSelfSignedCertificate()
	if err == nil {
		tunnelTLSCertificate = cert
	}
}

func listenTunnelTransport(rule domain.RelayRule) (net.Listener, error) {
	addr := tunnelListenAddr(rule.TunnelEndpoint)
	switch rule.Inbound {
	case domain.RelayProtocolTunnelTLS, domain.RelayProtocolTLS, domain.RelayProtocolWSS:
		return tls.Listen("tcp", addr, serverTLSConfig())
	default:
		return net.Listen("tcp", addr)
	}
}

func dialTunnelTransport(rule domain.RelayRule, timeout time.Duration) (net.Conn, error) {
	var conn net.Conn
	var err error
	switch rule.Inbound {
	case domain.RelayProtocolTunnelTLS, domain.RelayProtocolTLS, domain.RelayProtocolWSS:
		dialer := &net.Dialer{Timeout: timeout}
		conn, err = tls.DialWithDialer(dialer, "tcp", rule.TunnelEndpoint, clientTLSConfig())
	default:
		conn, err = net.DialTimeout("tcp", rule.TunnelEndpoint, timeout)
	}
	if err != nil {
		return nil, err
	}
	if rule.Inbound == domain.RelayProtocolWS || rule.Inbound == domain.RelayProtocolWSS {
		return clientWebSocket(conn, rule)
	}
	return conn, nil
}

func acceptTunnelTransport(conn net.Conn, rule domain.RelayRule) (net.Conn, error) {
	if rule.Inbound == domain.RelayProtocolWS || rule.Inbound == domain.RelayProtocolWSS {
		return serverWebSocket(conn)
	}
	return conn, nil
}

func serverTLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{tunnelTLSCertificate},
		MinVersion:   tls.VersionTLS12,
	}
}

func clientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}
}

func generateSelfSignedCertificate() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "goss tunnel"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func clientWebSocket(conn net.Conn, rule domain.RelayRule) (net.Conn, error) {
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	host := rule.TunnelEndpoint
	if host == "" {
		host = "goss-tunnel"
	}
	req := fmt.Sprintf("GET /goss-tunnel/%s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", rule.ID, host, key)
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", resp.Status)
	}
	if strings.TrimSpace(resp.Header.Get("Sec-WebSocket-Accept")) != websocketAccept(key) {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket accept mismatch")
	}
	return newWebSocketConn(conn, reader, true), nil
}

func serverWebSocket(conn net.Conn) (net.Conn, error) {
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := strings.TrimSpace(req.Header.Get("Sec-WebSocket-Key"))
	if !headerContains(req.Header.Get("Connection"), "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(req.Header.Get("Upgrade")), "websocket") ||
		key == "" {
		_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n")
		_ = conn.Close()
		return nil, fmt.Errorf("invalid websocket upgrade")
	}
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", websocketAccept(key))
	if _, err := io.WriteString(conn, resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return newWebSocketConn(conn, reader, false), nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContains(value string, want string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), want) {
			return true
		}
	}
	return false
}

type webSocketConn struct {
	net.Conn
	reader       *bufio.Reader
	clientWrites bool
	readBuffer   bytes.Buffer
}

func newWebSocketConn(conn net.Conn, reader *bufio.Reader, clientWrites bool) net.Conn {
	return &webSocketConn{Conn: conn, reader: reader, clientWrites: clientWrites}
}

func (c *webSocketConn) Read(p []byte) (int, error) {
	for c.readBuffer.Len() == 0 {
		payload, err := c.readFrame()
		if err != nil {
			return 0, err
		}
		if len(payload) == 0 {
			continue
		}
		_, _ = c.readBuffer.Write(payload)
	}
	return c.readBuffer.Read(p)
}

func (c *webSocketConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := c.writeFrame(0x2, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *webSocketConn) writeFrame(opcode byte, p []byte) error {
	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode)
	maskBit := byte(0)
	if c.clientWrites {
		maskBit = 0x80
	}
	switch {
	case len(p) < 126:
		header = append(header, maskBit|byte(len(p)))
	case len(p) <= 65535:
		header = append(header, maskBit|126, byte(len(p)>>8), byte(len(p)))
	default:
		header = append(header, maskBit|127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(p)))
		header = append(header, size[:]...)
	}
	if c.clientWrites {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		header = append(header, mask[:]...)
		if _, err := c.Conn.Write(header); err != nil {
			return err
		}
		masked := make([]byte, len(p))
		for i := range p {
			masked[i] = p[i] ^ mask[i%4]
		}
		if _, err := c.Conn.Write(masked); err != nil {
			return err
		}
		return nil
	}
	if _, err := c.Conn.Write(header); err != nil {
		return err
	}
	if _, err := c.Conn.Write(p); err != nil {
		return err
	}
	return nil
}

func (c *webSocketConn) readFrame() ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.reader, header[:]); err != nil {
		return nil, err
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	size := uint64(header[1] & 0x7f)
	switch size {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return nil, err
		}
		size = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return nil, err
		}
		size = binary.BigEndian.Uint64(ext[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	switch opcode {
	case 0x1, 0x2, 0x0:
		return payload, nil
	case 0x8:
		return nil, io.EOF
	case 0x9:
		_ = c.writeFrame(0xA, payload)
		return nil, nil
	case 0xA:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported websocket opcode %d", opcode)
	}
}
