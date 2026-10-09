package gatewaypolicy

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/peer"
)

func servePolicy(t *testing.T, cfg Config, trust bool) (*http.Client, string) {
	t.Helper()
	policy, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(os.TempDir(), "fpg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(directory, "s")
	base, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := peer.NewListener(base, []uint32{uint32(os.Getuid())}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, rejection := policy.Check(r)
		if rejection != nil {
			WriteRejection(w, r, rejection)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_gateway/grant") {
			grant, rejection := policy.IssueGrant(r, "/app/example/ws")
			if rejection != nil {
				WriteRejection(w, r, rejection)
				return
			}
			WriteGrant(w, grant)
			return
		}
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			key := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			response := &http.Response{StatusCode: 101, Header: make(http.Header), Request: r}
			if protocol := r.Header.Get("Sec-WebSocket-Protocol"); protocol != "" {
				response.Header.Set("Sec-WebSocket-Protocol", protocol)
			}
			if err := CompleteHandshake(response); err != nil {
				t.Error(err)
				return
			}
			protocolHeader := ""
			if protocol := response.Header.Get("Sec-WebSocket-Protocol"); protocol != "" {
				protocolHeader = "Sec-WebSocket-Protocol: " + protocol + "\r\n"
			}
			fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", base64.StdEncoding.EncodeToString(key[:]), protocolHeader)
			buffer.Flush()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"schema":1,"status":"ready"}`)
	})}
	if trust {
		server.ConnContext = trusted.ConnContext
	}
	go server.Serve(trusted)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	t.Cleanup(func() { transport.CloseIdleConnections(); server.Close(); trusted.CloseConnections() })
	return client, "http://internal.example/app/example"
}

func TestAuthenticatedOriginBoundary(t *testing.T) {
	cfg := Config{Prefix: "/app/example", AllowSameOriginFetch: true, ExternalOriginPorts: map[string][]uint16{"http": {5666}, "https": {5667}}}
	client, base := servePolicy(t, cfg, true)
	tests := []struct {
		name, method, origin, site string
		ws                         bool
		code                       string
	}{
		{"strict exact", "POST", "http://internal.example", "", false, ""},
		{"configured local port", "POST", "https://internal.example:5667", "", false, ""},
		{"unknown relay authority", "POST", "https://public.example:18443", "same-origin", false, ""},
		{"relay websocket", "GET", "https://public.example:18443", "same-origin", true, ""},
		{"unconfigured port missing metadata", "POST", "https://internal.example:18443", "", false, "origin_mismatch"},
		{"relay missing metadata", "POST", "https://public.example:18443", "", false, "origin_mismatch"},
		{"same site even exact origin", "POST", "http://internal.example", "same-site", false, "cross_site"},
		{"cross site even exact origin", "POST", "http://internal.example", "cross-site", false, "cross_site"},
		{"null despite same origin", "POST", "null", "same-origin", false, "invalid_origin"},
		{"list despite same origin", "POST", "https://one.example https://two.example", "same-origin", false, "invalid_origin"},
		{"userinfo", "POST", "https://secret@public.example", "same-origin", false, "invalid_origin"},
		{"path", "POST", "https://public.example/", "same-origin", false, "invalid_origin"},
		{"query", "POST", "https://public.example?secret", "same-origin", false, "invalid_origin"},
		{"fragment", "POST", "https://public.example#secret", "same-origin", false, "invalid_origin"},
		{"missing WS Origin", "GET", "", "same-origin", true, "origin_required"},
		{"none WS", "GET", "https://public.example", "none", true, "invalid_fetch_metadata"},
		{"none POST", "POST", "", "none", false, "invalid_fetch_metadata"},
		{"unknown site", "POST", "http://internal.example", "unknown", false, "invalid_fetch_metadata"},
		{"merged site", "POST", "http://internal.example", "same-origin, cross-site", false, "invalid_fetch_metadata"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request, _ := http.NewRequest(tc.method, base+"/ws", nil)
			request.Header.Set("X-Trim-Userid", "1000")
			request.Header.Set("X-Trim-Username", "example-user")
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				request.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.ws {
				request.Header.Set("Connection", "Upgrade")
				request.Header.Set("Upgrade", "websocket")
				request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			expected := http.StatusOK
			if tc.ws {
				expected = http.StatusSwitchingProtocols
			}
			if tc.code != "" {
				expected = http.StatusForbidden
			}
			if response.StatusCode != expected || response.Header.Get("X-FnPackKit-Rejection") != tc.code {
				t.Fatalf("status=%d code=%s", response.StatusCode, response.Header.Get("X-FnPackKit-Rejection"))
			}
		})
	}
	for _, name := range []string{"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "X-Trim-Userid"} {
		t.Run("duplicate "+name, func(t *testing.T) {
			request, _ := http.NewRequest("POST", base+"/_gateway/status", nil)
			request.Header.Set("X-Trim-Userid", "1000")
			request.Header.Set("X-Trim-Username", "example-user")
			request.Header.Set("Origin", "https://public.example")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			if name == "Sec-Fetch-Mode" {
				request.Header.Set(name, "cors")
			}
			if name == "Sec-Fetch-Dest" {
				request.Header.Set(name, "font")
			}
			request.Header.Add(name, request.Header.Get(name))
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 403 {
				t.Fatal("duplicate accepted")
			}
		})
	}
	t.Run("font cors same origin", func(t *testing.T) {
		request, _ := http.NewRequest("GET", base+"/static/font.woff2", nil)
		request.Header.Set("X-Trim-Userid", "1000")
		request.Header.Set("X-Trim-Username", "example-user")
		request.Header.Set("Origin", "https://relay.example:2443")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Sec-Fetch-Mode", "cors")
		request.Header.Set("Sec-Fetch-Dest", "font")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("same-origin font rejected")
		}
	})
}

func TestTransportAndConfigurationBoundary(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := Config{Prefix: "/app/example", AllowSameOriginFetch: enabled}
		client, base := servePolicy(t, cfg, false)
		request, _ := http.NewRequest("POST", base+"/_gateway/status", nil)
		request.Header.Set("X-Trim-Userid", "1000")
		request.Header.Set("X-Trim-Username", "example-user")
		request.Header.Set("Origin", "https://relay.example")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.Header.Get("X-FnPackKit-Rejection") != "untrusted_peer" {
			t.Fatal("unmarked transport accepted")
		}
	}
	for _, origins := range [][]string{{"https://*.example"}, {"null"}, {"https://example/path"}, {"https://example?"}} {
		if _, err := New(Config{Prefix: "/app/example", AllowedOrigins: origins}); err == nil {
			t.Fatalf("invalid configured origins: %v", origins)
		}
	}
	cfg := Config{Prefix: "/app/example", AllowedOrigins: []string{"https://relay.example:2443"}}
	client, base := servePolicy(t, cfg, true)
	for _, site := range []string{"", "same-origin", "same-site"} {
		request, _ := http.NewRequest("POST", base+"/_gateway/status", nil)
		request.Header.Set("X-Trim-Userid", "1000")
		request.Header.Set("X-Trim-Username", "example-user")
		request.Header.Set("Origin", "https://relay.example:2443")
		if site != "" {
			request.Header.Set("Sec-Fetch-Site", site)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		expected := 200
		if site == "same-site" {
			expected = 403
		}
		if response.StatusCode != expected {
			t.Fatal("explicit origin policy mismatch")
		}
	}
}

func TestDiagnosticRedaction(t *testing.T) {
	request := httptest.NewRequest("POST", "https://private.example:1234/app/example/ws?credential=secret", nil)
	request.Header.Set("Origin", "https://public-secret.example:2443")
	request.Header.Set("Cookie", "secret-cookie")
	request.Header.Set("Authorization", "secret-auth")
	request.Header.Set("X-FnPackKit-Request-ID", "attacker-id")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "untrusted\nline")
	rejection := &Rejection{Code: "origin_mismatch"}
	data, err := json.Marshal(Audit(request, rejection))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private.example", "public-secret.example", "secret-cookie", "secret-auth", "credential", "untrusted", "attacker-id"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("diagnostics leaked %q", secret)
		}
	}
	recorder := httptest.NewRecorder()
	id := WriteRejection(recorder, request, rejection)
	if len(id) != 32 || id == "attacker-id" || !strings.Contains(recorder.Body.String(), id) || recorder.Code != 403 {
		t.Fatal("unsafe diagnostic ID or response")
	}
	for _, status := range []int{403, 502, 503} {
		recorder := httptest.NewRecorder()
		WriteFailure(recorder, request, &Rejection{Code: "proxy_failure"}, status)
		if recorder.Code != status {
			t.Fatal("failure status lost")
		}
	}
}

func TestStrictFallbackAndConstraints(t *testing.T) {
	cases := []struct {
		name, host, origin, site, mode, requestPath string
		method                                      string
		enabled                                     bool
		ports                                       map[string][]uint16
		hosts                                       []string
		code                                        string
	}{
		{name: "explicit flag disabled", host: "internal.example", origin: "https://public.example:2443", site: "same-origin", method: "POST", code: "origin_mismatch"},
		{name: "allowed host still binds", host: "internal.example", origin: "https://public.example:2443", site: "same-origin", method: "POST", enabled: true, hosts: []string{"other.example"}, code: "invalid_host"},
		{name: "prefix still binds", host: "internal.example", origin: "https://public.example", site: "same-origin", method: "POST", enabled: true, requestPath: "/app/other", code: "invalid_path"},
		{name: "none navigation", host: "internal.example", site: "none", mode: "navigate", method: "GET"},
		{name: "none is not fetch", host: "internal.example", site: "none", mode: "cors", method: "GET", code: "invalid_fetch_metadata"},
		{name: "no metadata same host wrong port", host: "internal.example", origin: "http://internal.example:8080", method: "POST", ports: map[string][]uint16{"http": {5666}}, code: "origin_mismatch"},
		{name: "scheme specific ports", host: "internal.example", origin: "https://internal.example:5666", method: "POST", ports: map[string][]uint16{"http": {5666}}, code: "origin_mismatch"},
		{name: "normalized NAS hostname", host: "INTERNAL.EXAMPLE.", origin: "https://internal.example:5667", method: "POST", ports: map[string][]uint16{"https": {5667}}},
		{name: "IPv6 NAS port", host: "[2001:db8::1]", origin: "https://[2001:db8::1]:5667", method: "POST", ports: map[string][]uint16{"https": {5667}}},
		{name: "explicit mismatched host port", host: "internal.example:8080", origin: "http://internal.example:5666", method: "POST", ports: map[string][]uint16{"http": {5666}}, code: "origin_mismatch"},
		{name: "noncanonical configured port", host: "internal.example", origin: "http://internal.example:05666", method: "POST", ports: map[string][]uint16{"http": {5666}}, code: "origin_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, base := servePolicy(t, Config{Prefix: "/app/example", AllowedHosts: tc.hosts, AllowSameOriginFetch: tc.enabled, ExternalOriginPorts: tc.ports}, true)
			url := base + "/_gateway/status"
			if tc.requestPath != "" {
				url = "http://internal.example" + tc.requestPath
			}
			request, _ := http.NewRequest(tc.method, url, nil)
			request.Host = tc.host
			request.Header.Set("X-Trim-Userid", "1000")
			request.Header.Set("X-Trim-Username", "example-user")
			for name, value := range map[string]string{"Origin": tc.origin, "Sec-Fetch-Site": tc.site, "Sec-Fetch-Mode": tc.mode} {
				if value != "" {
					request.Header.Set(name, value)
				}
			}
			request.Header.Set("X-Forwarded-Host", "public.example:2443")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			expected := 200
			if tc.code != "" {
				expected = 403
			}
			if response.StatusCode != expected || response.Header.Get("X-FnPackKit-Rejection") != tc.code {
				t.Fatalf("status=%d code=%s", response.StatusCode, response.Header.Get("X-FnPackKit-Rejection"))
			}
		})
	}
}
