package smoke

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// This is a deliberately tiny STUN client (RFC 5389), just enough to ask a
// public STUN server "what public address do you see me coming from?".
//
// It is the honest test of the whole app. Discord voice is UDP, and the entire
// reason this project exists is that a UDP-blocking network breaks voice unless
// the UDP is carried through the server. A TCP reachability check cannot prove
// that. This can: the address the STUN server reports is the address UDP
// actually left from. If the tunnel is carrying voice, that address is the
// server's; if UDP is leaking around the tunnel, it is the ISP's.

const (
	stunBindingRequest  = 0x0001
	stunBindingResponse = 0x0101
	stunMagicCookie     = 0x2112A442
	attrXorMappedAddr   = 0x0020
	attrMappedAddr      = 0x0001
)

// discoverPublicUDPAddr sends a STUN binding request to stunServer over UDP and
// returns the public address the server saw.
//
// It dials directly rather than through the loopback SOCKS proxy, because the
// SOCKS library here speaks only TCP. In smoke mode the tunnel is configured to
// capture this very process, so a direct UDP socket is captured by the TUN
// adapter and travels the exact path Discord's voice packets will — which is
// precisely what we want to measure.
func discoverPublicUDPAddr(stunServer string, timeout time.Duration) (net.IP, error) {
	conn, err := net.Dial("udp", stunServer)
	if err != nil {
		return nil, fmt.Errorf("open UDP socket: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	req, txID, err := buildBindingRequest()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("send STUN request (UDP likely blocked or not tunnelled): %w", err)
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("no STUN reply (UDP is not getting through): %w", err)
	}

	return parseBindingResponse(buf[:n], txID)
}

func buildBindingRequest() ([]byte, [12]byte, error) {
	var txID [12]byte
	if _, err := rand.Read(txID[:]); err != nil {
		return nil, txID, err
	}

	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], stunBindingRequest)
	binary.BigEndian.PutUint16(msg[2:4], 0) // no attributes, so body length is 0
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	copy(msg[8:20], txID[:])
	return msg, txID, nil
}

func parseBindingResponse(msg []byte, txID [12]byte) (net.IP, error) {
	if len(msg) < 20 {
		return nil, fmt.Errorf("STUN reply too short")
	}
	if binary.BigEndian.Uint16(msg[0:2]) != stunBindingResponse {
		return nil, fmt.Errorf("not a STUN binding response")
	}
	if binary.BigEndian.Uint32(msg[4:8]) != stunMagicCookie {
		return nil, fmt.Errorf("STUN magic cookie mismatch")
	}

	// Walk the attribute list. We accept either the modern XOR-MAPPED-ADDRESS or
	// the legacy MAPPED-ADDRESS, since not every server sends both.
	body := msg[20:]
	for len(body) >= 4 {
		attrType := binary.BigEndian.Uint16(body[0:2])
		attrLen := int(binary.BigEndian.Uint16(body[2:4]))
		if 4+attrLen > len(body) {
			break
		}
		value := body[4 : 4+attrLen]

		switch attrType {
		case attrXorMappedAddr:
			if ip := parseXorMappedAddress(value); ip != nil {
				return ip, nil
			}
		case attrMappedAddr:
			if ip := parseMappedAddress(value); ip != nil {
				return ip, nil
			}
		}

		// Attributes are padded to a 4-byte boundary.
		advance := 4 + attrLen
		if pad := attrLen % 4; pad != 0 {
			advance += 4 - pad
		}
		body = body[advance:]
	}
	return nil, fmt.Errorf("STUN reply contained no address")
}

// parseXorMappedAddress decodes a value whose address is XOR-obfuscated with the
// magic cookie, per RFC 5389.
func parseXorMappedAddress(v []byte) net.IP {
	if len(v) < 8 {
		return nil
	}
	family := v[1]
	if family != 0x01 { // 0x01 = IPv4; voice over this tunnel is IPv4
		return nil
	}
	ip := make(net.IP, 4)
	cookie := []byte{0x21, 0x12, 0xA4, 0x42}
	for i := 0; i < 4; i++ {
		ip[i] = v[4+i] ^ cookie[i]
	}
	return ip
}

func parseMappedAddress(v []byte) net.IP {
	if len(v) < 8 {
		return nil
	}
	if v[1] != 0x01 {
		return nil
	}
	return net.IP(v[4:8]).To4()
}
