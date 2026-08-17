package store

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildAPIRequestEventInsertFiltersDeletedTokens(t *testing.T) {
	events := []APIRequestEvent{
		{
			TokenID:   uuid.New(),
			Method:    "GET",
			Route:     "/api/v1/domains",
			Status:    200,
			LatencyMS: 4,
			RequestID: "request-1",
			CreatedAt: time.Now(),
		},
		{
			TokenID:   uuid.New(),
			Method:    "POST",
			Route:     "/api/v1/mailboxes",
			Status:    201,
			LatencyMS: 8,
			RequestID: "request-2",
			CreatedAt: time.Now(),
		},
	}

	query, args := buildAPIRequestEventInsert(events)
	if !strings.Contains(query, "JOIN account_tokens AS token ON token.id = event.token_id") {
		t.Fatal("event insert must skip events whose token was deleted before the batch flush")
	}
	if strings.Contains(query, "created_at) VALUES") {
		t.Fatal("event insert must select through the live-token join")
	}
	if len(args) != len(events)*7 {
		t.Fatalf("unexpected argument count: got %d want %d", len(args), len(events)*7)
	}
}
