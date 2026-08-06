package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func readBody(s Streams, file string) ([]byte, error) {
	switch {
	case file == "-":
		raw, err := io.ReadAll(s.In)
		if err != nil {
			return nil, fmt.Errorf("failed to read the body from stdin: %w", err)
		}
		return blankToNil(raw), nil

	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read the body file: %w", err)
		}
		return blankToNil(raw), nil

	case isPiped(s.In):
		raw, err := io.ReadAll(s.In)
		if err != nil {
			return nil, fmt.Errorf("failed to read the body from stdin: %w", err)
		}
		return blankToNil(raw), nil

	default:
		return nil, nil
	}
}

func blankToNil(raw []byte) []byte {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return raw
}

func isPiped(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func bodyMessages(raw []byte, kind methodKind) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if !kind.ndjson() {
		return []string{string(raw)}, nil
	}

	var messages []string
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if err := checkJSONObject(trimmed); err != nil {
			return nil, fmt.Errorf("line %d of the request body is not a JSON object: %w", i+1, err)
		}
		messages = append(messages, trimmed)
	}
	return messages, nil
}

func checkJSONObject(line string) error {
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("got %s", jsonKind(value))
	}
	return nil
}

func jsonKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	default:
		return "an unexpected value"
	}
}
