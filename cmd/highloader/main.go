package main

import (
	"context"
	"errors"
	"flag"
	"highloader/internal/api"
	"highloader/internal/app"
	"highloader/internal/bootstrap"
	"highloader/internal/platform"
	"highloader/web"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, bootstrap.ErrExistingInstance) {
		_ = platform.ShowMessage("Highloader — ошибка", err.Error())
		os.Exit(1)
	}
}

func run() error {
	data := flag.String("data-dir", "", "Каталог данных (по умолчанию LOCALAPPDATA/Highloader)")
	noBrowser := flag.Bool("no-browser", false, "Не открывать браузер автоматически")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	assets, err := web.Handler()
	if err != nil {
		return err
	}
	js, css, err := web.ReportAssets()
	if err != nil {
		return err
	}
	runtime, err := bootstrap.Start(ctx, bootstrap.Options{DataDir: *data, DisableBrowser: *noBrowser,
		BuildHandler: func(start context.Context, dir string, logger *log.Logger) (http.Handler, func(context.Context) error, error) {
			application, e := app.OpenNative(start, dir, app.Options{Logger: logger, Version: bootstrap.Version, RequestExit: cancel, ReportJS: js, ReportCSS: css})
			if e != nil {
				return nil, nil, e
			}
			return api.Handler(application, assets), application.Close, nil
		},
	})
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- runtime.Wait() }()
	select {
	case <-ctx.Done():
	case err = <-done:
	}
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return errors.Join(err, runtime.Close(shutdown))
}
