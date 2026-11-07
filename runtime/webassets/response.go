// Package webassets optimizes declared, bundled static files after authorization.
// It must not be applied to APIs, user files, HTML or streaming responses.
package webassets

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

const limit = 8 << 20

// Negotiate keeps documents uncompressed for HTML injection. Only explicitly
// declared static routes may use upstream gzip; range requests stay unchanged.
func Negotiate(out, in *http.Request, static bool) {
	out.Header.Set("Accept-Encoding", "identity")
	if static && in.Header.Get("Range") == "" && acceptsGzip(in.Header.Get("Accept-Encoding")) {
		out.Header.Set("Accept-Encoding", "gzip")
	}
}

func acceptsGzip(value string) bool {
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		if strings.TrimSpace(parts[0]) != "gzip" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && key == "q" {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					return false
				}
				quality = parsed
			}
		}
		return quality > 0
	}
	return false
}

func assetType(value string) (allowed, compress bool) {
	media, _, _ := mime.ParseMediaType(value)
	switch media {
	case "text/javascript", "application/javascript", "text/css", "image/svg+xml", "application/wasm":
		return true, true
	case "font/woff", "font/woff2", "application/font-woff", "image/png", "image/jpeg", "image/webp", "image/gif", "image/x-icon", "image/vnd.microsoft.icon":
		return true, false
	}
	return false, false
}

// Response uses private, revalidated browser caches, never a shared body cache.
// Set-Cookie, errors and undeclared responses remain no-store. Bounded buffering
// supplies representation ETags and gzip when the upstream does not compress.
func Response(res *http.Response, static bool) error {
	res.Header.Set("Cache-Control", "no-store")
	request := res.Request
	if request == nil || request.Method != "GET" && request.Method != "HEAD" {
		return nil
	}
	// A static URL can resolve to an SPA document. Decode that fallback before
	// the caller injects its runtime panel, without making the document cacheable.
	if strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/html") {
		if request.Method != "HEAD" && res.Header.Get("Content-Encoding") == "gzip" {
			return decodeDocument(res)
		}
		return nil
	}
	allowed, compress := assetType(res.Header.Get("Content-Type"))
	if !static || len(res.Header.Values("Set-Cookie")) > 0 ||
		res.StatusCode != 200 && res.StatusCode != 304 || !allowed && res.StatusCode != 304 {
		return nil
	}
	res.Header.Set("Cache-Control", "private, no-cache")
	res.Header.Add("Vary", "Accept-Encoding")
	if res.StatusCode == 304 || request.Method == "HEAD" || request.Header.Get("Range") != "" {
		return nil
	}
	// Preserve upstream validators and cached compressed bodies where no
	// transformation is necessary (notably precompressed JS and WOFF2 fonts).
	if res.Header.Get("ETag") != "" && (!compress || res.Header.Get("Content-Encoding") == "gzip") {
		return nil
	}
	if res.ContentLength > limit {
		return nil
	}
	original := res.Body
	data, err := io.ReadAll(io.LimitReader(original, limit+1))
	if err != nil {
		original.Close()
		return err
	}
	if len(data) > limit {
		res.Body = &joinedBody{Reader: io.MultiReader(bytes.NewReader(data), original), Closer: original}
		return nil
	}
	original.Close()
	encoding := res.Header.Get("Content-Encoding")
	if compress && len(data) >= 1024 && (encoding == "" || encoding == "identity") && acceptsGzip(request.Header.Get("Accept-Encoding")) {
		var encoded bytes.Buffer
		writer := gzip.NewWriter(&encoded)
		if _, err := writer.Write(data); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		if encoded.Len() < len(data) {
			data = encoded.Bytes()
			res.Header.Set("Content-Encoding", "gzip")
			res.Header.Del("Content-MD5")
			res.Header.Del("Accept-Ranges")
		}
	}
	// Hash the bytes actually sent, including an upstream compressed variant.
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	res.Header.Set("ETag", etag)
	for _, candidate := range strings.Split(request.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "W/")) == etag || strings.TrimSpace(candidate) == "*" {
			res.StatusCode, res.Status = 304, "304 Not Modified"
			data = nil
			break
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(data))
	res.ContentLength = int64(len(data))
	res.Header.Set("Content-Length", strconv.Itoa(len(data)))
	if res.StatusCode == 304 {
		res.Header.Del("Content-Length")
	}
	return nil
}

type joinedBody struct {
	io.Reader
	io.Closer
}

func decodeDocument(res *http.Response) error {
	reader, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}
	defer reader.Close()
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if len(data) > limit {
		return fmt.Errorf("static fallback document exceeds injection limit")
	}
	res.Body = io.NopCloser(bytes.NewReader(data))
	res.ContentLength = int64(len(data))
	res.Header.Set("Content-Length", strconv.Itoa(len(data)))
	for _, key := range []string{"Content-Encoding", "ETag", "Content-MD5"} {
		res.Header.Del(key)
	}
	return nil
}
