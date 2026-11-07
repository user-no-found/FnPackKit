package webassets

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func response(body, contentType string) *http.Response {
	r := httptest.NewRequest("GET", "http://local/static/app.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}},
		Request: r, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func TestStaticVariantsRevalidateWithoutCachingDocumentsOrSecrets(t *testing.T) {
	body := strings.Repeat("const greeting = '你好';\n", 500)
	compressed := response(body, "text/javascript")
	if err := Response(compressed, true); err != nil {
		t.Fatal(err)
	}
	if compressed.Header.Get("Content-Encoding") != "gzip" || compressed.ContentLength >= int64(len(body))/2 ||
		compressed.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatal("static response was not compressed and privately revalidated")
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := io.ReadAll(reader)
	reader.Close()
	if string(decoded) != body {
		t.Fatal("compressed bytes differ")
	}
	plain := response(body, "text/javascript")
	plain.Request.Header.Set("Accept-Encoding", "gzip;q=0")
	if err := Response(plain, true); err != nil {
		t.Fatal(err)
	}
	if plain.Header.Get("Content-Encoding") != "" || plain.Header.Get("ETag") == compressed.Header.Get("ETag") {
		t.Fatal("encoding quality or variant ETag ignored")
	}
	for _, condition := range []string{compressed.Header.Get("ETag"), "W/" + compressed.Header.Get("ETag"), "*"} {
		next := response(body, "text/javascript")
		next.Request.Header.Set("If-None-Match", condition)
		if err := Response(next, true); err != nil {
			t.Fatal(err)
		}
		if data, _ := io.ReadAll(next.Body); next.StatusCode != 304 || len(data) != 0 {
			t.Fatal("matching cache must return an empty 304")
		}
	}
	for _, kind := range []string{"document", "api", "cookie", "error", "undeclared", "post"} {
		r := response(body, "text/javascript")
		switch kind {
		case "document":
			r.Header.Set("Content-Type", "text/html")
		case "api":
			r.Header.Set("Content-Type", "application/json")
		case "cookie":
			r.Header.Add("Set-Cookie", "private=secret")
		case "error":
			r.StatusCode = 403
		case "post":
			r.Request.Method = "POST"
		}
		if err := Response(r, kind != "undeclared"); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Content-Encoding") != "" {
			t.Fatalf("%s was made cacheable", kind)
		}
	}
}

func TestUpstreamCompressionAndSPAFallback(t *testing.T) {
	for _, media := range []string{"text/css", "text/html; charset=utf-8"} {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		writer.Write([]byte(strings.Repeat("example", 1000)))
		writer.Close()
		r := response(buffer.String(), media)
		r.Header.Set("Content-Encoding", "gzip")
		r.Header.Set("ETag", "\"upstream\"")
		if err := Response(r, true); err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		if strings.HasPrefix(media, "text/html") {
			if r.Header.Get("Content-Encoding") != "" || r.Header.Get("Cache-Control") != "no-store" || string(data) != strings.Repeat("example", 1000) {
				t.Fatal("SPA fallback must remain injectible and uncacheable")
			}
		} else if !bytes.Equal(data, buffer.Bytes()) || r.Header.Get("Content-Encoding") != "gzip" {
			t.Fatal("upstream gzip was altered")
		}
	}
}

func TestHeadDocumentAndStreamsDoNotReadBodies(t *testing.T) {
	for _, media := range []string{"text/html", "text/event-stream", "application/json"} {
		r := response("", media)
		r.Body = unreadBody{t}
		if media == "text/html" {
			r.Request.Method = "HEAD"
			r.Header.Set("Content-Encoding", "gzip")
		}
		if err := Response(r, true); err != nil || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("document or stream changed", err)
		}
	}
}

type unreadBody struct{ t *testing.T }

func (body unreadBody) Read([]byte) (int, error) {
	body.t.Fatal("stream or HEAD body was read")
	return 0, io.EOF
}
func (unreadBody) Close() error { return nil }

func TestStaticRangeOversizeAndNegotiation(t *testing.T) {
	for _, size := range []int{limit, limit + 1} {
		r := response(strings.Repeat("x", size), "text/css")
		r.ContentLength = -1
		if err := Response(r, true); err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		if size > limit && (len(data) != size || r.Header.Get("Content-Encoding") != "") {
			t.Fatal("oversize stream truncated or compressed")
		}
	}
	r := httptest.NewRequest("GET", "http://local/static/a.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	for _, static := range []bool{false, true} {
		out := r.Clone(r.Context())
		Negotiate(out, r, static)
		if (out.Header.Get("Accept-Encoding") == "gzip") != static {
			t.Fatal("negotiation escaped declared static routes")
		}
	}
	r.Header.Set("Range", "bytes=0-3")
	out := r.Clone(r.Context())
	Negotiate(out, r, true)
	if out.Header.Get("Accept-Encoding") != "identity" {
		t.Fatal("range negotiation changed")
	}
}

func TestBundledFileServerGETHEADConditionalAndRanges(t *testing.T) {
	body := strings.Repeat("const message = '你好';", 500)
	handler := FileServer(http.FS(fstest.MapFS{"build/app.js": {Data: []byte(body)}}))
	request := func(method string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/build/app.js", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	get := request("GET", nil)
	head := request("HEAD", nil)
	if get.Code != 200 || head.Code != 200 || head.Body.Len() != 0 || get.Header().Get("ETag") != head.Header().Get("ETag") || get.Header().Get("Content-Length") != head.Header().Get("Content-Length") {
		t.Fatal("HEAD representation differs from GET")
	}
	next := request("GET", map[string]string{"If-None-Match": get.Header().Get("ETag")})
	if next.Code != 304 || next.Body.Len() != 0 {
		t.Fatal("conditional file transfer did not return 304")
	}
	ranged := request("GET", map[string]string{"Range": "bytes=0-3"})
	if ranged.Code != 206 || ranged.Body.String() != body[:4] || ranged.Header().Get("Content-Encoding") != "" {
		t.Fatal("range response corrupted")
	}
}
