// Package peer accepts only explicitly trusted Linux Unix socket peers.
package peer

import (
	"errors"
	"net"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Listener rejects untrusted connections before HTTP parsing.
type Listener struct {
	net.Listener
	trusted     []uint32
	lifetime    time.Duration
	mu          sync.Mutex
	connections map[*lifetimeConn]struct{}
}

func (listener *Listener) Accept() (net.Conn, error) {
	for {
		conn, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := UID(conn)
		if err != nil || !slices.Contains(listener.trusted, uid) {
			conn.Close()
			continue
		}
		bounded := &lifetimeConn{Conn: conn, listener: listener, uid: uid, closed: make(chan struct{})}
		bounded.timer = time.NewTimer(listener.lifetime)
		listener.mu.Lock()
		listener.connections[bounded] = struct{}{}
		listener.mu.Unlock()
		go func() {
			select {
			case <-bounded.timer.C:
				bounded.Close()
			case <-bounded.closed:
			}
		}()
		return bounded, nil
	}
}

// UID obtains credentials from the kernel, never HTTP headers.
func UID(conn net.Conn) (uint32, error) {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("gateway requires Unix transport")
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var optionErr error
	err = raw.Control(func(fd uintptr) {
		cred, optionErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return 0, err
	}
	if optionErr != nil {
		return 0, optionErr
	}
	return cred.Uid, nil
}

type lifetimeConn struct {
	net.Conn
	listener *Listener
	uid      uint32
	timer    *time.Timer
	closed   chan struct{}
	once     sync.Once
}

func (conn *lifetimeConn) Close() error {
	var err error
	conn.once.Do(func() {
		close(conn.closed)
		if conn.timer != nil {
			conn.timer.Stop()
		}
		err = conn.Conn.Close()
		conn.listener.mu.Lock()
		delete(conn.listener.connections, conn)
		conn.listener.mu.Unlock()
	})
	return err
}

// CloseConnections closes active accepted connections.
func (listener *Listener) CloseConnections() {
	listener.mu.Lock()
	connections := make([]*lifetimeConn, 0, len(listener.connections))
	for conn := range listener.connections {
		connections = append(connections, conn)
	}
	listener.mu.Unlock()
	for _, conn := range connections {
		conn.Close()
	}
}

// NewListener binds explicit peer trust and a positive connection lifetime.
func NewListener(base net.Listener, trusted []uint32, lifetime time.Duration) (*Listener, error) {
	if base == nil || len(trusted) == 0 || lifetime <= 0 {
		return nil, errors.New("listener, peer UIDs and lifetime are required")
	}
	if _, ok := base.(*net.UnixListener); !ok {
		return nil, errors.New("gateway requires Unix listener")
	}
	return &Listener{Listener: base, trusted: append([]uint32(nil), trusted...), lifetime: lifetime, connections: make(map[*lifetimeConn]struct{})}, nil
}
