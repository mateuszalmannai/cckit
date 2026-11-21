package capture

import (
	"context"
	"net/http"
)

func contextWith(r *http.Request, ex *Exchange) context.Context {
	return context.WithValue(r.Context(), ctxKey{}, ex)
}
