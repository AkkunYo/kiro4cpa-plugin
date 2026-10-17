package kiro

import (
	"strings"
	"testing"
)

func TestLogoDataURI(t *testing.T) {
	uri := logoDataURI()
	if !strings.HasPrefix(uri, "data:image/svg+xml;base64,") {
		t.Fatalf("expected data:image/svg+xml;base64 prefix, got: %s", uri[:30])
	}
	if len(uri) < 100 {
		t.Fatalf("logo URI too short: %d bytes", len(uri))
	}
}
