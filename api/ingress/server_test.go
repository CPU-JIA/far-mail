package ingress

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSnapshotResolveRecipientLongestSuffix(t *testing.T) {
	accountID := uuid.New()
	snap := &snapshot{
		domains: []snapshotDomain{
			{id: 10, domain: "example.com"},
			{id: 20, domain: "mail.example.com"},
		},
		domainsByName: map[string]snapshotDomain{
			"example.com":      {id: 10, domain: "example.com"},
			"mail.example.com": {id: 20, domain: "mail.example.com"},
		},
		defaultAccountID:  accountID,
		mailboxTTLMinutes: 0,
	}
	resolved, ok := snap.resolveRecipient("User@x.mail.example.com")
	if !ok {
		t.Fatal("expected recipient to resolve")
	}
	if resolved.domainID != 20 {
		t.Fatalf("expected longest suffix domain id 20, got %d", resolved.domainID)
	}
	if resolved.address != "user@x.mail.example.com" || resolved.localPart != "user" || resolved.domainName != "x.mail.example.com" {
		t.Fatalf("unexpected resolved recipient: %+v", resolved)
	}
	if resolved.accountID != accountID || resolved.mailboxTTLMinutes != 0 {
		t.Fatalf("snapshot metadata not propagated: %+v", resolved)
	}
}

func TestSnapshotResolveRecipientRejectsUnknownDomain(t *testing.T) {
	snap := &snapshot{
		domains:          []snapshotDomain{{id: 10, domain: "example.com"}},
		domainsByName:    map[string]snapshotDomain{"example.com": {id: 10, domain: "example.com"}},
		defaultAccountID: uuid.New(), mailboxTTLMinutes: 30,
	}
	if _, ok := snap.resolveRecipient("user@evil.test"); ok {
		t.Fatal("expected unknown domain to be rejected")
	}
}

func TestSubmitJobHonorsCancellationWhenQueueIsFull(t *testing.T) {
	server := NewServer(nil, Config{QueueSize: 1, Workers: 1})
	server.jobs <- deliveryJob{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if server.submitJob(ctx, deliveryJob{}) {
		t.Fatal("submitJob accepted a cancelled job into a full queue")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("cancelled queue submission blocked for %s", elapsed)
	}
}

func TestDrainQueuedJobsReleasesCapacityAndNotifiesWaiter(t *testing.T) {
	server := NewServer(nil, Config{QueueSize: 1, Workers: 1})
	if !server.acquireSlot() {
		t.Fatal("failed to reserve ingress slot")
	}
	result := make(chan deliveryResult, 1)
	server.jobs <- deliveryJob{result: result}
	server.drainQueuedJobs(context.Canceled)
	if got := len(server.slots); got != 1 {
		t.Fatalf("slots after queue drain = %d, want 1", got)
	}
	select {
	case value := <-result:
		if value.status != deliveryTempFail {
			t.Fatalf("drain result status = %v, want temporary failure", value.status)
		}
	case <-time.After(time.Second):
		t.Fatal("queued waiter was not notified")
	}
	if server.jobsCancelled.Load() != 1 {
		t.Fatalf("jobs_cancelled = %d, want 1", server.jobsCancelled.Load())
	}
}
