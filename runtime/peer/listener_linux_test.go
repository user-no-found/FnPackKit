package peer

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(os.TempDir(), "fpp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(directory, "s")
}

func TestKernelPeerBoundary(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if _, err := UID(left); err == nil {
		t.Fatal("non-Unix peer accepted")
	}
	path := shortSocketPath(t)
	base, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	listener, err := NewListener(base, []uint32{uint32(os.Getuid())}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.CloseConnections()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if uid, err := UID(accepted.(*lifetimeConn).Conn); err != nil || uid != uint32(os.Getuid()) {
		t.Fatalf("kernel credentials: uid=%d err=%v", uid, err)
	}
	listener.CloseConnections()
	if _, err = client.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection remained open")
	}
}

func TestUntrustedPeerRejected(t *testing.T) {
	path := shortSocketPath(t)
	base, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	listener, err := NewListener(base, []uint32{uint32(os.Getuid()) + 1}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if conn != nil {
			conn.Close()
		}
		ended <- err
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("untrusted connection was not closed: %v", err)
	}
	base.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
}

func TestContextOnlyMarksOwnAcceptedConnection(t *testing.T) {
	path := shortSocketPath(t)
	base, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	listener, err := NewListener(base, []uint32{uint32(os.Getuid())}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	if uid, ok := TrustedUID(listener.ConnContext(context.Background(), accepted)); !ok || uid != uint32(os.Getuid()) {
		t.Fatal("verified connection unmarked")
	}
	if _, ok := TrustedUID(listener.ConnContext(context.Background(), client)); ok {
		t.Fatal("arbitrary connection marked")
	}
	other := &Listener{}
	if _, ok := TrustedUID(other.ConnContext(context.Background(), accepted)); ok {
		t.Fatal("another listener's connection marked")
	}
}
