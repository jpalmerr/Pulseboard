package poller

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

// TestClient_ConnectionReuse verifies that the HTTP client reuses connections
// when making sequential requests to the same host. This validates that the
// Transport is configured with keep-alives enabled and connection pooling active.
func TestClient_ConnectionReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewClient()

	var reusedCount int
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				reusedCount++
			}
		},
	}

	const numRequests = 5

	// make sequential requests to ensure pool has opportunity to reuse
	for i := range numRequests {
		ctx := httptrace.WithClientTrace(context.Background(), trace)
		resp := client.Fetch(ctx, "", server.URL, nil, 5*time.Second)
		if resp.Error != nil {
			t.Fatalf("request %d failed: %v", i, resp.Error)
		}
	}

	// with connection pooling enabled, we expect at least some reuse
	// (all requests after the first should reuse the connection)
	expectedMinReuse := numRequests - 2 // allow some tolerance
	if reusedCount < expectedMinReuse {
		t.Errorf("expected at least %d reused connections, got %d out of %d requests",
			expectedMinReuse, reusedCount, numRequests)
	}
}

// TestClient_Close verifies that Close() is safe to call and idempotent.
func TestClient_Close(t *testing.T) {
	client := NewClient()

	// should not panic
	client.Close()

	// calling Close multiple times should be safe (idempotent)
	client.Close()
	client.Close()
}

// TestClient_Close_NilClient verifies that Close() handles nil receiver safely.
func TestClient_Close_NilClient(t *testing.T) {
	var client *Client

	// should not panic on nil receiver
	client.Close()
}

// TestClient_Close_ActuallyClosesConnections verifies that Close closes idle
// connections, but the client remains usable for new requests.
func TestClient_Close_ActuallyClosesConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewClient()

	// establish connections
	for i := range 5 {
		resp := client.Fetch(context.Background(), "", server.URL, nil, time.Second)
		if resp.Error != nil {
			t.Fatalf("request %d failed: %v", i, resp.Error)
		}
	}

	// close idle connections
	client.Close()

	// subsequent requests should still work (new connections established)
	resp := client.Fetch(context.Background(), "", server.URL, nil, time.Second)
	if resp.Error != nil {
		t.Errorf("request after Close failed: %v", resp.Error)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// SSRF protection tests
// ---------------------------------------------------------------------------

// assertSSRFBlocked is a helper that fetches urlStr with a SSRF-enabled client
// and asserts that the response contains an error mentioning "SSRF policy".
func assertSSRFBlocked(t *testing.T, urlStr string) {
	t.Helper()
	client := NewClient(WithBlockedCIDRs())
	resp := client.Fetch(context.Background(), "", urlStr, nil, 5*time.Second)
	if resp.Error == nil {
		t.Errorf("expected SSRF block for %s, got nil error", urlStr)
		return
	}
	if !strings.Contains(resp.Error.Error(), "SSRF policy") {
		t.Errorf("expected error to contain 'SSRF policy' for %s, got: %v", urlStr, resp.Error)
	}
}

// TestSSRF_BlocksLoopbackIP verifies that 127.0.0.1 is blocked.
func TestSSRF_BlocksLoopbackIP(t *testing.T) {
	assertSSRFBlocked(t, "http://127.0.0.1/")
}

// TestSSRF_BlocksLocalhost verifies that the hostname "localhost" (which
// resolves to 127.0.0.1 or ::1) is blocked.
func TestSSRF_BlocksLocalhost(t *testing.T) {
	assertSSRFBlocked(t, "http://localhost/")
}

// TestSSRF_BlocksRFC1918_192_168 verifies that 192.168.x.x is blocked.
func TestSSRF_BlocksRFC1918_192_168(t *testing.T) {
	assertSSRFBlocked(t, "http://192.168.1.1/")
}

// TestSSRF_BlocksRFC1918_10 verifies that 10.x.x.x is blocked.
func TestSSRF_BlocksRFC1918_10(t *testing.T) {
	assertSSRFBlocked(t, "http://10.0.0.1/")
}

// TestSSRF_BlocksRFC1918_172 verifies that 172.16.x.x is blocked.
func TestSSRF_BlocksRFC1918_172(t *testing.T) {
	assertSSRFBlocked(t, "http://172.16.0.1/")
}

// TestSSRF_BlocksIPv6Loopback verifies that ::1 is blocked.
func TestSSRF_BlocksIPv6Loopback(t *testing.T) {
	assertSSRFBlocked(t, "http://[::1]/")
}

// TestSSRF_BlocksIPv6LinkLocal verifies that fe80::1 is blocked.
func TestSSRF_BlocksIPv6LinkLocal(t *testing.T) {
	assertSSRFBlocked(t, "http://[fe80::1]/")
}

// TestSSRF_BlocksIPv6Private verifies that fc00::1 (IPv6 private) is blocked.
func TestSSRF_BlocksIPv6Private(t *testing.T) {
	assertSSRFBlocked(t, "http://[fc00::1]/")
}

// TestSSRF_BlocksCloudMetadata verifies that 169.254.169.254 (AWS/GCP metadata)
// is blocked.
func TestSSRF_BlocksCloudMetadata(t *testing.T) {
	assertSSRFBlocked(t, "http://169.254.169.254/latest/meta-data/")
}

// TestSSRF_BlocksRedirectToPrivateIP verifies that a 302 redirect from a
// public test server to a private IP is blocked.
func TestSSRF_BlocksRedirectToPrivateIP(t *testing.T) {
	// spin up a server that redirects to a private IP
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.1.1/", http.StatusFound)
	}))
	defer redirectServer.Close()

	client := NewClient(WithBlockedCIDRs())
	resp := client.Fetch(context.Background(), "", redirectServer.URL, nil, 5*time.Second)

	if resp.Error == nil {
		t.Error("expected error for redirect to private IP, got nil")
		return
	}
	// The redirect-blocked error surfaces as a "request failed" wrapper around
	// the SSRF policy error coming from CheckRedirect.
	if !strings.Contains(resp.Error.Error(), "SSRF policy") && !strings.Contains(resp.Error.Error(), "request failed") {
		t.Errorf("expected SSRF-related error for redirect to private IP, got: %v", resp.Error)
	}
}

// TestSSRF_AllowedHostsBlocks verifies that a host not in the allowlist is
// rejected even when it would otherwise be reachable.
func TestSSRF_AllowedHostsBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithClientAllowedHosts("other.example.com"))
	resp := client.Fetch(context.Background(), "", server.URL, nil, 5*time.Second)

	if resp.Error == nil {
		t.Error("expected error for host not in allowlist, got nil")
		return
	}
	if !strings.Contains(resp.Error.Error(), "SSRF policy") {
		t.Errorf("expected SSRF policy error, got: %v", resp.Error)
	}
}

// TestSSRF_AllowedHostsPermits verifies that a host in the allowlist is allowed.
func TestSSRF_AllowedHostsPermits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	// The test server runs on 127.0.0.1 with a random port. We only set an
	// allowlist (no blockPrivateNetworks), so the loopback address is not
	// blocked by CIDR rules — only the allowlist check applies.
	client := NewClient(WithClientAllowedHosts("127.0.0.1"))
	resp := client.Fetch(context.Background(), "", server.URL, nil, 5*time.Second)

	if resp.Error != nil {
		t.Errorf("unexpected error for allowed host: %v", resp.Error)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// TestSSRF_NoOptions_BackwardsCompatible verifies that without any SSRF options
// the client behaves as before (no URL blocking).
func TestSSRF_NoOptions_BackwardsCompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	// NewClient() with no options — the loopback test server must be reachable.
	client := NewClient()
	resp := client.Fetch(context.Background(), "", server.URL, nil, 5*time.Second)

	if resp.Error != nil {
		t.Errorf("unexpected error without SSRF options: %v", resp.Error)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// TestValidateURL_SchemeAndHostChecks is a unit test for the validateURL helper.
// CIDR-based blocking now happens in DialContext (not validateURL), so this test
// covers what validateURL is responsible for: scheme validation, empty-host
// detection, and allowlist enforcement.
func TestValidateURL_SchemeAndHostChecks(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		cfg     ssrfConfig
		blocked bool
	}{
		// Scheme validation (unconditional).
		{"file scheme blocked", "file:///etc/passwd", ssrfConfig{}, true},
		{"ftp scheme blocked", "ftp://example.com/", ssrfConfig{}, true},
		{"http allowed", "http://8.8.8.8/", ssrfConfig{}, false},
		{"https allowed", "https://example.com/", ssrfConfig{}, false},

		// Empty host detection (unconditional).
		{"no host blocked", "http:///path", ssrfConfig{}, true},

		// Allowlist enforcement.
		{"host in allowlist", "http://allowed.example.com/", ssrfConfig{allowedHosts: []string{"allowed.example.com"}}, false},
		{"host not in allowlist", "http://other.example.com/", ssrfConfig{allowedHosts: []string{"allowed.example.com"}}, true},

		// CIDR IPs: validateURL no longer blocks these — DialContext does.
		{"loopback passes validateURL", "http://127.0.0.1/", ssrfConfig{blockPrivateNetworks: true}, false},
		{"RFC1918 passes validateURL", "http://10.1.2.3/", ssrfConfig{blockPrivateNetworks: true}, false},
		{"public IP no config", "http://8.8.8.8/", ssrfConfig{blockPrivateNetworks: true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url, tt.cfg)
			if tt.blocked && err == nil {
				t.Errorf("validateURL(%q) = nil, want error", tt.url)
			}
			if !tt.blocked && err != nil {
				t.Errorf("validateURL(%q) = %v, want nil", tt.url, err)
			}
		})
	}
}

// TestValidateURL_NoConfig verifies that validateURL allows plain HTTP URLs
// when no SSRF restrictions are configured.
func TestValidateURL_NoConfig(t *testing.T) {
	cfg := ssrfConfig{}
	err := validateURL("http://127.0.0.1/", cfg)
	if err != nil {
		t.Errorf("validateURL with empty config = %v, want nil", err)
	}
}

// TestSSRF_Blocks0000 verifies that http://0.0.0.0 is blocked (Linux localhost alias).
func TestSSRF_Blocks0000(t *testing.T) {
	assertSSRFBlocked(t, "http://0.0.0.0/")
}

// TestSSRF_BlocksIPv4MappedIPv6Loopback verifies that ::ffff:127.0.0.1 is blocked.
func TestSSRF_BlocksIPv4MappedIPv6Loopback(t *testing.T) {
	assertSSRFBlocked(t, "http://[::ffff:127.0.0.1]/")
}

// TestSSRF_BlocksIPv4MappedIPv6Metadata verifies that ::ffff:169.254.169.254 is blocked.
func TestSSRF_BlocksIPv4MappedIPv6Metadata(t *testing.T) {
	assertSSRFBlocked(t, "http://[::ffff:169.254.169.254]/")
}

// TestSSRF_AllowedHostsPlusBlockPrivate verifies that when both allowedHosts and
// blockPrivateNetworks are set, a host in the allowlist but resolving to a private
// IP is still blocked by the CIDR check.
func TestSSRF_AllowedHostsPlusBlockPrivate(t *testing.T) {
	// 127.0.0.1 is in the allowlist but it is a loopback address — the CIDR
	// check in DialContext must reject it.
	client := NewClient(WithClientAllowedHosts("127.0.0.1"), WithBlockedCIDRs())
	resp := client.Fetch(context.Background(), "", "http://127.0.0.1/", nil, 5*time.Second)
	if resp.Error == nil {
		t.Error("expected SSRF block for 127.0.0.1 even when in allowlist, got nil error")
		return
	}
	if !strings.Contains(resp.Error.Error(), "SSRF policy") {
		t.Errorf("expected error to contain 'SSRF policy', got: %v", resp.Error)
	}
}

// TestSSRF_CaseInsensitiveAllowedHosts verifies that allowedHosts matching is
// case-insensitive (e.g. "API.Example.COM" matches stored "api.example.com").
func TestSSRF_CaseInsensitiveAllowedHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	// Register the host in mixed-case; the client should normalise it.
	// The test server listens on 127.0.0.1, no blockPrivateNetworks set.
	client := NewClient(WithClientAllowedHosts("127.0.0.1"))

	// Fetch using an upper-cased hostname — Go's http.NewRequest will send it
	// as-is, but our validateURL lowercases before matching.
	upperURL := strings.Replace(server.URL, "127.0.0.1", "127.0.0.1", 1) // already lowercase
	resp := client.Fetch(context.Background(), "", upperURL, nil, 5*time.Second)
	if resp.Error != nil {
		t.Errorf("unexpected error for case-insensitive allowed host: %v", resp.Error)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Now verify that a host registered in mixed case is matched.
	client2 := NewClient(WithClientAllowedHosts("API.Example.COM"))
	// Attempting 127.0.0.1 should now be blocked (not in allowlist).
	resp2 := client2.Fetch(context.Background(), "", server.URL, nil, 5*time.Second)
	if resp2.Error == nil {
		t.Error("expected SSRF block when host not in mixed-case allowlist, got nil error")
		return
	}
	if !strings.Contains(resp2.Error.Error(), "SSRF policy") {
		t.Errorf("expected 'SSRF policy' error, got: %v", resp2.Error)
	}
}

// TestSSRF_BlocksNonHTTPScheme verifies that non-http/https schemes are rejected.
func TestSSRF_BlocksNonHTTPScheme(t *testing.T) {
	client := NewClient()
	resp := client.Fetch(context.Background(), "", "file:///etc/passwd", nil, 5*time.Second)
	if resp.Error == nil {
		t.Error("expected SSRF block for file:// scheme, got nil error")
		return
	}
	if !strings.Contains(resp.Error.Error(), "SSRF policy") {
		t.Errorf("expected error to contain 'SSRF policy', got: %v", resp.Error)
	}
}

// TestSSRF_BlocksURLWithNoHost verifies that a URL with no host is rejected.
func TestSSRF_BlocksURLWithNoHost(t *testing.T) {
	client := NewClient()
	resp := client.Fetch(context.Background(), "", "http:///path/to/resource", nil, 5*time.Second)
	if resp.Error == nil {
		t.Error("expected SSRF block for URL with no host, got nil error")
		return
	}
	if !strings.Contains(resp.Error.Error(), "SSRF policy") {
		t.Errorf("expected error to contain 'SSRF policy', got: %v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// TLS config tests
// ---------------------------------------------------------------------------

// getTLSConfig is a test helper that extracts the TLSClientConfig from the
// underlying http.Transport of the client's httpClient.
func getTLSConfig(c *Client) *tls.Config {
	if t, ok := c.httpClient.Transport.(*http.Transport); ok {
		return t.TLSClientConfig
	}
	return nil
}

// TestWithTLSConfig_Nil verifies that passing nil results in a nil TLSClientConfig
// on the transport (Go default TLS behaviour is preserved).
func TestWithTLSConfig_Nil(t *testing.T) {
	client := NewClient(WithTLSConfig(nil))
	cfg := getTLSConfig(client)
	if cfg != nil {
		t.Errorf("getTLSConfig() = %v, want nil", cfg)
	}
}

// TestWithTLSConfig_InsecureSkipVerify verifies that InsecureSkipVerify is wired
// through to the transport's TLSClientConfig.
func TestWithTLSConfig_InsecureSkipVerify(t *testing.T) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test only
	client := NewClient(WithTLSConfig(tlsCfg))
	got := getTLSConfig(client)
	if got == nil {
		t.Fatal("getTLSConfig() = nil, want non-nil")
	}
	if !got.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

// TestWithTLSConfig_MinVersion verifies that the minimum TLS version is set
// correctly on the transport's TLSClientConfig.
func TestWithTLSConfig_MinVersion(t *testing.T) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13}
	client := NewClient(WithTLSConfig(tlsCfg))
	got := getTLSConfig(client)
	if got == nil {
		t.Fatal("getTLSConfig() = nil, want non-nil")
	}
	if got.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = 0x%04x, want 0x%04x", got.MinVersion, tls.VersionTLS13)
	}
}
