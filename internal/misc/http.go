package misc

import (
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// getClientIP extracts the client IP address from the request
func GetClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header (used when behind a proxy)
	xForwardedFor := r.Header.Get("X-Forwarded-For")
	if xForwardedFor != "" {
		// The first IP in the list is the client IP
		ips := strings.Split(xForwardedFor, ",")
		return strings.TrimSpace(ips[0])
	}

	// Fallback to RemoteAddr if no X-Forwarded-For header
	// RemoteAddr is in the form "IP:PORT", so we need to extract the IP
	clientIP := strings.Split(r.RemoteAddr, ":")[0]
	return clientIP
}

// GetClientPort extracts the client port from the request
func GetClientPort(r *http.Request) string {
	remoteAddr := r.RemoteAddr
	if remoteAddr != "" {
		parts := strings.Split(remoteAddr, ":")
		if len(parts) == 2 {
			return parts[1]
		}
	}
	return "unknown"
}

func LogMiddlewareHandler(handler http.Handler, logger *zap.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        clientIP := GetClientIP(r)

        logger.Debug("New incoming request", zap.String("Method", r.Method), zap.String("Url", r.URL.String()), zap.String("ClientIP", clientIP), zap.String("Port", GetClientPort(r)))
		start := time.Now()

		handler.ServeHTTP(w, r)

		duration := time.Since(start)
        logger.Info("Request completed", zap.String("Method", r.Method), zap.String("Url", r.URL.String()), zap.String("ClientIP", clientIP), zap.String("Port", GetClientPort(r)), zap.Duration("Duration", duration))
	})
}
