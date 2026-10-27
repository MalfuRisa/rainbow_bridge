package backup

import (
	"context"
	"testing"
	"time"
)

func TestMaintenanceWaitsForRegisteredWritesAndRejectsNewOnes(t *testing.T) {
	gate := NewMaintenance()
	release, err := gate.BeginWrite()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		entered <- gate.Enter(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	if !gate.Active() {
		t.Fatal("maintenance should become active before writes drain")
	}
	if _, err := gate.BeginWrite(); !IsMaintenance(err) {
		t.Fatalf("new write should be rejected, got %v", err)
	}
	release()
	select {
	case err := <-entered:
		if err != nil {
			t.Fatalf("maintenance enter failed after drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not finish after the registered write ended")
	}
	gate.Leave()
	if gate.Active() {
		t.Fatal("maintenance should be released")
	}
	if releaseWrite, err := gate.BeginWrite(); err != nil {
		t.Fatalf("writes should be accepted after maintenance: %v", err)
	} else {
		releaseWrite()
	}
}

func TestMaintenanceTimeoutReleasesGate(t *testing.T) {
	gate := NewMaintenance()
	release, err := gate.BeginWrite()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if err := gate.Enter(ctx); err == nil {
		t.Fatal("expected drain timeout")
	}
	if gate.Active() {
		t.Fatal("failed drain must clear the active flag")
	}
	release()
	if releaseWrite, err := gate.BeginWrite(); err != nil {
		t.Fatalf("writes should be accepted after the failed drain: %v", err)
	} else {
		releaseWrite()
	}
}
