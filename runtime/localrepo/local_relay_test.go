package localrepo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/backend"
)

type businessEvidence struct {
	Version string `json:"version"`
	Saved   string `json:"saved"`
	PID     int    `json:"pid"`
	Commit  string `json:"commit"`
}

func businessState(t *testing.T, client *http.Client, address string) businessEvidence {
	t.Helper()
	code, body := localRequest(t, client, "GET", address+localPrefix+"/api/state", "")
	var state businessEvidence
	if code != 200 || json.Unmarshal([]byte(body), &state) != nil {
		t.Fatalf("actual business state: %d %s", code, body)
	}
	return state
}

func controlState(t *testing.T, client *http.Client, address, body string, expected int) backend.Snapshot {
	t.Helper()
	method := "POST"
	if body == "" {
		method = "GET"
	}
	code, data := localRequest(t, client, method, address+localPrefix+"/_runtime/control", body)
	var state backend.Snapshot
	if code != expected || json.Unmarshal([]byte(data), &state) != nil {
		t.Fatalf("local control state: %d %s", code, data)
	}
	return state
}

func TestIndependentServiceOwnsUpdatesProcessesAndPersistentData(t *testing.T) {
	f := newIndependentFixture(t)
	if f.managerPID == os.Getpid() {
		t.Fatal("management did not leave the FPK process")
	}
	// Native access starts the same application before an FPK relay even exists.
	controlState(t, f.client, f.address, `{"action":"start"}`, 200)
	initial := businessState(t, f.client, f.address)
	if initial.PID == f.managerPID || initial.PID == os.Getpid() {
		t.Fatal("business must be a separate owned process")
	}
	code, _ := localRequest(t, f.client, "POST", f.address+localPrefix+"/api/value", "persistent production fixture")
	if code != 204 {
		t.Fatal("native data write failed")
	}
	entry := f.relay(t)
	throughFPK := businessState(t, f.client, entry.address)
	if throughFPK.PID != initial.PID || throughFPK.Saved != "persistent production fixture" {
		t.Fatal("FPK created a second business process or data store")
	}
	code, html := localRequest(t, f.client, "GET", entry.address+localPrefix+"/", "")
	if code != 200 || strings.Count(html, localPrefix+"/_runtime/bootstrap.js") != 1 {
		t.Fatal("local management UI must be injected once by the native service")
	}
	commit := f.repo.advance(t, "updated backend")
	state := controlState(t, f.client, entry.address, `{"action":"update-backend","confirm":true}`, 200)
	if state.Source == nil || state.Source.Running == nil || state.Source.Running.Commit != commit {
		t.Fatal("update did not return the actual running revision")
	}
	updated := businessState(t, f.client, entry.address)
	if updated.PID == initial.PID || updated.Version != "updated backend" || updated.Saved != throughFPK.Saved {
		t.Fatal("local update did not replace the actual process while preserving data")
	}
	operations, err := os.ReadFile(filepath.Join(f.config.Data, "operations.log"))
	if err != nil || string(operations) != fmt.Sprintf("%d update-backend\n", f.managerPID) {
		t.Fatal("update was not executed by the local management process", err)
	}
	files, err := os.ReadDir(entry.root)
	if err != nil || len(files) != 1 || files[0].Name() != "bridge.sock" {
		t.Fatal("business code or data leaked into the FPK installation fixture")
	}
	entry.close()
	if err := os.Remove(entry.root); err != nil {
		t.Fatal("FPK removal fixture was not limited to its bridge files", err)
	}
	afterRemoval := businessState(t, f.client, f.address)
	if afterRemoval != updated {
		t.Fatal("closing/removing the FPK bridge changed the native business process or data")
	}
	if value, _ := os.ReadFile(filepath.Join(f.config.Data, "value.txt")); string(value) != updated.Saved {
		t.Fatal("persistent data left the configured local application directory")
	}
	if status := gitTest(t, f.repo.directory, "status", "--porcelain"); status != "" {
		t.Fatal("business data entered Git instead of its configured persistence directory", status)
	}
}

func TestStoppedLocalManagementSurvivesRelayRestartAndDoesNotAutoStart(t *testing.T) {
	f := newIndependentFixture(t)
	entry := f.relay(t)
	state := controlState(t, f.client, entry.address, "", 200)
	if state.State != "stopped" {
		t.Fatal("merely opening FPK started a native business process")
	}
	controlState(t, f.client, entry.address, `{"action":"start"}`, 200)
	before := businessState(t, f.client, entry.address)
	state = controlState(t, f.client, entry.address, `{"action":"stop","confirm":true}`, 200)
	if !state.Paused || state.State != "paused" {
		t.Fatal("native manager did not own the stopped state")
	}
	code, html := localRequest(t, f.client, "GET", entry.address+localPrefix+"/", "")
	if code != 200 || !strings.Contains(html, "轻量控制服务仍在运行") || strings.Contains(html, "完全关闭应用请使用飞牛应用中心") {
		t.Fatal("native recovery page hid controls or promised FPK owned the application")
	}
	for _, path := range []string{"/_runtime/manifest", "/_runtime/panel.js"} {
		if code, _ := localRequest(t, f.client, "GET", entry.address+localPrefix+path, ""); code != 200 {
			t.Fatal("local management was stopped together with business")
		}
	}
	commit := f.repo.advance(t, "update while stopped")
	state = controlState(t, f.client, entry.address, `{"action":"update-backend","confirm":true}`, 200)
	if !state.Paused || state.State != "paused" || state.Source == nil || state.Source.Running != nil || state.Source.Workspace.Commit != commit {
		t.Fatal("updating a stopped application started it or lost its version evidence")
	}
	entry.close()
	entry = f.relay(t)
	state = controlState(t, f.client, entry.address, "", 200)
	if !state.Paused || state.State != "paused" {
		t.Fatal("restarting the FPK relay reset native management state")
	}
	controlState(t, f.client, entry.address, `{"action":"start"}`, 200)
	after := businessState(t, f.client, entry.address)
	if after.PID == before.PID || after.Version != "update while stopped" || after.Commit != commit {
		t.Fatal("native controls could not start the updated business after an FPK restart")
	}
	controlState(t, f.client, entry.address, `{"action":"restart","confirm":true}`, 200)
	if restarted := businessState(t, f.client, entry.address); restarted.PID == after.PID || restarted.Commit != commit {
		t.Fatal("restart did not replace the real business process")
	}
}

func TestLocalManagementValidatesControlIntentAndKeepsFailureTruthful(t *testing.T) {
	f := newIndependentFixture(t)
	entry := f.relay(t)
	controlState(t, f.client, entry.address, `{"action":"start"}`, 200)
	before := businessState(t, f.client, entry.address)
	for _, body := range []string{`{"action":"stop"}`, `{"action":"update-backend"}`,
		`{"action":"start","executable":"anything"}`, `{"action":"exec","confirm":true}`} {
		if code, _ := localRequest(t, f.client, "POST", entry.address+localPrefix+"/_runtime/control", body); code != 400 {
			t.Fatal("native manager accepted an unconfirmed or arbitrary command")
		}
	}
	dirty := filepath.Join(f.repo.directory, "uncommitted.txt")
	writeTest(t, dirty, "local edits")
	controlState(t, f.client, entry.address, `{"action":"update-backend","confirm":true}`, 503)
	if after := businessState(t, f.client, entry.address); after != before {
		t.Fatal("a rejected update stopped or replaced the running business")
	}
	if code, _ := localRequest(t, f.client, "POST", entry.address+localPrefix+"/_runtime/control", `{"action":"rebuild-local","confirm":true}`); code != 200 {
		t.Fatal("native management could not apply local changes")
	}
}
