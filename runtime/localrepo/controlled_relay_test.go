package localrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFPKPanelControlsRealLocalBackendUpdatesAndRestart(t *testing.T) {
	f := newIndependentFixture(t)
	entry := f.controlledRelay(t)
	code, manifest := localRequest(t, f.client, "GET", entry.address+localPrefix+"/_runtime/manifest", "")
	if code != 200 || !strings.Contains(manifest, "FPK 后端管理") || !strings.Contains(manifest, "update-backend") {
		t.Fatal("FPK does not expose its own backend management panel")
	}
	controlState(t, f.client, entry.address, `{"action":"start"}`, 200)
	old := businessState(t, f.client, entry.address)
	if code, _ := localRequest(t, f.client, "POST", entry.address+localPrefix+"/api/value", "data through FPK controls"); code != 204 {
		t.Fatal("business data write failed")
	}
	commit := f.repo.advance(t, "latest backend from remote")
	state := controlState(t, f.client, entry.address, `{"action":"update-backend","confirm":true}`, 200)
	updated := businessState(t, f.client, entry.address)
	if updated.PID == old.PID || updated.Version != "latest backend from remote" || updated.Commit != commit ||
		updated.Saved != "data through FPK controls" || state.Source.Running.Commit != commit {
		t.Fatal("FPK update did not stop the old backend and start the real latest backend", old, updated)
	}
	controlState(t, f.client, entry.address, `{"action":"stop","confirm":true}`, 200)
	state = controlState(t, f.client, entry.address, "", 200)
	if !state.Paused || state.State != "paused" {
		t.Fatal("FPK did not report the local backend's stopped state")
	}
	entry.close()
	entry = f.controlledRelay(t)
	if state := controlState(t, f.client, entry.address, "", 200); !state.Paused {
		t.Fatal("FPK restart replaced authoritative local management state")
	}
	controlState(t, f.client, entry.address, `{"action":"start"}`, 200)
	started := businessState(t, f.client, entry.address)
	controlState(t, f.client, entry.address, `{"action":"restart","confirm":true}`, 200)
	restarted := businessState(t, f.client, entry.address)
	if restarted.PID == started.PID || restarted.Version != updated.Version || restarted.Saved != updated.Saved {
		t.Fatal("FPK restart did not replace the actual backend while preserving data")
	}
	if log, err := os.ReadFile(filepath.Join(f.config.Data, "operations.log")); err != nil || !strings.Contains(string(log), "update-backend") {
		t.Fatal("local management did not execute the update")
	}
	entry.close()
	if native := businessState(t, f.client, f.address); native != restarted {
		t.Fatal("closing the FPK controller stopped its independently running local backend")
	}
}
