package httpapi

import (
	"context"
	"net/http"

	"bloomer/agent/internal/config"
)

func contextWithTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), config.QueryTimeout)
}
