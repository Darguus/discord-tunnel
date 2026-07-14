package smoke

import (
	"encoding/binary"
	"net"
	"testing"
)

// A STUN parsing bug would not crash — it would hand back the wrong address, and
// the smoke test would then either cry "leak" at a working tunnel or bless a
// leaking one. Both are worse than a crash, so the parser is tested directly.

func buildResponse(txID [12]byte, attrType uint16, ip net.IP, xor bool) []byte {
	v4 := ip.To4()
	value := make([]byte, 8)
	value[0] = 0
	value[1] = 0x01 // IPv4 family
	// Port is not read by the parser, so any value does.
	binary.BigEndian.PutUint16(value[2:4], 0x1234)
	if xor {
		binary.BigEndian.PutUint16(value[2:4], 0x1234^(stunMagicCookie>>16))
		cookie := []byte{0x21, 0x12, 0xA4, 0x42}
		for i := 0; i < 4; i++ {
			value[4+i] = v4[i] ^ cookie[i]
		}
	} else {
		copy(value[4:8], v4)
	}

	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], stunBindingResponse)
	binary.BigEndian.PutUint16(msg[2:4], uint16(4+len(value)))
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	copy(msg[8:20], txID[:])

	attr := make([]byte, 4+len(value))
	binary.BigEndian.PutUint16(attr[0:2], attrType)
	binary.BigEndian.PutUint16(attr[2:4], uint16(len(value)))
	copy(attr[4:], value)

	return append(msg, attr...)
}

func TestParseXorMappedAddress(t *testing.T) {
	want := net.IPv4(203, 0, 113, 10).To4()
	var txID [12]byte
	msg := buildResponse(txID, attrXorMappedAddr, want, true)

	got, err := parseBindingResponse(msg, txID)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("XOR-MAPPED-ADDRESS decoded to %s, want %s", got, want)
	}
}

func TestParseLegacyMappedAddress(t *testing.T) {
	want := net.IPv4(198, 51, 100, 7).To4()
	var txID [12]byte
	msg := buildResponse(txID, attrMappedAddr, want, false)

	got, err := parseBindingResponse(msg, txID)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("MAPPED-ADDRESS decoded to %s, want %s", got, want)
	}
}

func TestParseRejectsWrongMessageType(t *testing.T) {
	var txID [12]byte
	msg := buildResponse(txID, attrXorMappedAddr, net.IPv4(1, 2, 3, 4), true)
	// Corrupt the message type into a binding *request*.
	binary.BigEndian.PutUint16(msg[0:2], stunBindingRequest)

	if _, err := parseBindingResponse(msg, txID); err == nil {
		t.Error("expected an error for a non-response message, got nil")
	}
}

func TestParseRejectsBadCookie(t *testing.T) {
	var txID [12]byte
	msg := buildResponse(txID, attrXorMappedAddr, net.IPv4(1, 2, 3, 4), true)
	binary.BigEndian.PutUint32(msg[4:8], 0xDEADBEEF)

	if _, err := parseBindingResponse(msg, txID); err == nil {
		t.Error("expected an error for a bad magic cookie, got nil")
	}
}

func TestBuildBindingRequestIsWellFormed(t *testing.T) {
	req, txID, err := buildBindingRequest()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(req) != 20 {
		t.Fatalf("a binding request with no attributes must be 20 bytes, got %d", len(req))
	}
	if binary.BigEndian.Uint16(req[0:2]) != stunBindingRequest {
		t.Error("wrong message type")
	}
	if binary.BigEndian.Uint32(req[4:8]) != stunMagicCookie {
		t.Error("wrong magic cookie")
	}
	// The transaction ID in the request must be echoed back for us to trust a
	// reply; check it is actually written into the message.
	var inMsg [12]byte
	copy(inMsg[:], req[8:20])
	if inMsg != txID {
		t.Error("transaction ID in the message does not match the returned one")
	}
}
