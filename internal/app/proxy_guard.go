package app

import "net/http"

func (a *App) WithProxyRequirement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Config.AppEnv != "production" || r.URL.Path == "/healthz" || proxyHeadersPresent(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}
