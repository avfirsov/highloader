package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("url", "", "owned fixture /fast URL")
	flag.Parse()
	u, err := url.Parse(*target)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/fast" || u.RawQuery != "" || u.User != nil {
		log.Fatal("expected owned http://127.0.0.1:port/fast")
	}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 64, MaxIdleConnsPerHost: 64}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	stats := func() uint64 {
		r, e := client.Get("http://" + u.Host + "/stats")
		if e != nil {
			log.Fatal(e)
		}
		defer r.Body.Close()
		var value struct{ Completed uint64 }
		if e = json.NewDecoder(r.Body).Decode(&value); e != nil {
			log.Fatal(e)
		}
		return value.Completed
	}
	before := stats()
	bodies := make([][]byte, 10)
	for i := range bodies {
		bodies[i] = []byte(fmt.Sprintf(`{"id":%d,"payload":"%s"}`, i, strings.Repeat("x", 10219)))
	}
	var completed, failures atomic.Uint64
	var wg sync.WaitGroup
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for ctx.Err() == nil {
				r, e := client.Post(*target, "application/json", bytes.NewReader(bodies[index%10]))
				index++
				if e != nil {
					failures.Add(1)
					continue
				}
				n, e := io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if e != nil || n != 1024 || r.StatusCode != 200 {
					failures.Add(1)
				} else {
					completed.Add(1)
				}
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	after := stats()
	result := map[string]any{"workers": 32, "seconds": elapsed, "completed": completed.Load(), "errors": failures.Load(), "fixtureCompletedDelta": after - before, "rps": float64(completed.Load()) / elapsed, "targetMultiple": float64(completed.Load()) / elapsed / 2000}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	if failures.Load() != 0 || after-before != completed.Load() {
		log.Fatal("fixture reconciliation failed")
	}
}
