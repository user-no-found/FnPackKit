package localrepo

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/process"
)

type fixture struct {
	root, directory, seed, remote string
	manager                       *Manager
	runtime                       *nativeRuntime
}

func gitTest(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("/usr/bin/git", append([]string{"-C", directory, "-c", "commit.gpgsign=false"}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic Git command failed: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTest(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func newRepoFixture(t *testing.T) *fixture {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("local repository runtime deliberately requires a nonroot owner")
	}
	root := t.TempDir()
	f := &fixture{root: root, seed: filepath.Join(root, "seed"), remote: filepath.Join(root, "remote"), directory: filepath.Join(root, "workspace"), runtime: &nativeRuntime{}}
	gitTest(t, root, "init", "--bare", "-b", "main", f.remote)
	gitTest(t, root, "init", "-b", "main", f.seed)
	writeTest(t, filepath.Join(f.seed, "message.txt"), "first")
	writeTest(t, filepath.Join(f.seed, ".gitignore"), ".build-cwd\n")
	gitTest(t, f.seed, "add", ".")
	gitTest(t, f.seed, "commit", "-m", "initial fixture")
	gitTest(t, f.seed, "remote", "add", "origin", f.remote)
	gitTest(t, f.seed, "push", "origin", "main")
	gitTest(t, root, "clone", f.remote, f.directory)
	manager, err := New(context.Background(), Config{Directory: f.directory, Git: "/usr/bin/git", Remote: "origin", Branch: "main", Timeout: 10 * time.Second,
		Build:  []Command{{Program: "/bin/sh", Args: []string{"-c", "pwd > .build-cwd"}}},
		Verify: []Command{{Program: "/bin/sh", Args: []string{"-c", "test -s message.txt"}}}}, f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	f.manager = manager
	t.Cleanup(func() {
		if err := f.runtime.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *fixture) advance(t *testing.T, text string) string {
	t.Helper()
	writeTest(t, filepath.Join(f.seed, "message.txt"), text)
	gitTest(t, f.seed, "add", "message.txt")
	gitTest(t, f.seed, "commit", "-m", "next fixture")
	gitTest(t, f.seed, "push", "origin", "main")
	return gitTest(t, f.seed, "rev-parse", "HEAD")
}

type nativeRuntime struct {
	mu            sync.Mutex
	child         *process.Child
	input         io.WriteCloser
	state         RuntimeState
	content       string
	starts, stops int
	badVersion    bool
}

func (r *nativeRuntime) State(context.Context) (RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state
	state.Running = r.child != nil && r.child.Alive()
	if !state.Running {
		state.Version = nil
	}
	return state, nil
}

func (r *nativeRuntime) Start(ctx context.Context, launch Launch) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command, err := launch.Command(executable, "-test.run=^TestLocalRepositoryChild$")
	if err != nil {
		return err
	}
	command.Env = append(command.Env, "FNPACKKIT_REPO_FIXTURE_CHILD=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	child, err := process.Start(command)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.child, r.input = child, stdin
	r.starts++
	r.mu.Unlock()
	var record struct {
		State   RuntimeState
		Content string
	}
	decoded := make(chan error, 1)
	go func() { decoded <- json.NewDecoder(stdout).Decode(&record) }()
	select {
	case err = <-decoded:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		r.Stop(context.Background())
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state, r.content = record.State, record.Content
	if r.badVersion {
		r.state.Version.Commit = strings.Repeat("0", 40)
	}
	return nil
}

func (r *nativeRuntime) Stop(context.Context) error {
	r.mu.Lock()
	child, input := r.child, r.input
	r.child, r.input = nil, nil
	if child != nil {
		r.stops++
	}
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

func TestLocalRepositoryChild(t *testing.T) {
	if os.Getenv("FNPACKKIT_REPO_FIXTURE_CHILD") != "1" {
		return
	}
	version, err := StartupVersion()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("message.txt")
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(os.Stdout).Encode(struct {
		State   RuntimeState
		Content string
	}{RuntimeState{Running: true, Directory: directory, Version: &version}, string(content)})
	io.Copy(io.Discard, os.Stdin)
}
