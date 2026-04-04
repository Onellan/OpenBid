package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"openbid/internal/auth"
)

type requestContextKey string

const requestIDContextKey requestContextKey = "request_id"

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func requestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(requestIDContextKey).(string); ok {
		return value
	}
	return ""
}

func forwardedClientIP(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		raw := strings.TrimSpace(r.Header.Get(header))
		if raw == "" {
			continue
		}
		if index := strings.Index(raw, ","); index >= 0 {
			raw = strings.TrimSpace(raw[:index])
		}
		return raw
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (a *App) WithRequestObservability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = auth.RandomString(16)
		}
		ctx := context.WithValue(r.Context(), requestIDContextKey, requestID)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-Id", requestID)

		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf(
			"event=http_request request_id=%s method=%s path=%s query=%q status=%d bytes=%d duration_ms=%d remote_ip=%s forwarded_proto=%s",
			requestID,
			r.Method,
			r.URL.Path,
			r.URL.RawQuery,
			status,
			recorder.bytes,
			time.Since(started).Milliseconds(),
			forwardedClientIP(r),
			strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")),
		)
	})
}
