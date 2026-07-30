package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestServerGracefulShutdownCompletesInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(started) })
		<-release
		_, _ = io.WriteString(response, "complete")
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		Handler:         handler,
		ShutdownTimeout: 2 * time.Second,
		ReadTimeout:     2 * time.Second,
		WriteTimeout:    2 * time.Second,
		IdleTimeout:     2 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(ctx, listener)
	}()

	responseBody := make(chan string, 1)
	requestError := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err != nil {
			requestError <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			requestError <- err
			return
		}
		responseBody <- string(body)
	}()
	<-started
	cancel()
	close(release)

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
	select {
	case err := <-requestError:
		t.Fatal(err)
	case body := <-responseBody:
		if body != "complete" {
			t.Fatalf("body = %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete")
	}
}

func TestServerReturnsShutdownTimeout(t *testing.T) {
	started := make(chan struct{})
	handler := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := Server{
		Handler:         handler,
		ShutdownTimeout: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(ctx, listener)
	}()
	go func() {
		_, _ = http.Get("http://" + listener.Addr().String())
	}()
	<-started
	cancel()
	err = <-served
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Serve error = %v, want deadline exceeded", err)
	}
}
