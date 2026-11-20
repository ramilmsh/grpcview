package workspace

import (
	"fmt"
	"maps"
	"slices"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoprint"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting/codegen"
)

// Source order (no sorting), every comment kept, defaults otherwise. Deterministic: the
// digest covers its input, not its output.
var protoPrinter = protoprint.Printer{Indent: "  "}

// protoFiles is GetDefinitionsResponse.protos: each merged file, google/protobuf/* excepted,
// as its winner's original text if it kept one, else rendered. Sorted by path.
func protoFiles(files map[string]*desc.FileDescriptor, claimedBy, originals map[string]string) ([]*grpcviewv1.ProtoDefinitionFile, error) {
	out := make([]*grpcviewv1.ProtoDefinitionFile, 0, len(files))
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if codegen.WKT(name) {
			continue
		}
		f := &grpcviewv1.ProtoDefinitionFile{Path: name, Source: claimedBy[name]}
		if text, ok := originals[name]; ok {
			f.Content = text
		} else {
			text, err := protoPrinter.PrintProtoToString(files[name])
			if err != nil {
				return nil, fmt.Errorf("render %s: %w", name, err)
			}
			f.Content, f.Rendered = text, true
		}
		out = append(out, f)
	}
	return out, nil
}
