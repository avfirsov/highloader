package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var assets embed.FS

//go:embed all:report-dist
var reportAssets embed.FS

func Handler() (http.Handler, error) {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, err
	}
	return http.FileServer(http.FS(root)), nil
}

func ReportAssets() ([]byte, []byte, error) {
	js, err := reportAssets.ReadFile("report-dist/report.js")
	if err != nil {
		return nil, nil, err
	}
	css, err := reportAssets.ReadFile("report-dist/report.css")
	return js, css, err
}
