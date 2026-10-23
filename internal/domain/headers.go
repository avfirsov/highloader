package domain

import (
	"fmt"
	"strings"
)

func ValidateHeader(name, value string) error {
	if name == "" {
		return fmt.Errorf("имя заголовка пустое")
	}
	for _, c := range []byte(name) {
		if c > 127 || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return fmt.Errorf("неверное имя заголовка %q", name)
		}
	}
	switch strings.ToLower(name) {
	case "content-length", "transfer-encoding", "trailer":
		return fmt.Errorf("заголовок %s управляется транспортом", name)
	}
	for _, b := range []byte(value) {
		if b == 127 || b < 32 && b != '\t' {
			return fmt.Errorf("недопустимый символ в заголовке %s", name)
		}
	}
	return nil
}
