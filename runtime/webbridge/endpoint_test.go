package webbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestEndpointRejectsRemoteAndUnprotectedIdentity(t *testing.T) {
	cases := []EndpointConfig{
		{URL: "https://example.com:443"},
		{URL: "http://192.168.1.1:8080"},
		{URL: "http://localhost:8080"},
		{URL: "http://127.0.0.1"},
		{URL: "http://127.0.0.1:0"},
		{URL: "http://user:secret@127.0.0.1:8080"},
		{URL: "http://127.0.0.1:8080/api"},
		{URL: "http://127.0.0.1:8080?secret=1"},
		{URL: "http://127.0.0.1:8080#fragment"},
		{URL: "http://127.0.0.1:8080", ForwardIdentity: true},
		{URL: "http://127.0.0.1:8080", Authorization: "Bearer secret\r\nX-Injected: 1"},
		{URL: "http://local", UnixSocket: "relative.sock"},
		{URL: "http://local", UnixSocket: "/run/../tmp/backend.sock"},
		{URL: "http://127.0.0.1:8080", Cookies: []string{"session", "session"}},
		{URL: "http://127.0.0.1:8080", Cookies: []string{"__Host-session"}},
	}
	for _, config := range cases {
		if endpoint, err := NewEndpoint(config); err == nil {
			endpoint.CloseIdleConnections()
			t.Fatalf("accepted unsafe endpoint: %+v", config)
		}
	}
}

func TestProxyRebuildsIdentityAndScopesApplicationCookies(t *testing.T) {
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/headers" || r.URL.RawQuery != "x=1" {
			t.Errorf("wrong upstream path: %s", r.URL)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/", Domain: "local", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "nas_cookie", Value: "must-not-escape", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Header)
	}, EndpointConfig{StripPrefix: true, ForwardIdentity: true, Authorization: "Bearer bridge-only", Cookies: []string{"session"}})
	f := newFixture(t, endpoint)
	r := f.request("GET", "/api/headers?x=1", "")
	r.Header.Set("X_Trim_Userid", "1001")
	r.Header.Set("X-Trim-Isadmin", "true")
	r.Header.Set("X_Forwarded_Host", "evil.example")
	r.Header.Set("Forwarded", "host=evil.example")
	r.Header.Set("Authorization", "Bearer browser-secret")
	r.Header.Set("Cookie", "nas_cookie=private; session=old")
	response, body := f.do(t, r)
	var headers http.Header
	if err := json.Unmarshal([]byte(body), &headers); err != nil {
		t.Fatal(body, err)
	}
	if headers.Get("Authorization") != "Bearer bridge-only" || headers.Get("X-Trim-Userid") != "1000" || headers.Get("X-Trim-Username") != "owner" ||
		headers.Get("X_Trim_Userid") != "" || headers.Get("X-Trim-Isadmin") != "" || headers.Get("X_Forwarded_Host") != "" ||
		headers.Get("Forwarded") != "" || headers.Get("Cookie") != "session=old" || headers.Get("Accept-Encoding") != "identity" {
		t.Fatal("untrusted headers crossed the bridge", headers)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Domain != "" || cookies[0].Path != testPrefix+"/" || !cookies[0].HttpOnly {
		t.Fatal("upstream cookie escaped application scope", response.Header)
	}
	r = f.request("GET", "/api/headers", "")
	r.Header.Set("Cookie", "session=one; session=two")
	response, _ = f.do(t, r)
	if response.StatusCode != 403 {
		t.Fatal("ambiguous application cookies forwarded")
	}
}

func TestPrivateUnixEndpointAndSeparateFrontendBackend(t *testing.T) {
	socket := socketPath(t, "business.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "private-api:"+r.Header.Get("X-Trim-Userid")+":"+r.URL.Path)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	api, err := NewEndpoint(EndpointConfig{URL: "http://business", UnixSocket: socket, StripPrefix: true, ForwardIdentity: true})
	if err != nil {
		t.Fatal(err)
	}
	defer api.CloseIdleConnections()
	frontend := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "frontend:"+r.URL.Path)
	}, EndpointConfig{})
	f := newFixture(t, frontend)
	f.app.selectEndpoint = func(relative string) *Endpoint {
		if strings.HasPrefix(relative, "/api/") {
			return api
		}
		return frontend
	}
	_, body := f.do(t, f.request("GET", "/assets/app.js", ""))
	if body != "frontend:"+testPrefix+"/assets/app.js" {
		t.Fatal(body)
	}
	_, body = f.do(t, f.request("GET", "/api/version", ""))
	if body != "private-api:1000:/api/version" {
		t.Fatal(body)
	}
}

func TestRedirectsStayUnderPublicPrefixAndHTMLRevalidates(t *testing.T) {
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			t.Error("conditional document request could skip panel injection")
		}
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "../login?next=home")
			w.WriteHeader(302)
		case "/bad-redirect":
			w.Header().Set("Location", "javascript:alert(1)")
			w.WriteHeader(302)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><head></head><body>fresh</body></html>")
		}
	}, EndpointConfig{StripPrefix: true})
	f := newFixture(t, endpoint)
	r := f.request("GET", "/redirect", "")
	r.Header.Set("Accept", "text/html")
	r.Header.Set("If-None-Match", "stale")
	r.Header.Set("If-Modified-Since", "yesterday")
	response, _ := f.do(t, r)
	if response.StatusCode != 302 || response.Header.Get("Location") != testPrefix+"/login?next=home" {
		t.Fatal(response.StatusCode, response.Header)
	}
	response, _ = f.do(t, f.request("GET", "/bad-redirect", ""))
	if response.StatusCode != 502 {
		t.Fatal("invalid redirect forwarded", response.StatusCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = f.request("GET", "/", "").WithContext(ctx)
	if response, err := f.client.Do(r); err == nil {
		response.Body.Close()
		t.Fatal("canceled request completed")
	}
}
