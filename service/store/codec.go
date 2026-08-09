package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

const schemaVersion = 1

var marshalOpts = protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitDefaultValues: true}

var unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}

func marshalMessage(m proto.Message) ([]byte, error) {
	data, err := marshalOpts.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeMessage(path string, m proto.Message) error {
	data, err := marshalMessage(m)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writeFileAtomic(path, data, 0o644)
}

func readMessage(path string, m proto.Message) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := unmarshalOpts.Unmarshal(data, m); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return nil
}

func requestBodyPath(itemDir string) string { return filepath.Join(itemDir, BodyFileName) }

func requestMetadataPath(itemDir string) string { return filepath.Join(itemDir, MetadataFileName) }

// An absent file is legal: the request-body contract reads it as EmptyBody.
func readSourceFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Bytes are preserved verbatim; the only normalization is ensuring exactly one trailing
// newline on non-empty content, so a hand-authored body.ts/metadata.ts never gets a
// spurious git diff from re-wrapping or reformatting. An empty src stays a zero-byte file.
func writeSourceFile(path, src string) error {
	data := []byte(src)
	if len(data) > 0 {
		data = append(bytes.TrimRight(data, "\n"), '\n')
	}
	return writeFileAtomic(path, data, 0o644)
}

// The loud hard break for the request.json -> body.ts/metadata.ts migration: unmarshalOpts
// discards unknown fields, so a stale request.json with draftBody/draftMetadataScript would
// otherwise load "successfully" with an empty body and the next write would silently drop
// it. Checked as top-level JSON keys, not a substring scan: a request body legitimately
// contains the string "draftBody" inside its own payload.
func readRequestMessage(path string, rf *grpcviewstorev1.Request) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	for _, stale := range []string{"draftBody", "draft_body", "draftMetadataScript", "draft_metadata_script"} {
		if _, ok := top[stale]; ok {
			return fmt.Errorf(
				"%s: stale key %q — request bodies and metadata scripts now live in sibling %s / %s files; "+
					"move the value into the matching file in this directory and delete this key",
				path, stale, BodyFileName, MetadataFileName,
			)
		}
	}
	if err := unmarshalOpts.Unmarshal(data, rf); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
