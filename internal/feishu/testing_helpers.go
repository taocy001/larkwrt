//go:build !production

package feishu

// Test-only exports for the integration test mock server.

func MarshalFrameForTest(f Frame) []byte    { return marshalFrame(f) }
func UnmarshalFrameForTest(b []byte) (Frame, error) { return unmarshalFrame(b) }
