package runtime_test

import (
	"encoding/binary"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"testing"
)

func FuzzRuntimeFrameDecode(f *testing.F) {
	for _, value := range []string{`{"protocol_version":1,"request_id":"x","operation":"hello","role":"management"}`, `{"protocol_version":2}`, `{"protocol_version":1,"protocol_version":1}`, `null`, `[]`} {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(value)))
		f.Add(append(prefix[:], []byte(value)...))
	}
	f.Add([]byte{255, 255, 255, 255})
	f.Add([]byte{})
	f.Fuzz(func(_ *testing.T, data []byte) { _ = infra.ValidateRuntimeFrameForTest(data) })
}
