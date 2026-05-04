package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	if err := http.ListenAndServe(*addr, httpapi.NewServer(store, *frontendOrigin)); err != nil {
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
