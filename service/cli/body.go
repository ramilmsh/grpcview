package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// readBody reads the request body a verb was handed: `-f <file>` reads that file,
// `-f -` reads stdin, and with no -f stdin is read only when it is piped. It
// returns nil when no body was supplied at all, which the caller must treat as
// "do not override anything" rather than as an empty body.
//
// The bytes come back UNCHANGED — no parsing, wrapping or reformatting. The
// backend's resolveInvokeBody normalizes protojson and TypeScript at one seam, so
// -f behaves identically for body.json and body.ts; re-doing any of that here
// would fork the contract.
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

// blankToNil collapses whitespace-only input to "no body". A pipe that carries
// nothing (a closed stdin in a script, `</dev/null`) must not override a saved
// request's stored body with an empty string.
func blankToNil(raw []byte) []byte {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return raw
}

// isPiped reports whether stdin carries data rather than being a terminal.
//
// A reader that is not an *os.File — a bytes.Buffer or a strings.Reader in a
// test — counts as piped, which is exactly what a table test wants: it feeds
// stdin without a -f flag and expects it to be read.
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

// bodyMessages turns the raw body input into the repeated `messages` a request
// carries, which depends on the method's streaming kind and on nothing else.
//
// For a client-streaming or bidi method the input is NDJSON: one protojson
// message per line, in send order (D13). For EVERY other kind it is one message
// verbatim and must not be split on newlines — a multi-line TypeScript module is
// a single body. That is the sharpest trap in this verb.
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

// checkJSONObject is the one validation the NDJSON path does. A client-streaming
// run sends N messages, so a line that is not an object cannot be handed on as a
// message and the line number is the only useful thing to report.
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
