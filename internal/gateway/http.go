package gateway

import (
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func LogMiddlewareHandler(handler http.Handler, logger *zap.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := getClientIP(r)
		clientPort := getClientPort(r)

		logger.Debug("New incoming request",
			zap.String("method", r.Method),
			zap.String("url", r.URL.String()),
			zap.String("clientIP", clientIP),
			zap.String("clientPort", clientPort),
			zap.String("userAgent", r.UserAgent()))
		start := time.Now()

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		handler.ServeHTTP(rw, r)

		duration := time.Since(start)
		logger.Info("Request completed",
			zap.String("method", r.Method),
			zap.String("url", r.URL.String()),
			zap.String("clientIP", clientIP),
			zap.String("clientPort", clientPort),
			zap.Int("status", rw.status),
			zap.Duration("duration", duration))
	})
}

// getClientIP extracts the client IP address from the request
func getClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header (used when behind a proxy)
	xForwardedFor := r.Header.Get("X-Forwarded-For")
	if xForwardedFor != "" {
		// The first IP in the list is the client IP
		ips := strings.Split(xForwardedFor, ",")
		return strings.TrimSpace(ips[0])
	}

	// Check for X-Real-IP header
	xRealIP := r.Header.Get("X-Real-IP")
	if xRealIP != "" {
		return xRealIP
	}

	// Fallback to RemoteAddr - use net.SplitHostPort for proper IPv6 support
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If SplitHostPort fails, return the original RemoteAddr
		return r.RemoteAddr
	}
	return host
}

// getClientPort extracts the client port from the request
func getClientPort(r *http.Request) string {
	// Use net.SplitHostPort for proper IPv6 support
	_, port, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	return port
}
