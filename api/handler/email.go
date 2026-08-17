package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"farmail/middleware"
	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type EmailEventSource interface {
	Subscribe(mailboxID uuid.UUID) (<-chan model.MailboxEmailEvent, func())
}

type EmailHandler struct {
	store       *store.Store
	eventSource EmailEventSource
}

func NewEmailHandler(s *store.Store, sources ...EmailEventSource) *EmailHandler {
	handler := &EmailHandler{store: s}
	if len(sources) > 0 {
		handler.eventSource = sources[0]
	}
	return handler
}

// Events streams newly committed email metadata. Authentication is inherited
// from the console/API route group and credentials are never placed in URLs.
func (h *EmailHandler) Events(c *gin.Context) {
	if h.eventSource == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "realtime email events unavailable"})
		return
	}
	account := middleware.GetAccount(c)
	mailboxID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}
	if _, err := h.store.GetMailbox(c.Request.Context(), mailboxID, account.ID, donationCreatorToken(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
		return
	}

	events, unsubscribe := h.eventSource.Subscribe(mailboxID)
	defer unsubscribe()
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	controller := http.NewResponseController(c.Writer)
	writeEvent := func(format string, args ...any) bool {
		// The HTTP server's global WriteTimeout cannot remain active for a
		// long-lived stream, but each individual write still needs a bound so a
		// disconnected or non-reading client cannot pin a handler forever.
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(c.Writer, format, args...); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !writeEvent("event: ready\ndata: {\"mailbox_id\":%q}\n\n", mailboxID.String()) {
		return
	}

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if !writeEvent(": heartbeat\n\n") {
				return
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if !writeEvent("id: %s\nevent: email\ndata: %s\n\n", event.Email.ID.String(), payload) {
				return
			}
		}
	}
}

// GET /console/v1/mailboxes/:id/emails 或 /api/v1/... - 列出邮件
func (h *EmailHandler) List(c *gin.Context) {
	account := middleware.GetAccount(c)
	mailboxID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}

	emails, total, err := h.store.ListEmails(c.Request.Context(), mailboxID, account.ID, donationCreatorToken(c), page, size)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  emails,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// GET /console/v1/mailboxes/:id/emails/:email_id 或 /api/v1/... - 读取邮件
func (h *EmailHandler) Get(c *gin.Context) {
	account := middleware.GetAccount(c)
	mailboxID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}

	emailID, err := parseUUID(c.Param("email_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email id"})
		return
	}

	email, err := h.store.GetEmail(c.Request.Context(), emailID, mailboxID, account.ID, donationCreatorToken(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "email not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"email": email})
}

// DELETE /console/v1/mailboxes/:id/emails/:email_id 或 /api/v1/... - 删除邮件
func (h *EmailHandler) Delete(c *gin.Context) {
	account := middleware.GetAccount(c)
	mailboxID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}

	emailID, err := parseUUID(c.Param("email_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email id"})
		return
	}

	if err := h.store.DeleteEmail(c.Request.Context(), emailID, mailboxID, account.ID, donationCreatorToken(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "email not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "email deleted"})
}
