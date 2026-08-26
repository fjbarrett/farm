package farm

import "testing"

func TestRequireLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7331", "[::1]:7331", "localhost:7331"} {
		if err := requireLoopback(address); err != nil {
			t.Fatalf("expected %s to be allowed: %v", address, err)
		}
	}
	if err := requireLoopback("0.0.0.0:7331"); err == nil {
		t.Fatal("public binding must be rejected until authentication exists")
	}
}
