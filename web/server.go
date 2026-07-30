package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

type Server struct {
	Handler           http.Handler
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

func (server Server) Serve(ctx context.Context, listener net.Listener) error {
	if server.Handler == nil {
		return errors.New("godjango web: server handler is required")
	}
	if listener == nil {
		return errors.New("godjango web: server listener is required")
	}
	shutdownTimeout := server.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 10 * time.Second
	}
	httpServer := &http.Server{
		Handler:           server.Handler,
		ReadHeaderTimeout: server.ReadHeaderTimeout,
		ReadTimeout:       server.ReadTimeout,
		WriteTimeout:      server.WriteTimeout,
		IdleTimeout:       server.IdleTimeout,
		MaxHeaderBytes:    server.MaxHeaderBytes,
	}
	served := make(chan error, 1)
	go func() {
		served <- httpServer.Serve(listener)
	}()

	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = httpServer.Close()
		}
		serveErr := <-served
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(shutdownErr, serveErr)
		}
		return shutdownErr
	}
}
