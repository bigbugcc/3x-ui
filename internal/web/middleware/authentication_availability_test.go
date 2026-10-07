package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

func TestAuthenticationAvailabilityBlocksRequestsDuringRestore(t *testing.T) {
	t.Cleanup(func() { database.AuthenticationSuspended.Store(false) })
	r := gin.New()
	r.Use(AuthenticationAvailability())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, suspended := range []bool{false, true, false} {
		database.AuthenticationSuspended.Store(suspended)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		want := http.StatusNoContent
		if suspended {
			want = http.StatusServiceUnavailable
		}
		if w.Code != want {
			t.Fatalf("suspended=%v: status=%d", suspended, w.Code)
		}
	}
}
