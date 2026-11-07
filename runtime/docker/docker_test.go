package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, state map[string]any) (Client, string) {
	t.Helper()
	if state == nil {
		state = map[string]any{}
	}
	dir, err := os.MkdirTemp("/tmp", "fpk-docker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	trace := filepath.Join(dir, "trace")
	encoded, _ := json.Marshal(state)
	script := fmt.Sprintf(`#!/usr/bin/python3
import json,os,sys,time
s=json.loads(%q)
a=sys.argv[1:]
with open(%q,'a') as f:f.write(json.dumps({'args':a,'env':dict(os.environ)})+'\n')
if s.get('slow'):time.sleep(5)
if s.get('huge'):print('x'*300000);sys.exit(0)
if a[:2]==['compose','version']:print('v5.1.4')
elif a[0]=='info':
 if s.get('unavailable'):sys.exit(1)
 print(json.dumps({'runc':{},'nvidia':{}}) if 'Runtimes' in a[-1] else 'engine')
elif a[:2]==['image','inspect']:
 if not s.get('cached'):sys.exit(1)
 print('sha256:cached')
elif a[0]=='pull':pass
elif a[0]=='compose':pass
elif a[:2]==['volume','inspect']:
 if s.get('missing'):sys.exit(1)
 print(json.dumps(s.get('labels',{})))
elif a[:2]==['volume','ls']:
 if not s.get('missing'):print('example_data')
elif a[:2]==['volume','rm']:pass
elif a[:2]==['container','inspect']:
 if s.get('missing'):sys.exit(1)
 print(json.dumps({'running':True,'labels':s.get('labels',{})}))
else:sys.exit(4)
`, string(encoded), trace)
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return Client{Binary: binary}, trace
}

func TestProjectScopeAndEnvironment(t *testing.T) {
	c, trace := fixture(t, nil)
	t.Setenv("DOCKER_HOST", "tcp://foreign:2375")
	t.Setenv("TRIM_API_TOKEN", "must-not-inherit")
	dir := t.TempDir()
	compose, env := filepath.Join(dir, "compose.yaml"), filepath.Join(dir, ".env")
	os.WriteFile(compose, []byte("services: {}"), 0600)
	os.WriteFile(env, []byte("SECRET=private"), 0600)
	p := Project{Client: c, Name: "example", ComposeFile: compose, EnvFile: env}
	if err := p.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Down(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(trace)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if len(record.Args) < 3 || record.Args[1] != "--project-name" || record.Args[2] != "example" || record.Env["DOCKER_HOST"] != "" || record.Env["TRIM_API_TOKEN"] != "" {
			t.Fatal("project or environment boundary failed")
		}
		for _, value := range record.Args {
			if value == "--volumes" || value == "-v" || value == "private" {
				t.Fatal("destructive or secret argument")
			}
		}
	}
	os.Chmod(env, 0644)
	if err := p.Up(context.Background()); err == nil {
		t.Fatal("accepted a public environment file")
	}
	os.Remove(env)
	os.Symlink(compose, env)
	if err := p.Up(context.Background()); err == nil {
		t.Fatal("accepted a linked environment file")
	}
	os.Remove(env)
	os.Link(compose, env)
	if err := p.Up(context.Background()); err == nil {
		t.Fatal("accepted hard-linked project authority")
	}
}

func TestOwnershipAndMissingState(t *testing.T) {
	ctx := context.Background()
	volume := Volume{Name: "example_data", Project: "example", Key: "data", Labels: map[string]string{"owner": "example"}}
	labels := map[string]string{"owner": "example", "com.docker.compose.project": "example", "com.docker.compose.volume": "data", "com.docker.compose.service": "web"}
	c, trace := fixture(t, map[string]any{"labels": labels})
	if err := c.RemoveVolume(ctx, volume); err != nil {
		t.Fatal(err)
	}
	if running, err := c.Running(ctx, Container{Name: "example-web", Project: "example", Service: "web"}); err != nil || !running {
		t.Fatalf("owned container rejected: %v", err)
	}
	data, _ := os.ReadFile(trace)
	if !strings.Contains(string(data), `"rm"`) {
		t.Fatal("owned volume was not removed")
	}
	labels["owner"] = "foreign"
	c, trace = fixture(t, map[string]any{"labels": labels})
	if err := c.RemoveVolume(ctx, volume); err == nil {
		t.Fatal("foreign volume accepted")
	}
	if _, err := c.Running(ctx, Container{Name: "example-web", Project: "foreign", Service: "web"}); err == nil {
		t.Fatal("foreign container accepted")
	}
	data, _ = os.ReadFile(trace)
	if strings.Contains(string(data), `"rm"`) {
		t.Fatal("foreign volume removal attempted")
	}
	c, _ = fixture(t, map[string]any{"missing": true})
	if err := c.RemoveVolume(ctx, volume); err != nil {
		t.Fatal(err)
	}
	c, _ = fixture(t, map[string]any{"missing": true, "unavailable": true})
	if _, err := c.Running(ctx, Container{Name: "example-web", Project: "example", Service: "web"}); err == nil {
		t.Fatal("daemon failure reported as stopped")
	}
}

func TestPrerequisitesImageAndCancellation(t *testing.T) {
	c, trace := fixture(t, map[string]any{"cached": true})
	ctx := context.Background()
	if err := c.Check(ctx, "nvidia"); err != nil {
		t.Fatal(err)
	}
	if err := c.Check(ctx, "missing"); err == nil {
		t.Fatal("missing runtime accepted")
	}
	if err := c.EnsureImage(ctx, "example/image:latest"); err == nil {
		t.Fatal("mutable image accepted")
	}
	image := "example/image@sha256:" + strings.Repeat("a", 64)
	if err := c.EnsureImage(ctx, image); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(trace)
	if strings.Contains(string(data), `"pull"`) {
		t.Fatal("cached immutable image was pulled again")
	}
	c, trace = fixture(t, map[string]any{})
	if err := c.EnsureImage(ctx, image); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(trace)
	if !strings.Contains(string(data), `"pull"`) {
		t.Fatal("missing image was not fetched")
	}
	c, _ = fixture(t, map[string]any{"huge": true})
	if err := c.Check(ctx, ""); err == nil {
		t.Fatal("unbounded output accepted")
	}
	c, _ = fixture(t, map[string]any{"slow": true})
	limited, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := c.Check(limited, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation was lost: %v", err)
	}
}
