package webassets

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
)

// FileServer applies the same bounded optimization to bundled files. The caller
// owns authorization and route selection; directories and non-assets use the
// standard file server without buffering or compression.
func FileServer(files http.FileSystem) http.Handler {
	fallback := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		allowed, _ := assetType(mimeType(r.URL.Path))
		if !allowed || r.Method != "GET" && r.Method != "HEAD" || r.Header.Get("Range") != "" {
			fallback.ServeHTTP(w, r)
			return
		}
		file, err := files.Open(path.Clean("/" + r.URL.Path))
		if err != nil {
			fallback.ServeHTTP(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
			fallback.ServeHTTP(w, r)
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, limit+1))
		if err != nil || len(data) > limit {
			http.Error(w, "static file unavailable", http.StatusInternalServerError)
			return
		}
		// Transform a GET representation so HEAD has identical encoding and ETag.
		request := r.Clone(r.Context())
		request.Method = "GET"
		request.Header.Del("If-None-Match")
		res := &http.Response{StatusCode: 200, Header: make(http.Header), Request: request,
			Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
		res.Header.Set("Content-Type", mimeType(r.URL.Path))
		if err := Response(res, true); err != nil {
			http.Error(w, "static file unavailable", http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()
		encoded, err := io.ReadAll(res.Body)
		if err != nil {
			http.Error(w, "static file unavailable", http.StatusInternalServerError)
			return
		}
		for name, values := range res.Header {
			w.Header()[name] = values
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), bytes.NewReader(encoded))
	})
}

func mimeType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs":
		return "text/javascript"
	case ".css":
		return "text/css"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	case ".woff", ".woff2":
		return "font/" + strings.TrimPrefix(path.Ext(name), ".")
	}
	return mime.TypeByExtension(path.Ext(name))
}
