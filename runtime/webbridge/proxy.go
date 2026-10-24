package webbridge

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
)

func (b *Bridge) proxy(endpoint *Endpoint, id identity.Request) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: endpoint.transport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(endpoint.origin)
			if endpoint.stripPrefix {
				p.Out.URL.Path = strings.TrimPrefix(p.In.URL.Path, b.prefix)
			}
			p.Out.URL.RawPath = ""
			for name := range p.Out.Header {
				normalized := strings.ReplaceAll(strings.ToLower(name), "_", "-")
				if strings.HasPrefix(normalized, "x-trim-") || strings.HasPrefix(normalized, "x-forwarded-") ||
					normalized == "forwarded" || normalized == "x-real-ip" || normalized == "authorization" ||
					normalized == "proxy-authorization" || normalized == "cookie" {
					delete(p.Out.Header, name)
				}
			}
			for _, cookie := range p.In.Cookies() {
				if endpoint.cookies[cookie.Name] {
					p.Out.AddCookie(cookie)
				}
			}
			if endpoint.authorization != "" {
				p.Out.Header.Set("Authorization", endpoint.authorization)
			}
			if endpoint.forwardIdentity {
				p.Out.Header.Set("X-Trim-Userid", strconv.FormatUint(uint64(id.UID), 10))
				p.Out.Header.Set("X-Trim-Username", id.Name)
			}
			p.Out.Header.Set("Accept-Encoding", "identity")
			if document(p.In, b.prefix) {
				p.Out.Header.Del("If-None-Match")
				p.Out.Header.Del("If-Modified-Since")
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if err := endpoint.rewriteResponse(response, b.prefix); err != nil {
				return err
			}
			if err := runtimepanel.Inject(response, b.prefix); err != nil {
				return err
			}
			if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
				response.Header.Set("Cache-Control", "no-store")
			}
			return gatewaypolicy.CompleteHandshake(response)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			b.failure(w, r, err, http.StatusBadGateway)
		},
	}
}

func (e *Endpoint) rewriteResponse(response *http.Response, prefix string) error {
	if location := response.Header.Get("Location"); location != "" {
		target, err := url.Parse(location)
		if err != nil || target.User != nil || target.Opaque != "" || target.Scheme != "" && target.Scheme != "http" && target.Scheme != "https" {
			return errors.New("backend returned an invalid redirect")
		}
		resolved := response.Request.URL.ResolveReference(target)
		if strings.EqualFold(resolved.Host, e.origin.Host) && resolved.Scheme == e.origin.Scheme {
			resolved.Scheme, resolved.Host = "", ""
			resolved.Path = e.publicPath(resolved.Path, prefix)
			resolved.RawPath = ""
			response.Header.Set("Location", resolved.String())
		}
	}
	cookies := response.Cookies()
	response.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if !e.cookies[cookie.Name] {
			continue
		}
		cookie.Domain = ""
		cookie.Path = e.publicPath(cookie.Path, prefix)
		response.Header.Add("Set-Cookie", cookie.String())
	}
	return nil
}

func (e *Endpoint) publicPath(value, prefix string) string {
	value = path.Clean("/" + strings.TrimPrefix(value, "/"))
	if value == prefix || strings.HasPrefix(value, prefix+"/") {
		return value
	}
	return prefix + "/" + strings.TrimPrefix(value, "/")
}
