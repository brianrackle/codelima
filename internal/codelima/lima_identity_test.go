package codelima

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestLimaIdentitySerializesOnlyKeyInitializationAcrossClients(t *testing.T) {
	home := t.TempDir()
	first, second := NewLimaClient(t.TempDir()), NewLimaClient(t.TempDir())
	first.LimaHome, second.LimaHome = home, home
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	done := make(chan error, 1)
	go func() {
		done <- first.withIdentityInitialization(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.withIdentityInitialization(context.Background(), func() error { close(secondEntered); return nil })
	}()
	config := filepath.Join(home, "_config")
	if err := os.WriteFile(filepath.Join(config, "user"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "user.pub"), []byte("ssh-ed25519 partial"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
		t.Fatal("another start entered with a partial key pair")
	case <-time.After(75 * time.Millisecond):
	}
	public, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "user.pub"), ssh.MarshalAuthorizedKey(public), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("key gate serialized the entire first VM boot")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, "user"))
	if err != nil || string(data) != "private fixture" {
		t.Fatal("gate modified provider identity", err)
	}
}

func TestLimaIdentityCanceledWaiterNeverStartsAndFailureReleasesGate(t *testing.T) {
	c := NewLimaClient(t.TempDir())
	c.LimaHome = t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	done := make(chan error, 1)
	failure := errors.New("fixture startup failed before key generation")
	go func() {
		done <- c.withIdentityInitialization(context.Background(), func() error { close(entered); <-release; return failure })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	err := c.withIdentityInitialization(ctx, func() error { called = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatal("canceled waiter started", err, called)
	}
	unblock()
	if err := <-done; !errors.Is(err, failure) {
		t.Fatal("startup failure was hidden", err)
	}
	if err := c.withIdentityInitialization(context.Background(), func() error { called = true; return nil }); err != nil || !called {
		t.Fatal("failure retained the gate", err)
	}
}
