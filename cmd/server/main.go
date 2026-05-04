package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"omp-session-viewer/backend/internal/httpapi"
	"omp-session-viewer/backend/internal/ompstore"
)

func main() {
	ompRoot := flag.String("omp-root", "~/.omp/agent", "path to OMP agent root")
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	frontendOrigin := flag.String("frontend-origin", "http://localhost:5173", "allowed frontend origin")
	flag.Parse()

	root, err := expandPath(*ompRoot)
	if err != nil {
		log.Fatal(err)
	}

	store, err := ompstore.Open(root)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	log.Printf("serving OMP session viewer API on http://%s", *addr)
	log.Printf("using OMP root %s", root)
	server := &http.Server{
		Addr:    *addr,
		Handler: httpapi.NewServer(store, *frontendOrigin),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatal(err)
		}
		return
	case <-ctx.Done():
	}

	log.Print("shutting down OMP session viewer API")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		log.Fatal(err)
	}
}

func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}
