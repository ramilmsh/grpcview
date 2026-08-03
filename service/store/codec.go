package store

import (
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// schemaVersion is stamped into grpcview.json; bump it on an incompatible on-disk change.
const schemaVersion = 1

var marshalOpts = protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitDefaultValues: true}

// DiscardUnknown so a file written by a newer grpcview still loads.
var unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}

func writeMessage(path string, m proto.Message) error {
	data, err := marshalOpts.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

// readMessage returns the read error unwrapped, so callers can test os.ErrNotExist.
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

// writeFileAtomic writes via a temp file + rename, so a reader never sees a torn write.
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
