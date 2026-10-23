package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

type Config struct {
	SchemaVersion int  `json:"schemaVersion"`
	OpenBrowser   bool `json:"openBrowser"`
}

func LoadConfig(dir string) (Config, error) {
	result := Config{SchemaVersion: 1, OpenBrowser: true}
	f, err := os.Open(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return result, SaveConfig(dir, result)
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&result); err != nil {
		return result, fmt.Errorf("config.json: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return result, fmt.Errorf("config.json: лишние данные")
	}
	if result.SchemaVersion != 1 {
		return result, fmt.Errorf("неподдерживаемая версия config.json: %d", result.SchemaVersion)
	}
	return result, nil
}

func SaveConfig(dir string, c Config) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("неподдерживаемая версия конфигурации")
	}
	return AtomicJSON(filepath.Join(dir, "config.json"), c)
}

func AtomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".highloader-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func OpenLog(dir string) (*log.Logger, *os.File, error) {
	folder := filepath.Join(dir, "logs")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(folder, "highloader.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 4<<20 {
		if err = os.Rename(path, path+".1"); err != nil {
			return nil, nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, err
	}
	return log.New(f, "", log.Ldate|log.Ltime|log.LUTC), f, nil
}
