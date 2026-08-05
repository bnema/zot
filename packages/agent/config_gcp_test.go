package agent

import (
	"os"
	"path/filepath"
	"testing"

	providerauth "github.com/bnema/zut/packages/provider/auth"
)

func isolateGoogleVertexADC(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("APPDATA", root)
	path, err := providerauth.GoogleApplicationDefaultCredentialsPath()
	if err != nil {
		t.Fatalf("GoogleApplicationDefaultCredentialsPath: %v", err)
	}
	return path
}

// TestResolveCredentialGoogleVertexAPIKey confirms that GOOGLE_CLOUD_API_KEY
// takes top priority for the google-vertex provider and is returned with
// method "apikey".
func TestResolveCredentialGoogleVertexAPIKey(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	isolateGoogleVertexADC(t)
	t.Setenv("GOOGLE_CLOUD_API_KEY", "gcp-key-123")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	cred, method, _, err := ResolveCredentialFull("google-vertex", "")
	if err != nil {
		t.Fatalf("ResolveCredentialFull failed: %v", err)
	}
	if cred != "gcp-key-123" {
		t.Errorf("cred = %q, want %q", cred, "gcp-key-123")
	}
	if method != "apikey" {
		t.Errorf("method = %q, want apikey", method)
	}
}

// TestResolveCredentialGoogleVertexApplicationCredentialsEnv confirms that
// when GOOGLE_APPLICATION_CREDENTIALS is set (service account JSON path),
// resolution returns the "<adc>" sentinel with method "apikey", matching
// the real NewVertex client's ADC-based auth flow.
func TestResolveCredentialGoogleVertexApplicationCredentialsEnv(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	isolateGoogleVertexADC(t)
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/path/to/service-account.json")

	cred, method, _, err := ResolveCredentialFull("google-vertex", "")
	if err != nil {
		t.Fatalf("ResolveCredentialFull failed: %v", err)
	}
	if cred != "<adc>" {
		t.Errorf("cred = %q, want <adc>", cred)
	}
	if method != "apikey" {
		t.Errorf("method = %q, want apikey", method)
	}
}

// TestResolveCredentialGoogleVertexDefaultADCFile confirms that, absent any
// env vars, the presence of the platform-specific default gcloud ADC file is
// detected and resolves to the "<adc>" sentinel.
func TestResolveCredentialGoogleVertexDefaultADCFile(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	adcPath := isolateGoogleVertexADC(t)
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	if err := os.MkdirAll(filepath.Dir(adcPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adcPath, []byte(`{"type":"authorized_user"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cred, method, _, err := ResolveCredentialFull("google-vertex", "")
	if err != nil {
		t.Fatalf("ResolveCredentialFull failed: %v", err)
	}
	if cred != "<adc>" {
		t.Errorf("cred = %q, want <adc>", cred)
	}
	if method != "apikey" {
		t.Errorf("method = %q, want apikey", method)
	}
}

// TestResolveCredentialGoogleVertexPrecedence confirms GOOGLE_CLOUD_API_KEY
// wins over GOOGLE_APPLICATION_CREDENTIALS and the default ADC file when
// multiple credential sources are present simultaneously.
func TestResolveCredentialGoogleVertexPrecedence(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	adcPath := isolateGoogleVertexADC(t)
	t.Setenv("GOOGLE_CLOUD_API_KEY", "explicit-key")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/path/to/sa.json")

	if err := os.MkdirAll(filepath.Dir(adcPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adcPath, []byte(`{"type":"authorized_user"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cred, method, _, err := ResolveCredentialFull("google-vertex", "")
	if err != nil {
		t.Fatalf("ResolveCredentialFull failed: %v", err)
	}
	if cred != "explicit-key" {
		t.Errorf("cred = %q, want explicit-key (API key must win over ADC)", cred)
	}
	if method != "apikey" {
		t.Errorf("method = %q, want apikey", method)
	}
}

// TestResolveCredentialGoogleVertexNoCredentials confirms that when none of
// the Vertex credential sources are present (no env vars, no ADC file, no
// auth.json entry), resolution fails with an error rather than silently
// returning an empty credential.
func TestResolveCredentialGoogleVertexNoCredentials(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	isolateGoogleVertexADC(t)
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	cred, method, _, err := ResolveCredentialFull("google-vertex", "")
	if err == nil {
		t.Fatalf("expected error, got cred=%q method=%q", cred, method)
	}
	if cred != "" || method != "" {
		t.Errorf("expected empty cred/method on error, got cred=%q method=%q", cred, method)
	}
}
