package handler

import (
	"net/http"
	"strconv"
	"strings"

	"farmail/store"

	"github.com/gin-gonic/gin"
)

type AnalyticsHandler struct{ store *store.Store }

func NewAnalyticsHandler(s *store.Store) *AnalyticsHandler { return &AnalyticsHandler{store: s} }

func (h *AnalyticsHandler) Summary(c *gin.Context) {
	windowDays, _ := strconv.Atoi(strings.TrimSpace(c.Query("days")))
	if windowDays != 14 && windowDays != 30 {
		windowDays = 7
	}
	summary, days, err := h.store.GetAnalyticsSummary(c.Request.Context(), windowDays)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary, "days": days, "window_days": windowDays})
}
