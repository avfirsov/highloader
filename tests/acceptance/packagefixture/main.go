package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	parent := flag.String("parent", "logs", "existing workspace directory for a new isolated fixture")
	flag.Parse()
	dir, err := os.MkdirTemp(*parent, "package-acceptance-")
	if err != nil {
		log.Fatal(err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	templates := filepath.Join(dir, "templates")
	if err = os.Mkdir(templates, 0700); err != nil {
		log.Fatal(err)
	}
	bodies := make([][]byte, 10)
	for i := range bodies {
		bodies[i] = []byte(fmt.Sprintf(`{"id":%d,"payload":"%s"}`, i, strings.Repeat("x", 10219)))
		if err = os.WriteFile(filepath.Join(templates, fmt.Sprintf("%02d.json", i)), bodies[i], 0600); err != nil {
			log.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	var accepted, completed, bad, active, peak atomic.Uint64
	response := bytes.Repeat([]byte("r"), 1024)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": accepted.Load(), "completed": completed.Load(), "bad": bad.Load(), "active": active.Load(), "peakActive": peak.Load(), "utc": time.Now().UTC()})
	})
	for _, path := range []string{"/fast", "/slow"} {
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
			body, readErr := io.ReadAll(io.LimitReader(r.Body, 10241))
			valid := false
			for _, expected := range bodies {
				if bytes.Equal(body, expected) {
					valid = true
					break
				}
			}
			if readErr != nil || !valid {
				bad.Add(1)
				http.Error(w, "invalid fixture body", 400)
				return
			}
			accepted.Add(1)
			n := active.Add(1)
			defer active.Add(^uint64(0))
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			if r.URL.Path == "/slow" {
				select {
				case <-time.After(2 * time.Second):
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			if _, err := w.Write(response); err == nil {
				completed.Add(1)
			}
		})
	}
	ready := map[string]any{"pid": os.Getpid(), "directory": dir, "templates": templates, "dataDirectory": filepath.Join(dir, "app-data"), "fastURL": origin + "/fast", "slowURL": origin + "/slow", "statsURL": origin + "/stats", "requestBytes": 10240, "responseBytes": 1024}
	b, err := json.MarshalIndent(ready, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "fixture.json"), b, 0600); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(b))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	log.Fatal(server.Serve(listener))
}
