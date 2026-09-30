package cloooud

import (
	"os"
	"strings"
	"testing"
)

func TestRedactCloooudCredentialsFromRequestLog(t *testing.T) {
	for _, path := range []string{"/api/auth/cloooud/start", "/api/auth/cloooud/callback"} {
		if got := RedactRequestPath(path + "?code=secret&state=secret"); got != path {
			t.Fatalf("SSO query leaked: %s", got)
		}
	}
	original := "/api/public/canvas-shares/secret/file"
	if got := RedactRequestPath(original); got != original {
		t.Fatal("module must leave other redaction to host")
	}
}

func TestExtendHostOpenAPI(t *testing.T) {
	base, err := os.ReadFile("../../handler/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(base), "\npaths:\n") != 1 {
		t.Fatal("host OpenAPI insertion point changed")
	}
	combined := string(ExtendOpenAPI(base))
	for _, path := range []string{"/auth/cloooud/start:", "/auth/cloooud/callback:"} {
		if strings.Count(combined, path) != 1 {
			t.Fatalf("missing or duplicate SSO operation: %s", path)
		}
	}
	if strings.Replace(combined, openAPIPaths, "", 1) != string(base) {
		t.Fatal("module changed host API contract")
	}
}
