package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tompickens06-tech/1f916-client/internal/f916"
	"github.com/tompickens06-tech/1f916-client/internal/web"
)

func isContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusWriter) WriteHeader(status int) {
	w.statusCode = status
	w.ResponseWriter.WriteHeader(status)
}

func main() {
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8080"
	}

	f916Base := os.Getenv("F916_BASE")
	if f916Base == "" {
		f916Base = "https://1f916.ai"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	fmt.Printf("Starting 1f916 client v0.1 listening on %s (upstream: %s, log_level: %s)\n", listenAddr, f916Base, logLevel)

	// Check for non-loopback warning outside container
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = listenAddr
	}
	if host != "127.0.0.1" && host != "localhost" && !isContainer() {
		fmt.Printf("WARNING: Listening on non-loopback address %s outside a container!\n", listenAddr)
		fmt.Printf("WARNING: This process is now reachable by other machines on your local network!\n")
	}

	f916Client := f916.NewClient(f916Base)
	webServer, err := web.NewServer(f916Client)
	if err != nil {
		log.Fatalf("Failed to initialize web server: %v", err)
	}

	mux := http.NewServeMux()
	webServer.RegisterRoutes(mux)

	// Request logging middleware
	loggingMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
		mux.ServeHTTP(sw, r)
		duration := time.Since(start)
		log.Printf("%s %s -> HTTP %d (%v)\n", r.Method, r.URL.Path, sw.statusCode, duration)
	})

	handler := webServer.SecurityMiddleware(loggingMux)

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("Shutting down 1f916 client...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	fmt.Println("Server stopped cleanly.")
}
