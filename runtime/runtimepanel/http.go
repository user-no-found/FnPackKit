// Package runtimepanel serves shared controls behind an application's authenticated gateway.
package runtimepanel

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

//go:embed web/*
var assets embed.FS

type Action struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Selector string `json:"selector"`
}

// Command is a bridge-owned control action that works without application HTML.
type Command struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Confirm bool   `json:"confirm,omitempty"`
}

type ExecuteFunc func(context.Context, identity.Request, backend.Command) (backend.Snapshot, error)

// CommandFunc executes a command declared by the application. The action is
// selected from the manifest, never from an arbitrary client-supplied executable.
type CommandFunc func(context.Context, identity.Request, string) (backend.Snapshot, error)

// Handler receives only an identity already authenticated and authorized by the application.
// Gateway Origin policy must run before Handle. No body can select a user or executable.
type Handler struct {
	Prefix         string
	Name           string
	Execute        ExecuteFunc
	ExecuteCommand CommandFunc
	Actions        []Action
	Commands       []Command
}

func (h Handler) Handle(w http.ResponseWriter, r *http.Request, user identity.Request) bool {
	base := h.Prefix + "/_runtime/"
	if !strings.HasPrefix(r.URL.Path, base) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	suffix := strings.TrimPrefix(r.URL.Path, base)
	if suffix == "control" {
		h.control(w, r, user)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return true
	}
	if suffix == "manifest" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(struct {
				Name     string    `json:"name"`
				Prefix   string    `json:"prefix"`
				Actions  []Action  `json:"actions"`
				Commands []Command `json:"commands,omitempty"`
			}{h.Name, h.Prefix, h.Actions, h.Commands})
		}
		return true
	}
	allowed := map[string]string{"bootstrap.js": "text/javascript", "panel.js": "text/javascript", "drag.js": "text/javascript", "panel.css": "text/css"}
	if kind, ok := allowed[suffix]; ok {
		data, err := assets.ReadFile("web/" + suffix)
		if err != nil {
			http.Error(w, "control resources unavailable", 500)
			return true
		}
		w.Header().Set("Content-Type", kind+"; charset=utf-8")
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return true
	}
	http.NotFound(w, r)
	return true
}

func (h Handler) command(name string) (Command, bool) {
	for _, command := range h.Commands {
		if command.ID == name {
			return command, true
		}
	}
	return Command{}, false
}

func (h Handler) control(w http.ResponseWriter, r *http.Request, user identity.Request) {
	command := backend.Command{Action: "status"}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if kind != "application/json" || r.Header.Get("X-FnPackKit-Control") != "1" || r.Header.Get("Origin") == "" {
			http.Error(w, "invalid control request", 403)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&command) != nil || decoder.Decode(new(any)) != io.EOF || command.Action == "status" {
			http.Error(w, "invalid control command", 400)
			return
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(405)
		return
	}
	builtin := command.Action == "status" || command.Action == "start" || command.Action == "stop" || command.Action == "restart"
	custom, declared := h.command(command.Action)
	if !builtin && (!declared || h.ExecuteCommand == nil) {
		http.Error(w, "invalid control command", 400)
		return
	}
	if ((command.Action == "stop" || command.Action == "restart") || (!builtin && custom.Confirm)) && !command.Confirm {
		http.Error(w, "invalid or unconfirmed control command", 400)
		return
	}
	var result backend.Snapshot
	var err error
	if builtin {
		result, err = h.Execute(r.Context(), user, command)
	} else {
		result, err = h.ExecuteCommand(r.Context(), user, command.Action)
	}
	code := 200
	if err != nil {
		code = 503
		if errors.Is(err, backend.ErrConfirmation) || errors.Is(err, backend.ErrAction) {
			code = 400
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(struct {
		backend.Snapshot
		Failed bool `json:"failed,omitempty"`
	}{result, err != nil})
}

var pausedPage = template.Must(template.New("paused").Parse(`<!doctype html><html lang="zh-CN" data-runtime-page><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Name}}</title><script type="module" src="{{.Prefix}}/_runtime/bootstrap.js"></script></head><body style="margin:0;background:#f4f6fa;color:#182230;font:16px system-ui;display:grid;place-items:center;min-height:100vh"><main style="max-width:32rem;padding:2rem"><h1>{{.Name}}</h1><p>{{.Message}}</p><p>点击悬浮按钮，使用后端控制继续操作。</p><p style="color:#667085;font-size:14px">轻量控制服务仍在运行。完全关闭应用请使用飞牛应用中心。</p></main></body></html>`))

// Unavailable serves an independent control shell only for document navigation.
func (h Handler) Unavailable(w http.ResponseWriter, r *http.Request) {
	h.unavailable(w, r, "后端已停止，应用数据已保留。", "backend_paused", 200)
}

// Recovery keeps controls available when the application cannot acquire or reach its backend.
// It does not claim that a failed backend has stopped or saved its data.
func (h Handler) Recovery(w http.ResponseWriter, r *http.Request) {
	h.unavailable(w, r, "后端暂不可用，请尝试启动或重启后端。", "backend_unavailable", 503)
}

func (h Handler) unavailable(w http.ResponseWriter, r *http.Request, message, code string, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.URL.Path == h.Prefix+"/" || strings.Contains(r.Header.Get("Accept"), "text/html")) && r.Header.Get("Upgrade") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_ = pausedPage.Execute(w, struct {
				Name, Prefix, Message string
			}{h.Name, h.Prefix, message})
		}
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(503)
	_ = json.NewEncoder(w).Encode(struct {
		Schema int    `json:"schema"`
		Code   string `json:"code"`
	}{1, code})
}

// ValidPrefix is used by response injection before writing a script URL into HTML.
func ValidPrefix(prefix string) bool {
	return strings.HasPrefix(prefix, "/") && prefix != "/" && path.Clean(prefix) == prefix && !strings.ContainsAny(prefix, "<>\"'?#%\\\x00")
}
