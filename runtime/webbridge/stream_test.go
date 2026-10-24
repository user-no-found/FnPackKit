package webbridge

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func echoWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, buffer, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	hash := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(hash[:]))
	if buffer.Flush() != nil {
		return
	}
	header := make([]byte, 2)
	if _, err = io.ReadFull(buffer, header); err != nil || header[1]&127 >= 126 {
		return
	}
	var mask [4]byte
	if header[1]&128 != 0 {
		if _, err = io.ReadFull(buffer, mask[:]); err != nil {
			return
		}
	}
	data := make([]byte, int(header[1]&127))
	if _, err = io.ReadFull(buffer, data); err != nil {
		return
	}
	for i := range data {
		data[i] ^= mask[i%4]
	}
	_, _ = conn.Write(append([]byte{0x81, byte(len(data))}, data...))
}

func TestWebSocketGrantAndEchoThroughBridge(t *testing.T) {
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Sec-WebSocket-Protocol"), "fpk-gateway") {
			t.Error("handshake proof leaked to business service")
		}
		echoWebSocket(w, r)
	}, EndpointConfig{StripPrefix: true})
	f := newFixture(t, endpoint)
	r := f.request("POST", "/_gateway/grant/ws", "")
	r.Header.Set("Origin", "https://external-nas.example")
	response, body := f.do(t, r)
	var grant struct{ Protocol string }
	if response.StatusCode != 200 || json.Unmarshal([]byte(body), &grant) != nil || grant.Protocol == "" || f.app.acquisitions != 0 {
		t.Fatal(response.StatusCode, body)
	}
	websocketRequest := func() *http.Request {
		r := f.request("GET", "/ws", "")
		r.Header.Set("Origin", "https://external-nas.example")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Sec-WebSocket-Version", "13")
		r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		r.Header.Set("Sec-WebSocket-Protocol", "fnpackkit.gateway.v1, "+grant.Protocol)
		return r
	}
	client := *f.client
	client.Timeout = 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.Do(websocketRequest().WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Protocol") != "fnpackkit.gateway.v1" {
		data, _ := io.ReadAll(response.Body)
		t.Fatal(response.StatusCode, response.Header, string(data))
	}
	stream := response.Body.(io.ReadWriteCloser)
	if _, err = stream.Write([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2}); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4)
	if _, err = io.ReadFull(stream, frame); err != nil || string(frame[2:]) != "hi" {
		t.Fatal(frame, err)
	}
	response, body = f.do(t, websocketRequest())
	if response.StatusCode != 403 || !strings.Contains(body, "grant_invalid") {
		t.Fatal("replayed proof accepted", response.StatusCode, body)
	}
}

func TestSSEFlushesBeforeUpstreamCompletes(t *testing.T) {
	release := make(chan struct{})
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, EndpointConfig{StripPrefix: true})
	f := newFixture(t, endpoint)
	defer close(release)
	response, err := f.client.Do(f.request("GET", "/events", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatal(line, err)
	}
}
