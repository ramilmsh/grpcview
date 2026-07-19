package store

import (
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// schemaVersion is stamped into grpcview.json so future layout changes are
// detectable. It versions the on-disk schema (grpcview.store.v1), independently
// of the wire API. Bump when the on-disk shape changes incompatibly.
const schemaVersion = 1

// marshalOpts renders managed files as indented protojson: deterministic field
// order and diff-friendly. Empty/default fields are omitted so a file costs
// bytes only for what it actually sets.
var marshalOpts = protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitDefaultValues: true}

// unmarshalOpts tolerates unknown fields so a file written by a newer grpcview
// (with fields this build doesn't know) still loads instead of hard-failing.
var unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}

// writeMessage atomically writes m as protojson to path.
func writeMessage(path string, m proto.Message) error {
	data, err := marshalOpts.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

// readMessage reads and decodes a protojson file into m. The read error is
// returned unwrapped so callers can test it with errors.Is(err, os.ErrNotExist).
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

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so a concurrent reader never observes a torn write.
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
