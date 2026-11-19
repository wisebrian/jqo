package sha2

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"testing"
)

func unhex(t testing.TB, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Known answers from FIPS 180-4's examples and RFC 4231.
func TestVectors(t *testing.T) {
	abc := []byte("abc")
	for name, tc := range map[string]struct {
		got  []byte
		want string
	}{
		"SHA-256 abc":   {Sum256(abc), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		"SHA-384 abc":   {Sum384(abc), "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7"},
		"SHA-512 abc":   {Sum512(abc), "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		"SHA-256 empty": {Sum256(nil), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		"RFC 4231 1 SHA-256": {HMAC256(bytes.Repeat([]byte{0x0b}, 20), []byte("Hi There")),
			"b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"},
		"RFC 4231 1 SHA-384": {HMAC384(bytes.Repeat([]byte{0x0b}, 20), []byte("Hi There")),
			"afd03944d84895626b0825f4ab46907f15f9dadbe4101ec682aa034c7cebc59cfaea9ea9076ede7f4af152e8b2fa9cb6"},
		"RFC 4231 1 SHA-512": {HMAC512(bytes.Repeat([]byte{0x0b}, 20), []byte("Hi There")),
			"87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cdedaa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854"},
		"RFC 4231 2 SHA-256": {HMAC256([]byte("Jefe"), []byte("what do ya want for nothing?")),
			"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
	} {
		if !bytes.Equal(tc.got, unhex(t, tc.want)) {
			t.Errorf("%s = %x, want %s", name, tc.got, tc.want)
		}
	}
}

func check(t testing.TB, key, msg []byte) {
	for _, tc := range []struct {
		name string
		sum  func([]byte) []byte
		mac  func(key, msg []byte) []byte
		std  func() hash.Hash
	}{
		{"256", Sum256, HMAC256, sha256.New},
		{"384", Sum384, HMAC384, sha512.New384},
		{"512", Sum512, HMAC512, sha512.New},
	} {
		h := tc.std()
		h.Write(msg)
		if got := tc.sum(msg); !bytes.Equal(got, h.Sum(nil)) {
			t.Fatalf("SHA-%s of %d bytes: %x, crypto says %x", tc.name, len(msg), got, h.Sum(nil))
		}
		m := hmac.New(tc.std, key)
		m.Write(msg)
		if got := tc.mac(key, msg); !bytes.Equal(got, m.Sum(nil)) {
			t.Fatalf("HMAC-SHA-%s with a %d-byte key over %d bytes: %x, crypto says %x", tc.name, len(key), len(msg), got, m.Sum(nil))
		}
	}
}

// Every length around the block and padding boundaries, and keys shorter
// than, equal to and longer than a block.
func TestMatchesCrypto(t *testing.T) {
	data := make([]byte, 600)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	for n := 0; n <= len(data); n++ {
		for _, k := range []int{0, 1, 32, 64, 65, 128, 129, 300} {
			check(t, data[:k], data[:n])
		}
	}
}

func FuzzHMAC(f *testing.F) {
	f.Add([]byte("key"), []byte("message"))
	f.Add(bytes.Repeat([]byte{1}, 200), bytes.Repeat([]byte{2}, 300))
	f.Fuzz(func(t *testing.T, key, msg []byte) { check(t, key, msg) })
}
