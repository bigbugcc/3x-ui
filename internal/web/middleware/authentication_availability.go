package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

// AuthenticationAvailability blocks all new panel requests during DB replacement.
func AuthenticationAvailability() gin.HandlerFunc {
	return func(c *gin.Context) {
		if database.AuthenticationSuspended.Load() {
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		c.Next()
	}
}
