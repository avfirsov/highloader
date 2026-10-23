package template

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"highloader/internal/domain"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ImportResult struct {
	Templates   []domain.TemplateSource            `json:"templates"`
	SelectedIDs []string                           `json:"selectedIds"`
	Settings    map[string]domain.TemplateSettings `json:"settings"`
	Diagnostics []domain.Diagnostic                `json:"diagnostics"`
}

func ImportDirectory(dir string) (ImportResult, error) {
	result := ImportResult{Templates: []domain.TemplateSource{}, SelectedIDs: []string{}, Settings: map[string]domain.TemplateSettings{}, Diagnostics: []domain.Diagnostic{}}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return result, fmt.Errorf("не удалось прочитать папку: %w", e)
	}
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".json" && ext != ".jsonc" {
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		id, e := evaluate(expression{name: "sys.uuid"}, Context{Random: rand.Reader})
		if e != nil {
			return result, e
		}
		s := domain.TemplateSource{ID: id.(string), Filename: entry.Name(), ImportOrder: len(result.Templates), Headers: []domain.FileHeader{}, Diagnostics: []domain.Diagnostic{}}
		raw, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		s.Raw = raw
		digest := sha256.Sum256(raw)
		s.Digest = fmt.Sprintf("%x", digest)
		if e == nil {
			var p Parsed
			p, e = Parse(raw)
			if e == nil {
				s.Body = p.Body
				s.Headers = p.Headers
				sort.Slice(s.Headers, func(i, j int) bool { return strings.ToLower(s.Headers[i].Name) < strings.ToLower(s.Headers[j].Name) })
				_, e = Compile(s.Body)
			}
		}
		if e != nil {
			diagnostic := domain.Diagnostic{Field: s.Filename, Message: e.Error()}
			var syntax *json.SyntaxError
			if errors.As(e, &syntax) {
				diagnostic.Offset = syntax.Offset
				diagnostic.Message = fmt.Sprintf("%s (байт %d)", e.Error(), syntax.Offset)
			}
			s.Diagnostics = append(s.Diagnostics, diagnostic)
		} else {
			s.Valid = true
			result.SelectedIDs = append(result.SelectedIDs, s.ID)
		}
		result.Settings[s.ID] = domain.TemplateSettings{Weight: 1, Headers: []domain.HeaderRow{}}
		result.Templates = append(result.Templates, s)
	}
	if len(result.Templates) == 0 {
		result.Diagnostics = append(result.Diagnostics, domain.Diagnostic{Field: "directory", Message: "В папке нет доступных файлов JSON/JSONC"})
	}
	return result, nil
}
