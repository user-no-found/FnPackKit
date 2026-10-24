package process

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestKillAndWaitHelper(t *testing.T) {
	if os.Getenv("FNP_KILL_HELPER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	fmt.Println("ready")
	for {
		time.Sleep(time.Second)
	}
}

func TestKillAndWaitReapsUncooperativeChild(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestKillAndWaitHelper$")
	command.Env = append(os.Environ(), "FNP_KILL_HELPER=1")
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child, err := Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer child.KillAndWait(2 * time.Second)
	ready := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(output).ReadString('\n')
		ready <- err == nil && line == "ready\n"
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("helper did not establish its signal handler")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("helper readiness timed out")
	}
	if err := child.Terminate(50 * time.Millisecond); err == nil || !child.Alive() {
		t.Fatal("graceful termination unexpectedly stopped the signal-ignoring child")
	}
	if err := child.KillAndWait(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if child.Alive() {
		t.Fatal("killed child remains alive")
	}
	exit, ok := child.Result().(*exec.ExitError)
	if !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("the owned child was not reaped after SIGKILL")
	}
	if err := child.KillAndWait(time.Second); err != nil {
		t.Fatal("repeat stop failed:", err)
	}
	if err := child.KillAndWait(0); err == nil {
		t.Fatal("invalid reap timeout accepted")
	}
}
