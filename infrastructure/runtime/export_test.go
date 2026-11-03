package runtime

import "bytes"

// ValidateRuntimeFrameForTest exposes only the bounded decoder to external fuzz tests.
func ValidateRuntimeFrameForTest(data []byte) error {
	payload, err := readRuntimeFrame(bytes.NewReader(data))
	if err != nil {
		return err
	}
	var request runtimeRequest
	return decodeRuntimeJSON(payload, &request)
}
