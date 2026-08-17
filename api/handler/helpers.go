package handler

import (
	"farmail/middleware"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func parseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

func donationCreatorToken(c *gin.Context) *uuid.UUID {
	token := middleware.GetToken(c)
	if token == nil || token.TokenKind != "donation" || token.ID == uuid.Nil {
		return nil
	}
	id := token.ID
	return &id
}
