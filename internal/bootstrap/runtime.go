//go:build windows

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"highloader/internal/platform"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var Version = "development"
var SourceDigest = "unrecorded"
var ErrExistingInstance = errors.New("интерфейс уже запущенного Highloader открыт")

type Options struct {
	DataDir string
	Handler http.Handler
	// BuildHandler runs only after acquiring instance ownership. On error it must
	// return cleanup for any acquired resources; cleanup must join its workers.
	BuildHandler   func(context.Context, string, *log.Logger) (http.Handler, func(context.Context) error, error)
	Browser        func(string) error
	Message        func(string, string) error
	DisableBrowser bool
}
type Descriptor struct {
	PID    int    `json:"pid"`
	Origin string `json:"origin"`
	Epoch  string `json:"epoch"`
	Secret string `json:"secret"`
}
type Runtime struct {
	Origin   string
	Session  *Session
	Logger   *log.Logger
	server   *http.Server
	owner    *platform.Owner
	logFile  *os.File
	done     chan error
	once     sync.Once
	closeErr error
	shutdown func(context.Context) error
}

func Start(ctx context.Context, o Options) (*Runtime, error) {
	if o.Browser == nil {
		o.Browser = platform.OpenBrowser
	}
	if o.Message == nil {
		o.Message = platform.ShowMessage
	}
	if (o.Handler == nil) == (o.BuildHandler == nil) {
		return nil, fmt.Errorf("обработчик приложения отсутствует")
	}
	if o.DataDir == "" {
		var err error
		o.DataDir, err = platform.DefaultDataDir()
		if err != nil {
			return nil, err
		}
	}
	owner, err := platform.AcquireOwner(o.DataDir)
	if errors.Is(err, platform.ErrAlreadyRunning) {
		address, e := Reopen(ctx, o.DataDir)
		if e != nil {
			return nil, e
		}
		if !o.DisableBrowser {
			if e = o.Browser(address); e != nil {
				_ = o.Message("Highloader", "Откройте адрес в браузере:\n"+address)
			}
		}
		return nil, ErrExistingInstance
	}
	if err != nil {
		return nil, err
	}
	failed := true
	retainOwner := false
	defer func() {
		if failed && !retainOwner {
			owner.Close()
		}
	}()
	config, err := LoadConfig(owner.Dir)
	if err != nil {
		return nil, err
	}
	logger, file, err := OpenLog(owner.Dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if failed && !retainOwner {
			file.Close()
		}
	}()
	var shutdown func(context.Context) error
	defer func() {
		if failed && shutdown != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdown(cleanup) != nil {
				// uncertain cleanup cannot release ownership to another process.
				retainOwner = true
				logger.Print("Application cleanup failed; instance ownership retained")
			}
		}
	}()
	if o.BuildHandler != nil {
		o.Handler, shutdown, err = o.BuildHandler(ctx, owner.Dir, logger)
		if err != nil {
			return nil, err
		}
		if o.Handler == nil || shutdown == nil {
			return nil, errors.New("owned application factory requires handler and shutdown")
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer func() {
		if failed {
			listener.Close()
		}
	}()
	epoch, err := Token()
	if err != nil {
		return nil, err
	}
	secret, err := Token()
	if err != nil {
		return nil, err
	}
	origin := "http://" + listener.Addr().String()
	session, err := NewSession(origin, epoch, secret)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: session.Handler(o.Handler), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10}
	runtime := &Runtime{Origin: origin, Session: session, Logger: logger, server: server, owner: owner, logFile: file, done: make(chan error, 1), shutdown: shutdown}
	if err = AtomicJSON(filepath.Join(owner.Dir, "owner.json"), Descriptor{os.Getpid(), origin, epoch, secret}); err != nil {
		return nil, err
	}
	go func() {
		e := server.Serve(listener)
		if errors.Is(e, http.ErrServerClosed) {
			e = nil
		}
		runtime.done <- e
		close(runtime.done)
	}()
	failed = false
	logger.Printf("Loopback server ready at %s; version=%s", origin, Version)
	if config.OpenBrowser && !o.DisableBrowser {
		address, e := session.LaunchURL()
		if e == nil {
			e = o.Browser(address)
		}
		if e != nil {
			logger.Print("Browser launch failed; native fallback shown")
			_ = o.Message("Highloader", "Не удалось открыть браузер. Откройте адрес:\n"+address)
		}
	}
	return runtime, nil
}

func Reopen(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var d Descriptor
		b, e := os.ReadFile(filepath.Join(dir, "owner.json"))
		if e == nil && len(b) <= 4096 && json.Unmarshal(b, &d) == nil {
			u, e := url.Parse(d.Origin)
			if e == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.Path == "" && d.Secret != "" && d.Epoch != "" {
				req, e := http.NewRequestWithContext(ctx, "POST", d.Origin+"/internal/reopen", nil)
				if e != nil {
					return "", e
				}
				req.Header.Set("X-Highloader-Owner", d.Secret)
				response, e := client.Do(req)
				if e == nil {
					var result struct {
						Epoch string `json:"epoch"`
						URL   string `json:"url"`
					}
					decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result)
					response.Body.Close()
					launch, parseErr := url.Parse(result.URL)
					if response.StatusCode == 200 && decodeErr == nil && parseErr == nil && result.Epoch == d.Epoch && launch.Scheme == u.Scheme && launch.Host == u.Host && launch.Path == "/bootstrap" {
						return result.URL, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("Highloader запускается или не отвечает; повторите запуск: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	r.once.Do(func() {
		r.closeErr = r.server.Shutdown(ctx)
		if r.closeErr != nil {
			_ = r.server.Close()
		}
		if r.shutdown != nil {
			if err := r.shutdown(ctx); err != nil {
				r.closeErr = errors.Join(r.closeErr, err)
				r.Logger.Print("Application shutdown failed; instance ownership retained until process exit")
				return
			}
		}
		_ = os.Remove(filepath.Join(r.owner.Dir, "owner.json"))
		r.Logger.Print("Loopback server stopped; ownership released")
		r.closeErr = errors.Join(r.closeErr, r.logFile.Close(), r.owner.Close())
	})
	return r.closeErr
}

func (r *Runtime) Wait() error { return <-r.done }
