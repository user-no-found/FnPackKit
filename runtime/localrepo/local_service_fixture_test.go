package localrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/process"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
)

const localPrefix = "/app/local-fixture"
const fixtureAuthorization = "Bearer independent-local-fixture"

type localServiceConfig struct {
	Workspace string
	Data      string
	Owner     identity.Request
}

type localWebRuntime struct {
	mu      sync.Mutex
	child   *process.Child
	input   io.WriteCloser
	state   RuntimeState
	address string
}

func (r *localWebRuntime) State(context.Context) (RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state
	state.Running = r.child != nil && r.child.Alive()
	if !state.Running {
		state.Version = nil
	}
	return state, nil
}

func (r *localWebRuntime) Start(ctx context.Context, launch Launch) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command, err := launch.Command(executable, "-test.run=^TestIndependentBusinessProcess$")
	if err != nil {
		return err
	}
	command.Env = append(command.Env, "LOCAL_SERVICE_CHILD=business")
	command.Stderr = os.Stderr
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	child, err := process.Start(command)
	if err != nil {
		input.Close()
		return err
	}
	r.mu.Lock()
	r.child, r.input = child, input
	r.mu.Unlock()
	var ready struct {
		URL   string
		State RuntimeState
	}
	decoded := make(chan error, 1)
	go func() { decoded <- json.NewDecoder(output).Decode(&ready) }()
	select {
	case err = <-decoded:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		return errors.Join(err, r.Stop(context.Background()))
	}
	r.mu.Lock()
	r.address, r.state = ready.URL, ready.State
	r.mu.Unlock()
	return nil
}

func (r *localWebRuntime) Stop(context.Context) error {
	r.mu.Lock()
	child, input := r.child, r.input
	r.child, r.input, r.address = nil, nil, ""
	r.mu.Unlock()
	if input != nil {
		input.Close()
	}
	if child != nil {
		select {
		case <-child.Done():
			return nil
		case <-time.After(3 * time.Second):
			return child.KillAndWait(time.Second)
		}
	}
	return nil
}

func (r *localWebRuntime) origin() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.address
}

func readLocalServiceConfig(t *testing.T) localServiceConfig {
	t.Helper()
	var config localServiceConfig
	if err := json.Unmarshal([]byte(os.Getenv("LOCAL_SERVICE_CONFIG")), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestIndependentManagementProcess(t *testing.T) {
	if os.Getenv("LOCAL_SERVICE_CHILD") != "manager" {
		return
	}
	config := readLocalServiceConfig(t)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TRIM_") {
			t.Fatal("native management must not need FPK lifecycle variables")
		}
	}
	runtime := &localWebRuntime{}
	manager, err := New(context.Background(), Config{Directory: config.Workspace, Git: "/usr/bin/git",
		Remote: "origin", Branch: "main", Timeout: 10 * time.Second,
		Verify: []Command{{Program: "/bin/sh", Args: []string{"-c", "test -s message.txt && test -s frontend/index.html"}}}}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	controller := backend.New(manager)
	panel := runtimepanel.Handler{Prefix: localPrefix, Name: "独立本地应用", Execute: controller.Execute,
		Commands: []runtimepanel.Command{runtimepanel.UpdateBackendCommand(), runtimepanel.RebuildLocalCommand()}}
	panel.ExecuteCommand = func(ctx context.Context, id identity.Request, command string) (backend.Snapshot, error) {
		var err error
		switch command {
		case "update-backend":
			err = manager.Update(ctx, id)
		case "rebuild-local":
			err = manager.Rebuild(ctx, id)
		default:
			err = backend.ErrAction
		}
		if err == nil {
			file, openErr := os.OpenFile(filepath.Join(config.Data, "operations.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if openErr == nil {
				_, err = fmt.Fprintf(file, "%d %s\n", os.Getpid(), command)
				file.Close()
			} else {
				err = openErr
			}
		}
		status, statusErr := controller.Execute(ctx, id, backend.Command{Action: "status"})
		return status, errors.Join(err, statusErr)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		native, _ := r.Cookie("local_session")
		bridged := r.Header.Get("Authorization") == fixtureAuthorization && r.Header.Get("X-Trim-Userid") == fmt.Sprint(config.Owner.UID)
		if !bridged && (native == nil || native.Value != "independent-local-fixture") {
			http.Error(w, "native authorization denied", 403)
			return
		}
		if !bridged && r.Method == "POST" && (r.Header.Get("Origin") == "" || r.Header.Get("Sec-Fetch-Site") != "same-origin") {
			http.Error(w, "native origin rejected", 403)
			return
		}
		if panel.Handle(w, r, config.Owner) {
			return
		}
		state, err := runtime.State(r.Context())
		if err != nil || !state.Running {
			panel.Unavailable(w, r)
			return
		}
		target, _ := url.Parse(runtime.origin())
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(out *http.Request) {
			director(out)
			out.URL.Path = strings.TrimPrefix(out.URL.Path, localPrefix)
			out.Header.Set("Accept-Encoding", "identity")
		}
		proxy.ModifyResponse = func(response *http.Response) error { return runtimepanel.Inject(response, localPrefix) }
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer runtime.Stop(context.Background())
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		URL string
		PID int
	}{server.URL, os.Getpid()})
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestIndependentBusinessProcess(t *testing.T) {
	if os.Getenv("LOCAL_SERVICE_CHILD") != "business" {
		return
	}
	config := readLocalServiceConfig(t)
	version, err := StartupVersion()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.Getwd()
	if err != nil || directory != config.Workspace {
		t.Fatal("business working directory drifted", err)
	}
	message, err := os.ReadFile("message.txt")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataFile := filepath.Join(config.Data, "value.txt")
		switch r.URL.Path {
		case "/api/state":
			value, _ := os.ReadFile(dataFile)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"version": string(message), "saved": string(value), "pid": os.Getpid(), "commit": version.Commit})
		case "/api/value":
			value, err := io.ReadAll(io.LimitReader(r.Body, 1024))
			if r.Method != "POST" || err != nil || os.WriteFile(dataFile, value, 0600) != nil {
				http.Error(w, "write rejected", 400)
				return
			}
			w.WriteHeader(204)
		default:
			body, err := os.ReadFile("frontend/index.html")
			if err != nil {
				http.Error(w, "frontend unavailable", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
		}
	}))
	defer server.Close()
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		URL   string
		State RuntimeState
	}{server.URL, RuntimeState{Running: true, Directory: directory, Version: &version}})
	_, _ = io.Copy(io.Discard, os.Stdin)
}
