package sshclient

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net"
	"sync"
)

const (
	veilNonceSize = 32
	veilMagic     = "MCV1"
)

func deriveVeilStreams(peerCode string, nonce []byte) (c2s, s2c cipher.Stream) {
	makeStream := func(dir string) cipher.Stream {
		h := sha256.New()
		h.Write([]byte("mc-veil-" + dir + ":"))
		h.Write([]byte(peerCode))
		h.Write(nonce)
		key := h.Sum(nil)

		ivh := sha256.New()
		ivh.Write([]byte("mc-veil-iv-" + dir + ":"))
		ivh.Write(nonce)
		ivh.Write([]byte(peerCode))
		iv := ivh.Sum(nil)[:aes.BlockSize]

		blk, _ := aes.NewCipher(key)
		return cipher.NewCTR(blk, iv)
	}
	return makeStream("c2s"), makeStream("s2c")
}

type veilConn struct {
	net.Conn
	rmu sync.Mutex
	wmu sync.Mutex
	rs  cipher.Stream
	ws  cipher.Stream
}

func (v *veilConn) Read(p []byte) (int, error) {
	v.rmu.Lock()
	defer v.rmu.Unlock()
	n, err := v.Conn.Read(p)
	if n > 0 {
		v.rs.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (v *veilConn) Write(p []byte) (int, error) {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	buf := make([]byte, len(p))
	v.ws.XORKeyStream(buf, p)
	return v.Conn.Write(buf)
}

// WrapClientVeil obfuscates a raw TCP connection to a Peer Agent v4.1+ before TLS ClientHello.
// First byte is guaranteed != 0x16 so DPI never sees a bare-IP TLS ClientHello signature.
func WrapClientVeil(raw net.Conn, peerCode string) (net.Conn, error) {
	nonce := make([]byte, veilNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	if nonce[0] == 0x16 {
		nonce[0] ^= 0x5A
	}
	c2s, s2c := deriveVeilStreams(peerCode, nonce)

	var rb [1]byte
	_, _ = io.ReadFull(rand.Reader, rb[:])
	padLen := 16 + int(rb[0]%64)
	hdr := make([]byte, 4+1+padLen)
	copy(hdr[:4], []byte(veilMagic))
	hdr[4] = byte(padLen)
	_, _ = io.ReadFull(rand.Reader, hdr[5:])
	c2s.XORKeyStream(hdr, hdr)

	pkt := make([]byte, 0, len(nonce)+len(hdr))
	pkt = append(pkt, nonce...)
	pkt = append(pkt, hdr...)
	if _, err := raw.Write(pkt); err != nil {
		return nil, err
	}
	return &veilConn{Conn: raw, rs: s2c, ws: c2s}, nil
}
