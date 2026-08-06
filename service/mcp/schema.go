package mcp

import (
	"encoding/json"
	"strings"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const maxSchemaAnnotateDepth = 3

// annotateSchema writes each field's .proto leading comment into that field's
// "description" in the JSON Schema the MCP plugin generated for md.
func annotateSchema(raw json.RawMessage, md protoreflect.MessageDescriptor) json.RawMessage {
	if md == nil || len(raw) == 0 {
		return raw
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}

	annotateMessage(schema, md, map[protoreflect.FullName]int{})

	out, err := json.Marshal(schema)
	if err != nil {
		return raw
	}
	return json.RawMessage(out)
}

func annotateMessage(schema map[string]any, md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]int) {
	if schema == nil || md == nil {
		return
	}
	if seen[md.FullName()] >= maxSchemaAnnotateDepth {
		return
	}
	seen[md.FullName()]++
	defer func() { seen[md.FullName()]-- }()

	annotateProps(schema, md, seen)
}

func annotateProps(schema map[string]any, md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]int) {
	for _, key := range [...]string{"anyOf", "oneOf", "allOf"} {
		arr, ok := schema[key].([]any)
		if !ok {
			continue
		}
		for _, item := range arr {
			if branch, ok := item.(map[string]any); ok {
				annotateProps(branch, md, seen)
			}
		}
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}

	for i := 0; i < md.Fields().Len(); i++ {
		fd := md.Fields().Get(i)
		propRaw, ok := props[string(fd.Name())]
		if !ok {
			continue
		}
		prop, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}

		if comment := fieldComment(fd); comment != "" {
			if existing, ok := prop["description"].(string); ok && existing != "" {
				prop["description"] = comment + "\n\n" + existing
			} else {
				prop["description"] = comment
			}
		}

		annotateFieldSchema(prop, fd, seen)
	}
}

func annotateFieldSchema(node map[string]any, fd protoreflect.FieldDescriptor, seen map[protoreflect.FullName]int) {
	if node == nil || fd == nil {
		return
	}

	if items, ok := node["items"].(map[string]any); ok {
		annotateFieldSchema(items, fd, seen)
		return
	}

	if fd.IsMap() {
		ap, ok := node["additionalProperties"].(map[string]any)
		if !ok {
			return
		}
		mv := fd.MapValue()
		if mv != nil && mv.Kind() == protoreflect.MessageKind && mv.Message() != nil {
			annotateMessage(ap, mv.Message(), seen)
		}
		return
	}

	if fd.Kind() == protoreflect.MessageKind && fd.Message() != nil {
		annotateMessage(node, fd.Message(), seen)
	}
}

func fieldComment(fd protoreflect.FieldDescriptor) string {
	if fd == nil {
		return ""
	}
	file := fd.ParentFile()
	if file == nil {
		return ""
	}
	loc := file.SourceLocations().ByDescriptor(fd)
	return strings.TrimSpace(gen.CleanComment(loc.LeadingComments))
}
