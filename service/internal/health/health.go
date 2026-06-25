package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
)

type Server struct {
	ready  atomic.Bool
	server *http.Server
}

func NewServer(port string) *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.ready.Load() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
		}
	})
	s.server = &http.Server{Addr: ":" + port, Handler: mux}
	return s
}

func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *Server) Start() {
	go func() {
		slog.Info("health server starting", "addr", s.server.Addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health server error", "error", err)
		}
	}()
}

func (s *Server) Shutdown(ctx context.Context) {
	s.server.Shutdown(ctx)
}
