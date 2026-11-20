package jqo

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

// sampleDoc builds a realistic document of about size bytes: an array of
// records mixing every kind of value, with non-ASCII text and escapes.
func sampleDoc(size int) []byte {
	var b bytes.Buffer
	b.Grow(size + 512)
	b.WriteString("[")
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"User %d","email":"user%d@example.com","active":%t,"score":%d.%d,`+
			`"tags":["alpha","beta","gamma"],"address":{"city":"București","zip":"0%05d","geo":[44.43,26.1]},`+
			`"bio":"line one\nline \"two\" `+"\\u00e9"+`","manager":null}`,
			i, i, i, i%2 == 0, i%100, i%10, i)
	}
	b.WriteString("]")
	return b.Bytes()
}

var docs sync.Map // size → []byte, so benchmarks don't rebuild them

func doc(size int) []byte {
	if d, ok := docs.Load(size); ok {
		return d.([]byte)
	}
	d := sampleDoc(size)
	docs.Store(size, d)
	return d
}

func benchParse(b *testing.B, size int) {
	data := doc(size)
	opts := Options{MaxSize: len(data)}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(data, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse4MB(b *testing.B)  { benchParse(b, 4<<20) }
func BenchmarkParse20MB(b *testing.B) { benchParse(b, 20<<20) }
