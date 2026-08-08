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

	annotateFields(schema, md, seen)
	hoistOneofs(schema, md)
}

func annotateFields(schema map[string]any, md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]int) {
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

// The plugin emits a non-synthetic oneof as a message-level `anyOf` of `oneOf` groups and keeps
// its members OUT of `properties`. An MCP client that flattens `anyOf` into a single property bag
// keeps only `properties`, so the members vanish before the model sees them and the tool cannot be
// called at all. Hoisting them back — after the branches have been annotated, so their .proto
// comments come along — costs nothing on the argument side: the names are real proto field names
// and protojson still rejects two members of one oneof.
type oneofMember struct {
	name   string
	schema map[string]any
}

func hoistOneofs(schema map[string]any, md protoreflect.MessageDescriptor) {
	groups, ok := schema["anyOf"].([]any)
	if !ok {
		return
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
	}

	var members []oneofMember
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			return
		}
		branches, ok := group["oneOf"].([]any)
		if !ok {
			return
		}
		for _, rawBranch := range branches {
			branch, ok := rawBranch.(map[string]any)
			if !ok {
				return
			}
			branchProps, ok := branch["properties"].(map[string]any)
			if !ok || len(branchProps) != 1 {
				return
			}
			for name, rawProp := range branchProps {
				prop, ok := rawProp.(map[string]any)
				if !ok {
					return
				}
				if _, taken := props[name]; taken {
					return
				}
				members = append(members, oneofMember{name: name, schema: prop})
			}
		}
	}

	for _, m := range members {
		if note := oneofNote(md, m.name); note != "" {
			if existing, ok := m.schema["description"].(string); ok && existing != "" {
				m.schema["description"] = existing + "\n\n" + note
			} else {
				m.schema["description"] = note
			}
		}
		props[m.name] = m.schema
	}
	schema["properties"] = props
	delete(schema, "anyOf")
	schema["required"] = withoutNames(schema["required"], members)
}

func oneofNote(md protoreflect.MessageDescriptor, name string) string {
	fd := md.Fields().ByName(protoreflect.Name(name))
	if fd == nil {
		return ""
	}
	oo := fd.ContainingOneof()
	if oo == nil {
		return ""
	}
	return "Note: This field is part of the '" + string(oo.Name()) + "' oneof group. " +
		"Only one field in this group can be set at a time. " +
		"Setting multiple fields in the group WILL result in an error. Protobuf oneOf semantics apply."
}

// A hoisted member stays optional: the oneof it came from was one branch of an anyOf, and the
// branch's own `required` went away with the branch.
func withoutNames(raw any, members []oneofMember) any {
	list, ok := raw.([]any)
	if !ok {
		return raw
	}
	drop := make(map[string]struct{}, len(members))
	for _, m := range members {
		drop[m.name] = struct{}{}
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			if _, skip := drop[s]; skip {
				continue
			}
		}
		out = append(out, item)
	}
	return out
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
