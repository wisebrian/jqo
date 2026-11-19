// Package sha2 implements SHA-256, SHA-384 and SHA-512 (FIPS 180-4) and HMAC
// (RFC 2104), to sign JSON Web Tokens. The standard library's crypto packages
// compute the same thing, but through the FIPS 140 module they import os and
// time, which the core must not, and they add about 135 KB to the
// WebAssembly binary. The tests compare every result with crypto/sha256,
// crypto/sha512 and crypto/hmac.
//
// Signing happens on the visitor's own data in their own browser, so the
// constant-time care that verifying secrets on a server needs doesn't apply.
package sha2

import "math/bits"

// The constants are the first bits of the fractional parts of the square
// roots (initial values) and cube roots (round constants) of the first
// primes, as FIPS 180-4 defines them; they were computed from that
// definition with math/big.

var iv256 = [8]uint32{
	0x6a09e667,
	0xbb67ae85,
	0x3c6ef372,
	0xa54ff53a,
	0x510e527f,
	0x9b05688c,
	0x1f83d9ab,
	0x5be0cd19,
}

var k256 = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

var iv512 = [8]uint64{
	0x6a09e667f3bcc908,
	0xbb67ae8584caa73b,
	0x3c6ef372fe94f82b,
	0xa54ff53a5f1d36f1,
	0x510e527fade682d1,
	0x9b05688c2b3e6c1f,
	0x1f83d9abfb41bd6b,
	0x5be0cd19137e2179,
}

var iv384 = [8]uint64{
	0xcbbb9d5dc1059ed8,
	0x629a292a367cd507,
	0x9159015a3070dd17,
	0x152fecd8f70e5939,
	0x67332667ffc00b31,
	0x8eb44a8768581511,
	0xdb0c2e0d64f98fa7,
	0x47b5481dbefa4fa4,
}

var k512 = [80]uint64{
	0x428a2f98d728ae22, 0x7137449123ef65cd, 0xb5c0fbcfec4d3b2f, 0xe9b5dba58189dbbc,
	0x3956c25bf348b538, 0x59f111f1b605d019, 0x923f82a4af194f9b, 0xab1c5ed5da6d8118,
	0xd807aa98a3030242, 0x12835b0145706fbe, 0x243185be4ee4b28c, 0x550c7dc3d5ffb4e2,
	0x72be5d74f27b896f, 0x80deb1fe3b1696b1, 0x9bdc06a725c71235, 0xc19bf174cf692694,
	0xe49b69c19ef14ad2, 0xefbe4786384f25e3, 0x0fc19dc68b8cd5b5, 0x240ca1cc77ac9c65,
	0x2de92c6f592b0275, 0x4a7484aa6ea6e483, 0x5cb0a9dcbd41fbd4, 0x76f988da831153b5,
	0x983e5152ee66dfab, 0xa831c66d2db43210, 0xb00327c898fb213f, 0xbf597fc7beef0ee4,
	0xc6e00bf33da88fc2, 0xd5a79147930aa725, 0x06ca6351e003826f, 0x142929670a0e6e70,
	0x27b70a8546d22ffc, 0x2e1b21385c26c926, 0x4d2c6dfc5ac42aed, 0x53380d139d95b3df,
	0x650a73548baf63de, 0x766a0abb3c77b2a8, 0x81c2c92e47edaee6, 0x92722c851482353b,
	0xa2bfe8a14cf10364, 0xa81a664bbc423001, 0xc24b8b70d0f89791, 0xc76c51a30654be30,
	0xd192e819d6ef5218, 0xd69906245565a910, 0xf40e35855771202a, 0x106aa07032bbd1b8,
	0x19a4c116b8d2d0c8, 0x1e376c085141ab53, 0x2748774cdf8eeb99, 0x34b0bcb5e19b48a8,
	0x391c0cb3c5c95a63, 0x4ed8aa4ae3418acb, 0x5b9cca4f7763e373, 0x682e6ff3d6b2b8a3,
	0x748f82ee5defb2fc, 0x78a5636f43172f60, 0x84c87814a1f0ab72, 0x8cc702081a6439ec,
	0x90befffa23631e28, 0xa4506cebde82bde9, 0xbef9a3f7b2c67915, 0xc67178f2e372532b,
	0xca273eceea26619c, 0xd186b8c721c0c207, 0xeada7dd6cde0eb1e, 0xf57d4f7fee6ed178,
	0x06f067aa72176fba, 0x0a637dc5a2c898a6, 0x113f9804bef90dae, 0x1b710b35131c471b,
	0x28db77f523047d84, 0x32caab7b40c72493, 0x3c9ebe0a15c9bebc, 0x431d67c49c100d4c,
	0x4cc5d4becb3e42b6, 0x597f299cfc657e2a, 0x5fcb6fab3ad6faec, 0x6c44198c4a475817,
}

// digest is the state of one hash computation.
type digest interface {
	write(p []byte)
	sum() []byte
	reset()
	blockSize() int
}

type d256 struct {
	h   [8]uint32
	buf [64]byte
	n   int    // bytes waiting in buf
	len uint64 // bytes written in total
}

func (d *d256) reset()         { *d = d256{h: iv256} }
func (d *d256) blockSize() int { return 64 }

func (d *d256) write(p []byte) {
	d.len += uint64(len(p))
	if d.n > 0 {
		k := copy(d.buf[d.n:], p)
		d.n += k
		p = p[k:]
		if d.n < len(d.buf) {
			return
		}
		block256(&d.h, d.buf[:])
		d.n = 0
	}
	for len(p) >= len(d.buf) {
		block256(&d.h, p[:len(d.buf)])
		p = p[len(d.buf):]
	}
	d.n = copy(d.buf[:], p)
}

func (d *d256) sum() []byte {
	c := *d // summing must not change the state being summed
	bitLen := c.len * 8
	var pad [64 + 8]byte
	pad[0] = 0x80
	n := 56 - int(c.len%64) // pad to 56 bytes into a block, leaving 8 for the length
	if n <= 0 {
		n += 64
	}
	for i := 0; i < 8; i++ {
		pad[n+i] = byte(bitLen >> (56 - 8*i))
	}
	c.write(pad[:n+8])
	out := make([]byte, 32)
	for i, v := range c.h {
		out[4*i], out[4*i+1], out[4*i+2], out[4*i+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	return out
}

func block256(h *[8]uint32, p []byte) {
	var w [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = uint32(p[4*i])<<24 | uint32(p[4*i+1])<<16 | uint32(p[4*i+2])<<8 | uint32(p[4*i+3])
	}
	for i := 16; i < 64; i++ {
		s0 := bits.RotateLeft32(w[i-15], -7) ^ bits.RotateLeft32(w[i-15], -18) ^ w[i-15]>>3
		s1 := bits.RotateLeft32(w[i-2], -17) ^ bits.RotateLeft32(w[i-2], -19) ^ w[i-2]>>10
		w[i] = w[i-16] + s0 + w[i-7] + s1
	}
	a, b, c, d, e, f, g, hh := h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]
	for i := 0; i < 64; i++ {
		t1 := hh + (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11) ^ bits.RotateLeft32(e, -25)) + (e&f ^ ^e&g) + k256[i] + w[i]
		t2 := (bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)) + (a&b ^ a&c ^ b&c)
		hh, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	h[0] += a
	h[1] += b
	h[2] += c
	h[3] += d
	h[4] += e
	h[5] += f
	h[6] += g
	h[7] += hh
}

type d512 struct {
	h    [8]uint64
	iv   *[8]uint64
	size int // bytes of output: 64 for SHA-512, 48 for SHA-384
	buf  [128]byte
	n    int
	len  uint64
}

func (d *d512) reset()         { *d = d512{h: *d.iv, iv: d.iv, size: d.size} }
func (d *d512) blockSize() int { return 128 }

func (d *d512) write(p []byte) {
	d.len += uint64(len(p))
	if d.n > 0 {
		k := copy(d.buf[d.n:], p)
		d.n += k
		p = p[k:]
		if d.n < len(d.buf) {
			return
		}
		block512(&d.h, d.buf[:])
		d.n = 0
	}
	for len(p) >= len(d.buf) {
		block512(&d.h, p[:len(d.buf)])
		p = p[len(d.buf):]
	}
	d.n = copy(d.buf[:], p)
}

func (d *d512) sum() []byte {
	c := *d
	bitLen := c.len * 8
	var pad [128 + 16]byte
	pad[0] = 0x80
	n := 112 - int(c.len%128) // leave 16 bytes for the 128-bit length
	if n <= 0 {
		n += 128
	}
	for i := 0; i < 8; i++ { // the high 64 bits of the length stay zero
		pad[n+8+i] = byte(bitLen >> (56 - 8*i))
	}
	c.write(pad[:n+16])
	out := make([]byte, 64)
	for i, v := range c.h {
		for j := 0; j < 8; j++ {
			out[8*i+j] = byte(v >> (56 - 8*j))
		}
	}
	return out[:c.size]
}

func block512(h *[8]uint64, p []byte) {
	var w [80]uint64
	for i := 0; i < 16; i++ {
		for j := 0; j < 8; j++ {
			w[i] = w[i]<<8 | uint64(p[8*i+j])
		}
	}
	for i := 16; i < 80; i++ {
		s0 := bits.RotateLeft64(w[i-15], -1) ^ bits.RotateLeft64(w[i-15], -8) ^ w[i-15]>>7
		s1 := bits.RotateLeft64(w[i-2], -19) ^ bits.RotateLeft64(w[i-2], -61) ^ w[i-2]>>6
		w[i] = w[i-16] + s0 + w[i-7] + s1
	}
	a, b, c, d, e, f, g, hh := h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]
	for i := 0; i < 80; i++ {
		t1 := hh + (bits.RotateLeft64(e, -14) ^ bits.RotateLeft64(e, -18) ^ bits.RotateLeft64(e, -41)) + (e&f ^ ^e&g) + k512[i] + w[i]
		t2 := (bits.RotateLeft64(a, -28) ^ bits.RotateLeft64(a, -34) ^ bits.RotateLeft64(a, -39)) + (a&b ^ a&c ^ b&c)
		hh, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	h[0] += a
	h[1] += b
	h[2] += c
	h[3] += d
	h[4] += e
	h[5] += f
	h[6] += g
	h[7] += hh
}

func new256() digest { d := &d256{}; d.reset(); return d }
func new384() digest { d := &d512{iv: &iv384, size: 48}; d.reset(); return d }
func new512() digest { d := &d512{iv: &iv512, size: 64}; d.reset(); return d }

func sum(d digest, p []byte) []byte {
	d.write(p)
	return d.sum()
}

// Sum256 returns the SHA-256 digest of p.
func Sum256(p []byte) []byte { return sum(new256(), p) }

// Sum384 returns the SHA-384 digest of p.
func Sum384(p []byte) []byte { return sum(new384(), p) }

// Sum512 returns the SHA-512 digest of p.
func Sum512(p []byte) []byte { return sum(new512(), p) }

// HMAC256 returns HMAC-SHA-256 of msg under key, the HS256 signature.
func HMAC256(key, msg []byte) []byte { return mac(new256(), key, msg) }

// HMAC384 returns HMAC-SHA-384 of msg under key, the HS384 signature.
func HMAC384(key, msg []byte) []byte { return mac(new384(), key, msg) }

// HMAC512 returns HMAC-SHA-512 of msg under key, the HS512 signature.
func HMAC512(key, msg []byte) []byte { return mac(new512(), key, msg) }

// mac follows RFC 2104: H((K ^ opad) || H((K ^ ipad) || msg)), where a key
// longer than a block is first replaced by its hash.
func mac(d digest, key, msg []byte) []byte {
	bs := d.blockSize()
	if len(key) > bs {
		key = sum(d, key)
		d.reset()
	}
	ipad := make([]byte, bs)
	opad := make([]byte, bs)
	copy(ipad, key)
	copy(opad, key)
	for i := range ipad {
		ipad[i] ^= 0x36
		opad[i] ^= 0x5c
	}
	d.write(ipad)
	inner := sum(d, msg)
	d.reset()
	d.write(opad)
	return sum(d, inner)
}
