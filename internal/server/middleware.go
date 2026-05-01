package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return s.recoverMiddleware(s.loggingMiddleware(s.authMiddleware(next)))
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Dur("duration", time.Since(started)).
			Msg("http request handled")
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error().Interface("panic", recovered).Msg("panic recovered")
				writeJSON(w, http.StatusInternalServerError, apiResponse{Success: false, Error: "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requiresAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		expected := "Bearer " + s.options.Auth.BearerToken
		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
			if s.options.Prometheus.Enabled && r.URL.Path == s.options.Prometheus.Path {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Error: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requiresAuth(path string) bool {
	if !s.options.Auth.Enabled {
		return false
	}
	if strings.HasPrefix(path, "/api/v1/") {
		return true
	}
	if s.options.Prometheus.Enabled && s.options.Prometheus.ProtectWithAuth && path == s.options.Prometheus.Path {
		return true
	}
	return false
}
