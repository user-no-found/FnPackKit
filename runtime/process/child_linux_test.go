package process

import (
	"os/exec"
	"testing"
	"time"
)

func TestCredentialAndNativeLifetime(t *testing.T) {
	if _, err := Credential(0, 0, nil); err == nil {
		t.Fatal("root accepted")
	}
	if _, err := Credential(1000, 1000, []uint32{4294967295}); err == nil {
		t.Fatal("sentinel group accepted")
	}
	child, err := Start(exec.Command("/bin/sh", "-c", "exit 0"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("child not reaped")
	}
	if child.Alive() || child.Result() != nil {
		t.Fatal("incorrect child result")
	}
}
