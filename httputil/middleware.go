package httputil

import "net/http"

// HSTS will automatically inform the browser that the website can only be accessed through HTTPS.
func HSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This enforces the use of HTTPS for 1 year, including present and future subdomains.
		// Chrome and Mozilla Firefox maintain an HSTS preload list
		// issue : golang.org/issue/26162
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		next.ServeHTTP(w, r)
	})
}
