package handler

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"farmail/middleware"
	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type integrationEventSource struct {
	mu     sync.Mutex
	events chan model.MailboxEmailEvent
	active int
}

func (source *integrationEventSource) Subscribe(uuid.UUID) (<-chan model.MailboxEmailEvent, func()) {
	source.mu.Lock()
	source.events = make(chan model.MailboxEmailEvent, 4)
	source.active++
	events := source.events
	source.mu.Unlock()
	var once sync.Once
	return events, func() {
		once.Do(func() {
			source.mu.Lock()
			source.active--
			source.mu.Unlock()
		})
	}
}

func (source *integrationEventSource) subscriberCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.active
}

func TestEmailEventsDisconnectUnsubscribes(t *testing.T) {
	dsn := os.Getenv("FARMAIL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FARMAIL_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer database.Close()

	snapshot, err := database.LoadIngressSnapshot(ctx)
	if err != nil {
		t.Fatalf("load owner account: %v", err)
	}
	domainName := "sse-" + uuid.NewString() + ".test"
	domain, err := database.AddDomain(ctx, domainName)
	if err != nil {
		t.Fatalf("add domain: %v", err)
	}
	mailbox, err := database.CreateMailbox(ctx, snapshot.DefaultAccountID, nil, "events", domain.ID, "events@"+domainName, 30)
	if err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	defer func() {
		_ = database.DeleteMailbox(context.Background(), mailbox.ID, snapshot.DefaultAccountID, nil)
		_ = database.DeleteDomain(context.Background(), domain.ID)
	}()

	source := &integrationEventSource{}
	handler := NewEmailHandler(database, source)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.AccountKey, &model.Account{ID: snapshot.DefaultAccountID, IsAdmin: true})
		c.Next()
	})
	router.GET("/mailboxes/:id/events", handler.Events)
	server := httptest.NewServer(router)
	defer server.Close()

	requestCtx, cancelRequest := context.WithCancel(ctx)
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL+"/mailboxes/"+mailbox.ID.String()+"/events", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("unexpected event response: status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(response.Body)
	ready := make(chan string, 1)
	readErrors := make(chan error, 1)
	go func() {
		var received strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				readErrors <- err
				return
			}
			received.WriteString(line)
			if strings.Contains(received.String(), "event: ready") && strings.HasSuffix(received.String(), "\n\n") {
				ready <- received.String()
				return
			}
		}
	}()
	select {
	case <-ready:
	case err := <-readErrors:
		t.Fatalf("read ready event: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ready event")
	}
	if source.subscriberCount() != 1 {
		t.Fatalf("active subscribers = %d, want 1", source.subscriberCount())
	}

	source.events <- model.MailboxEmailEvent{
		MailboxID: mailbox.ID,
		Email:     model.EmailSummary{ID: uuid.New(), Sender: "sender@example.test", Subject: "event", ParsedCode: "123456", ParsedLink: "https://example.test/verify", ReceivedAt: time.Now()},
	}
	emailEvent := make(chan string, 1)
	go func() {
		var received strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				readErrors <- err
				return
			}
			received.WriteString(line)
			if strings.Contains(received.String(), "event: email") && strings.HasSuffix(received.String(), "\n\n") {
				emailEvent <- received.String()
				return
			}
		}
	}()
	select {
	case payload := <-emailEvent:
		if !strings.Contains(payload, `"subject":"event"`) {
			t.Fatalf("unexpected event payload: %s", payload)
		}
		if !strings.Contains(payload, `"parsed_code":"123456"`) || !strings.Contains(payload, `"parsed_link":"https://example.test/verify"`) {
			t.Fatalf("event payload is missing parsed automation fields: %s", payload)
		}
	case err := <-readErrors:
		t.Fatalf("read email event: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for email event")
	}

	cancelRequest()
	_ = response.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for source.subscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if source.subscriberCount() != 0 {
		t.Fatalf("subscriber leaked after disconnect: %d", source.subscriberCount())
	}
}
