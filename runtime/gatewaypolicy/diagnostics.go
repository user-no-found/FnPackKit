package gatewaypolicy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// AuditFields exposes request shape without domains, user identifiers or credentials.
type AuditFields struct {
	Code           string `json:"code"`
	Method         string `json:"method"`
	RouteClass     string `json:"route_class"`
	HostPort       string `json:"host_port"`
	OriginScheme   string `json:"origin_scheme"`
	OriginPort     string `json:"origin_port"`
	HostOriginSame bool   `json:"host_origin_same"`
	FetchSite      string `json:"fetch_site"`
	FetchMode      string `json:"fetch_mode"`
	FetchDest      string `json:"fetch_dest"`
	Upgrade        string `json:"upgrade"`
}

func token(header http.Header, name string, allowed ...string) string {
	values := header.Values(name)
	if len(values) == 0 {
		return "absent"
	}
	if len(values) != 1 {
		return "invalid"
	}
	for _, value := range allowed {
		if values[0] == value {
			return value
		}
	}
	return "invalid"
}

func portLabel(host string) string {
	if !validHost(host) {
		return "invalid"
	}
	authority, _ := url.Parse("http://" + host)
	if authority.Port() == "" {
		return "omitted"
	}
	value, err := strconv.ParseUint(authority.Port(), 10, 16)
	if err != nil || value == 0 {
		return "invalid"
	}
	return strconv.FormatUint(value, 10)
}

// Audit summarizes a rejection safely; it does not include paths, query strings or domains.
func Audit(request *http.Request, rejection *Rejection) AuditFields {
	code := "unknown"
	if rejection != nil {
		code = knownCode(rejection.Code)
	}
	method := "other"
	switch request.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		method = request.Method
	}
	route := "resource"
	if request.URL != nil && strings.HasSuffix(request.URL.Path, "/_gateway/status") {
		route = "diagnostic"
	}
	upgrade := token(request.Header, "Upgrade", "websocket")
	if upgrade == "websocket" {
		route = "websocket"
	}
	fields := AuditFields{Code: code, Method: method, RouteClass: route, HostPort: portLabel(request.Host),
		OriginScheme: "absent", OriginPort: "absent", Upgrade: upgrade,
		FetchSite: token(request.Header, "Sec-Fetch-Site", "same-origin", "same-site", "cross-site", "none"),
		FetchMode: token(request.Header, "Sec-Fetch-Mode", "cors", "no-cors", "same-origin", "navigate", "websocket"),
		FetchDest: token(request.Header, "Sec-Fetch-Dest", "", "document", "iframe", "font", "script", "style", "image", "empty")}
	origins := request.Header.Values("Origin")
	if len(origins) > 0 {
		fields.OriginScheme = "invalid"
		fields.OriginPort = "invalid"
		if len(origins) == 1 {
			if origin, ok := parseOrigin(origins[0]); ok {
				fields.OriginScheme = origin.Scheme
				fields.OriginPort = portLabel(origin.Host)
				fields.HostOriginSame = origin.Host == request.Host
			}
		}
	}
	return fields
}

func knownCode(code string) string {
	switch code {
	case "untrusted_peer", "invalid_identity", "invalid_host", "invalid_path", "duplicate_origin", "invalid_origin", "invalid_fetch_metadata", "cross_site", "origin_required", "origin_mismatch", "invalid_cookie", "runtime_unavailable", "proxy_failure", "grant_disabled", "grant_invalid", "grant_capacity", "grant_unavailable":
		return code
	}
	return "unknown"
}

// WriteRejection returns a stable diagnostic body and a fresh server-generated request ID.
// The caller can attach the returned ID to its Audit log record.
func WriteRejection(writer http.ResponseWriter, request *http.Request, rejection *Rejection) string {
	return WriteFailure(writer, request, rejection, http.StatusForbidden)
}

// WriteFailure emits the same safe diagnostic for policy, worker and proxy failures.
// Only 403, 502 and 503 are accepted application statuses.
func WriteFailure(writer http.ResponseWriter, request *http.Request, rejection *Rejection, status int) string {
	if status != http.StatusForbidden && status != http.StatusBadGateway && status != http.StatusServiceUnavailable {
		status = http.StatusInternalServerError
		rejection = &Rejection{Code: "unknown"}
	}
	var random [16]byte
	id := "unavailable"
	if _, err := rand.Read(random[:]); err == nil {
		id = hex.EncodeToString(random[:])
	}
	code := "unknown"
	if rejection != nil {
		code = knownCode(rejection.Code)
	}
	writer.Header().Set("X-FnPackKit-Rejection", code)
	writer.Header().Set("X-FnPackKit-Request-ID", id)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	if request.Method != http.MethodHead {
		_ = json.NewEncoder(writer).Encode(struct {
			Schema    int    `json:"schema"`
			Status    string `json:"status"`
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		}{1, "rejected", code, id})
	}
	return id
}
