package admin

import (
	"context"
	"net/http"
)

func ctxWithNonce(r *http.Request) context.Context {
	return context.WithValue(context.WithValue(r.Context(), nonceKey, "n0nce"), csrfKey, csrfValue)
}
