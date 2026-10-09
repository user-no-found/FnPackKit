package gatewaypolicy

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func grantConfig() Config {
	return Config{Prefix: "/app/example", AllowSameOriginFetch: true, HandshakeGrants: &GrantConfig{Lifetime: time.Second, Capacity: 3, PerUser: 2}}
}

func mint(t *testing.T, client *http.Client, base, uid string) (Grant, int) {
	t.Helper()
	request, _ := http.NewRequest("POST", base+"/_gateway/grant", nil)
	request.Header.Set("X-Trim-Userid", uid)
	request.Header.Set("X-Trim-Username", "user"+uid)
	request.Header.Set("Origin", "https://relay.example:2443")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var grant Grant
	if response.StatusCode == 200 {
		if err := json.NewDecoder(response.Body).Decode(&grant); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(grant.Protocol, grantPrefix) || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid grant response")
		}
	}
	return grant, response.StatusCode
}

func useGrant(client *http.Client, base string, grant Grant, uid, origin, target, protocols string) (int, string, error) {
	request, _ := http.NewRequest("GET", base+target, nil)
	request.Header.Set("X-Trim-Userid", uid)
	request.Header.Set("X-Trim-Username", "user"+uid)
	request.Header.Set("Origin", origin)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if protocols == "" {
		protocols = HandshakeProtocol + ", " + grant.Protocol
	}
	request.Header.Set("Sec-WebSocket-Protocol", protocols)
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	return response.StatusCode, response.Header.Get("Sec-WebSocket-Protocol"), nil
}

func TestGrantBindingConsumptionAndQuota(t *testing.T) {
	client, base := servePolicy(t, grantConfig(), true)
	grant, status := mint(t, client, base, "1000")
	if status != 200 {
		t.Fatal("mint denied")
	}
	for _, tc := range []struct{ uid, origin, target string }{
		{"1001", "https://relay.example:2443", "/ws"},
		{"1000", "https://other.example:2443", "/ws"},
		{"1000", "https://relay.example:2443", "/other"},
	} {
		code, _, err := useGrant(client, base, grant, tc.uid, tc.origin, tc.target, "")
		if err != nil || code != 403 {
			t.Fatalf("mismatched grant accepted: %d %v", code, err)
		}
	}
	// Wrong bindings did not consume the legitimate user's proof.
	code, selected, err := useGrant(client, base, grant, "1000", "https://relay.example:2443", "/ws", "")
	if err != nil || code != 101 || selected != HandshakeProtocol {
		t.Fatalf("grant handshake: %d %s %v", code, selected, err)
	}
	code, _, err = useGrant(client, base, grant, "1000", "https://relay.example:2443", "/ws", "")
	if err != nil || code != 403 {
		t.Fatal("consumed grant replayed")
	}
	// An explicit invalid ticket must fail even if strict Host/Origin would pass.
	code, _, err = useGrant(client, base, Grant{Protocol: grantPrefix + "invalid"}, "1000", "http://internal.example", "/ws", "")
	if err != nil || code != 403 {
		t.Fatal("invalid ticket fell back to strict origin")
	}
	oldest, _ := mint(t, client, base, "1000")
	_, _ = mint(t, client, base, "1000")
	newest, _ := mint(t, client, base, "1000")
	code, _, _ = useGrant(client, base, oldest, "1000", "https://relay.example:2443", "/ws", "")
	if code != 403 {
		t.Fatal("per-user oldest grant was not replaced")
	}
	other, status := mint(t, client, base, "1001")
	if status != 200 {
		t.Fatal("one user exhausted another user's slots")
	}
	_, status = mint(t, client, base, "1002")
	if status != 403 {
		t.Fatal("global grant capacity not bounded")
	}
	code, selected, err = useGrant(client, base, newest, "1000", "https://relay.example:2443", "/ws", "chat, "+HandshakeProtocol+", "+newest.Protocol)
	if err != nil || code != 101 || selected != "chat" {
		t.Fatalf("app protocol was changed: %d %s %v", code, selected, err)
	}
	time.Sleep(1100 * time.Millisecond)
	code, _, err = useGrant(client, base, other, "1001", "https://relay.example:2443", "/ws", "")
	if err != nil || code != 403 {
		t.Fatal("expired grant accepted")
	}
	_, status = mint(t, client, base, "1002")
	if status != 200 {
		t.Fatal("expired entries not reclaimed")
	}
}

func TestGrantAtomicSingleUseAndMalformedOffers(t *testing.T) {
	client, base := servePolicy(t, grantConfig(), true)
	grant, _ := mint(t, client, base, "1000")
	for _, protocols := range []string{
		HandshakeProtocol, grant.Protocol,
		HandshakeProtocol + ", " + HandshakeProtocol + ", " + grant.Protocol,
		HandshakeProtocol + ", " + grant.Protocol + ", " + grant.Protocol,
		HandshakeProtocol + ", " + grantPrefix + ", " + grant.Protocol,
		HandshakeProtocol + ", " + grant.Protocol + ", " + grantPrefix,
	} {
		code, _, err := useGrant(client, base, grant, "1000", "https://relay.example:2443", "/ws", protocols)
		if err != nil || code != 403 {
			t.Fatal("malformed grant protocols accepted")
		}
	}
	var group sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			code, _, err := useGrant(client, base, grant, "1000", "https://relay.example:2443", "/ws", "")
			if err != nil {
				codes <- 0
				return
			}
			codes <- code
		}()
	}
	group.Wait()
	close(codes)
	accepted := 0
	rejected := 0
	for code := range codes {
		if code == 101 {
			accepted++
		}
		if code == 403 {
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("grant was not atomic: accepted %d rejected %d", accepted, rejected)
	}
}

func TestGrantSurvivesInternalHostRewrite(t *testing.T) {
	cfg := grantConfig()
	cfg.AllowedHosts = []string{"internal.example", "upgrade.example"}
	client, base := servePolicy(t, cfg, true)
	grant, status := mint(t, client, base, "1000")
	if status != 200 {
		t.Fatal("mint denied")
	}
	request, _ := http.NewRequest("GET", base+"/ws", nil)
	request.Host = "upgrade.example"
	request.Header.Set("X-Trim-Userid", "1000")
	request.Header.Set("X-Trim-Username", "user1000")
	request.Header.Set("Origin", "https://relay.example:2443")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Protocol", HandshakeProtocol+", "+grant.Protocol)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 101 {
		t.Fatalf("proxy-only authority change rejected: %d", response.StatusCode)
	}
}

func TestNonGETCannotConsumeGrant(t *testing.T) {
	client, base := servePolicy(t, grantConfig(), true)
	grant, status := mint(t, client, base, "1000")
	if status != 200 {
		t.Fatal("mint denied")
	}
	request, _ := http.NewRequest("POST", base+"/ws", nil)
	request.Header.Set("X-Trim-Userid", "1000")
	request.Header.Set("X-Trim-Username", "user1000")
	request.Header.Set("Origin", "https://relay.example:2443")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Protocol", HandshakeProtocol+", "+grant.Protocol)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("POST consumed a handshake grant")
	}
	code, _, err := useGrant(client, base, grant, "1000", "https://relay.example:2443", "/ws", "")
	if err != nil || code != 101 {
		t.Fatal("invalid request consumed another attempt's grant")
	}
}
