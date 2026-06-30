package relay

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/SadNoo/gosspanel/internal/domain"
)

var proxyV2Sig = []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}

type proxiedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *proxiedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type peerInfo struct {
	srcIP   net.IP
	dstIP   net.IP
	srcPort int
	dstPort int
}

func readProxyHeader(conn net.Conn, version domain.ProxyProtocolVersion) (net.Conn, peerInfo, error) {
	reader := bufio.NewReader(conn)
	if version == domain.ProxyProtocolVersion1 {
		return readProxyV1(conn, reader)
	}
	return readProxyV2(conn, reader)
}

func readProxyV1(conn net.Conn, reader *bufio.Reader) (net.Conn, peerInfo, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, peerInfo{}, err
	}
	line = strings.TrimSpace(line)
	parts := strings.Fields(line)
	if len(parts) < 6 || parts[0] != "PROXY" {
		return nil, peerInfo{}, errors.New("invalid proxy protocol v1 header")
	}
	srcPort, err := strconv.Atoi(parts[4])
	if err != nil {
		return nil, peerInfo{}, err
	}
	dstPort, err := strconv.Atoi(parts[5])
	if err != nil {
		return nil, peerInfo{}, err
	}
	info := peerInfo{
		srcIP:   net.ParseIP(parts[2]),
		dstIP:   net.ParseIP(parts[3]),
		srcPort: srcPort,
		dstPort: dstPort,
	}
	if info.srcIP == nil || info.dstIP == nil {
		return nil, peerInfo{}, errors.New("invalid proxy protocol v1 address")
	}
	return &proxiedConn{Conn: conn, reader: reader}, info, nil
}

func readProxyV2(conn net.Conn, reader *bufio.Reader) (net.Conn, peerInfo, error) {
	header, err := reader.Peek(16)
	if err != nil {
		return nil, peerInfo{}, err
	}
	if !bytes.Equal(header[:12], proxyV2Sig) {
		return nil, peerInfo{}, errors.New("invalid proxy protocol v2 signature")
	}
	if header[13] != 0x11 && header[13] != 0x21 {
		return nil, peerInfo{}, errors.New("unsupported proxy protocol v2 family")
	}
	length := int(binary.BigEndian.Uint16(header[14:16]))
	full := make([]byte, 16+length)
	if _, err := io.ReadFull(reader, full); err != nil {
		return nil, peerInfo{}, err
	}
	addr := full[16:]
	info := peerInfo{}
	switch header[13] {
	case 0x11:
		if len(addr) < 12 {
			return nil, peerInfo{}, errors.New("short proxy protocol v2 ipv4 address")
		}
		info.srcIP = net.IPv4(addr[0], addr[1], addr[2], addr[3])
		info.dstIP = net.IPv4(addr[4], addr[5], addr[6], addr[7])
		info.srcPort = int(binary.BigEndian.Uint16(addr[8:10]))
		info.dstPort = int(binary.BigEndian.Uint16(addr[10:12]))
	case 0x21:
		if len(addr) < 36 {
			return nil, peerInfo{}, errors.New("short proxy protocol v2 ipv6 address")
		}
		info.srcIP = net.IP(addr[0:16])
		info.dstIP = net.IP(addr[16:32])
		info.srcPort = int(binary.BigEndian.Uint16(addr[32:34]))
		info.dstPort = int(binary.BigEndian.Uint16(addr[34:36]))
	}
	return &proxiedConn{Conn: conn, reader: reader}, info, nil
}

func writeProxyHeader(w io.Writer, version domain.ProxyProtocolVersion, src net.Addr, dst net.Addr) error {
	srcTCP, ok := src.(*net.TCPAddr)
	if !ok {
		return nil
	}
	dstTCP, ok := dst.(*net.TCPAddr)
	if !ok {
		return nil
	}
	if version == domain.ProxyProtocolVersion1 {
		family := "TCP4"
		if srcTCP.IP.To4() == nil {
			family = "TCP6"
		}
		_, err := fmt.Fprintf(w, "PROXY %s %s %s %d %d\r\n", family, srcTCP.IP.String(), dstTCP.IP.String(), srcTCP.Port, dstTCP.Port)
		return err
	}
	return writeProxyV2(w, srcTCP, dstTCP)
}

func writeProxyV2(w io.Writer, src, dst *net.TCPAddr) error {
	src4 := src.IP.To4()
	dst4 := dst.IP.To4()
	if src4 != nil && dst4 != nil {
		buf := make([]byte, 28)
		copy(buf[:12], proxyV2Sig)
		buf[12] = 0x21
		buf[13] = 0x11
		binary.BigEndian.PutUint16(buf[14:16], 12)
		copy(buf[16:20], src4)
		copy(buf[20:24], dst4)
		binary.BigEndian.PutUint16(buf[24:26], uint16(src.Port))
		binary.BigEndian.PutUint16(buf[26:28], uint16(dst.Port))
		_, err := w.Write(buf)
		return err
	}
	src16 := src.IP.To16()
	dst16 := dst.IP.To16()
	if src16 == nil || dst16 == nil {
		return nil
	}
	buf := make([]byte, 52)
	copy(buf[:12], proxyV2Sig)
	buf[12] = 0x21
	buf[13] = 0x21
	binary.BigEndian.PutUint16(buf[14:16], 36)
	copy(buf[16:32], src16)
	copy(buf[32:48], dst16)
	binary.BigEndian.PutUint16(buf[48:50], uint16(src.Port))
	binary.BigEndian.PutUint16(buf[50:52], uint16(dst.Port))
	_, err := w.Write(buf)
	return err
}
