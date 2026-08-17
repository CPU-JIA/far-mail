package ingress

import (
	"testing"
	"time"

	"farmail/model"

	"github.com/google/uuid"
)

func TestEmailEventSubscriptionsAreMailboxScoped(t *testing.T) {
	server := &Server{}
	mailboxA := uuid.New()
	mailboxB := uuid.New()
	eventsA, unsubscribeA := server.Subscribe(mailboxA)
	defer unsubscribeA()
	eventsB, unsubscribeB := server.Subscribe(mailboxB)
	defer unsubscribeB()
	allEvents, unsubscribeAll := server.Subscribe(uuid.Nil)
	defer unsubscribeAll()

	event := model.MailboxEmailEvent{
		MailboxID: mailboxA,
		Email: model.EmailSummary{
			ID:         uuid.New(),
			ReceivedAt: time.Now(),
		},
	}
	server.publishEmailEvent(event)

	select {
	case received := <-eventsA:
		if received.Email.ID != event.Email.ID {
			t.Fatalf("unexpected event: %+v", received)
		}
	default:
		t.Fatal("matching mailbox subscriber did not receive event")
	}
	select {
	case <-allEvents:
	default:
		t.Fatal("global subscriber did not receive event")
	}
	select {
	case received := <-eventsB:
		t.Fatalf("different mailbox received event: %+v", received)
	default:
	}
}

func TestEmailEventPublisherDropsInsteadOfBlocking(t *testing.T) {
	server := &Server{}
	mailboxID := uuid.New()
	_, unsubscribe := server.Subscribe(mailboxID)
	defer unsubscribe()
	for i := 0; i < 17; i++ {
		server.publishEmailEvent(model.MailboxEmailEvent{MailboxID: mailboxID})
	}
	if got := server.eventsDropped.Load(); got != 1 {
		t.Fatalf("events dropped = %d, want 1", got)
	}
}
