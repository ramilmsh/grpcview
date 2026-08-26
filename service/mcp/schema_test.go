package mcp

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

// normalize decodes JSON so key order never matters in comparisons.
func normalize(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("normalize: invalid JSON %s: %v", raw, err)
	}
	return v
}

func mustJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("mustJSON: %v", err)
	}
	return json.RawMessage(s)
}

func assertJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	g := normalize(t, got)
	w := normalize(t, want)
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("mismatch:\n got:  %s\n want: %s", got, want)
	}
}

func i32(v int32) *int32 { return &v }

func strp(v string) *string { return &v }

// buildTestFile constructs a synthetic .proto file with comments attached
// via SourceCodeInfo, since generated Go descriptors strip source_code_info.
//
// Messages (in declaration order):
//
//	Inner       { string value = 1; }              // value: "Inner value comment."
//	Outer       { string body = 1; string plain = 2; Inner inner = 3; }
//	                                                 // body: "Body comment."
//	WithOneof   { oneof target { string name = 1; string id2 = 2; } }
//	                                                 // name: "Target name."
//	Repeated    { repeated Inner items = 1; }
//	WithMap     { map<string, Inner> entries = 1; }
func buildTestFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()

	msgType := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	strType := descriptorpb.FieldDescriptorProto_TYPE_STRING
	optLabel := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	repLabel := descriptorpb.FieldDescriptorProto_LABEL_REPEATED

	inner := &descriptorpb.DescriptorProto{
		Name: strp("Inner"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: strp("value"), Number: i32(1), Label: &optLabel, Type: &strType},
		},
	}

	outer := &descriptorpb.DescriptorProto{
		Name: strp("Outer"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: strp("body"), Number: i32(1), Label: &optLabel, Type: &strType},
			{Name: strp("plain"), Number: i32(2), Label: &optLabel, Type: &strType},
			{Name: strp("inner"), Number: i32(3), Label: &optLabel, Type: &msgType, TypeName: strp(".mcpschematest.Inner")},
		},
	}

	oneofName := "target"
	oneofIdx := int32(0)
	withOneof := &descriptorpb.DescriptorProto{
		Name: strp("WithOneof"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: strp("name"), Number: i32(1), Label: &optLabel, Type: &strType, OneofIndex: &oneofIdx},
			{Name: strp("id2"), Number: i32(2), Label: &optLabel, Type: &strType, OneofIndex: &oneofIdx},
		},
		OneofDecl: []*descriptorpb.OneofDescriptorProto{
			{Name: &oneofName},
		},
	}

	repeated := &descriptorpb.DescriptorProto{
		Name: strp("Repeated"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: strp("items"), Number: i32(1), Label: &repLabel, Type: &msgType, TypeName: strp(".mcpschematest.Inner")},
		},
	}

	withMap := &descriptorpb.DescriptorProto{
		Name: strp("WithMap"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name: strp("entries"), Number: i32(1), Label: &repLabel, Type: &msgType,
				TypeName: strp(".mcpschematest.WithMap.EntriesEntry"),
			},
		},
		NestedType: []*descriptorpb.DescriptorProto{
			{
				Name: strp("EntriesEntry"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: strp("key"), Number: i32(1), Label: &optLabel, Type: &strType},
					{
						Name: strp("value"), Number: i32(2), Label: &optLabel, Type: &msgType,
						TypeName: strp(".mcpschematest.Inner"),
					},
				},
				Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
			},
		},
	}

	syntax := "proto3"
	fdp := &descriptorpb.FileDescriptorProto{
		Name:        strp("mcpschematest.proto"),
		Package:     strp("mcpschematest"),
		Syntax:      &syntax,
		MessageType: []*descriptorpb.DescriptorProto{inner, outer, withOneof, repeated, withMap},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{Path: []int32{4, 0, 2, 0}, Span: []int32{0, 0, 1}, LeadingComments: strp(" Inner value comment.\n")},
				{Path: []int32{4, 1, 2, 0}, Span: []int32{0, 0, 1}, LeadingComments: strp(" Body comment.\n")},
				{Path: []int32{4, 2, 2, 0}, Span: []int32{0, 0, 1}, LeadingComments: strp(" Target name.\n")},
			},
		},
	}

	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	return fd
}

func msgByName(t *testing.T, fd protoreflect.FileDescriptor, name string) protoreflect.MessageDescriptor {
	t.Helper()
	md := fd.Messages().ByName(protoreflect.Name(name))
	if md == nil {
		t.Fatalf("message %q not found in test file", name)
	}
	return md
}

func TestAnnotateSchema_PlainObject(t *testing.T) {
	fd := buildTestFile(t)
	outer := msgByName(t, fd, "Outer")

	raw := mustJSON(t, `{
		"type": "object",
		"properties": {
			"body":  {"type": "string"},
			"plain": {"type": "string"}
		}
	}`)

	got := annotateSchema(raw, outer)

	want := mustJSON(t, `{
		"type": "object",
		"properties": {
			"body":  {"type": "string", "description": "Body comment."},
			"plain": {"type": "string"}
		}
	}`)

	assertJSONEqual(t, got, want)
}

func TestAnnotateSchema_OneofAnyOf(t *testing.T) {
	fd := buildTestFile(t)
	withOneof := msgByName(t, fd, "WithOneof")

	raw := mustJSON(t, `{
		"type": "object",
		"properties": {},
		"required": [],
		"anyOf": [
			{
				"$comment": "oneof wrapper",
				"oneOf": [
					{"properties": {"name": {"type": "string"}}, "required": ["name"]},
					{"properties": {"id2":  {"type": "string"}}, "required": ["id2"]}
				]
			}
		]
	}`)

	got := annotateSchema(raw, withOneof)

	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := doc["anyOf"]; ok {
		t.Fatalf("anyOf survived: %#v", doc["anyOf"])
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", doc["properties"])
	}

	nameProp, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatalf("name was not hoisted: %#v", props)
	}
	nameDesc, _ := nameProp["description"].(string)
	if !strings.HasPrefix(nameDesc, "Target name.") {
		t.Fatalf("name description = %q, want it to open with the .proto comment", nameDesc)
	}
	if !strings.Contains(nameDesc, "'target' oneof group") {
		t.Fatalf("name description = %q, want the oneof note", nameDesc)
	}

	id2Prop, ok := props["id2"].(map[string]any)
	if !ok {
		t.Fatalf("id2 was not hoisted: %#v", props)
	}
	id2Desc, _ := id2Prop["description"].(string)
	if !strings.HasPrefix(id2Desc, "Note: This field is part of the 'target' oneof group.") {
		t.Fatalf("id2 description = %q, want the oneof note alone", id2Desc)
	}

	req, ok := doc["required"].([]any)
	if !ok || len(req) != 0 {
		t.Fatalf("required = %#v, want the hoisted members to stay optional", doc["required"])
	}
}

// A oneof member the model cannot see is a tool it cannot call: add_source could not add a
// reflection or a bazel source at all while the branches lived under anyOf.
func TestAnnotateSchema_AddDescriptorSourceIsFlat(t *testing.T) {
	sd, err := loadWorkspaceService()
	if err != nil {
		t.Fatalf("loadWorkspaceService: %v", err)
	}
	md := sd.Methods().ByName("AddDescriptorSource").Input()

	raw, err := json.Marshal(gen.MessageSchema(md, gen.SchemaOptions{}))
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(annotateSchema(raw, md), &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := doc["anyOf"]; ok {
		t.Fatalf("anyOf survived: %#v", doc["anyOf"])
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", doc["properties"])
	}
	for _, name := range []string{"collection", "descriptor_set", "reflection", "bazel", "file_name", "path", "commit_descriptors"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("properties has no %q: %v", name, keysOf(props))
		}
	}

	refl := props["reflection"].(map[string]any)["properties"].(map[string]any)
	if _, ok := refl["address"]; !ok {
		t.Fatalf("reflection kept no nested fields: %v", keysOf(refl))
	}
	bazel := props["bazel"].(map[string]any)["properties"].(map[string]any)
	if _, ok := bazel["label"]; !ok {
		t.Fatalf("bazel kept no nested fields: %v", keysOf(bazel))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestAnnotateSchema_NestedMessage(t *testing.T) {
	fd := buildTestFile(t)
	outer := msgByName(t, fd, "Outer")

	raw := mustJSON(t, `{
		"type": "object",
		"properties": {
			"inner": {
				"type": "object",
				"properties": {
					"value": {"type": "string"}
				}
			}
		}
	}`)

	got := annotateSchema(raw, outer)

	want := mustJSON(t, `{
		"type": "object",
		"properties": {
			"inner": {
				"type": "object",
				"properties": {
					"value": {"type": "string", "description": "Inner value comment."}
				}
			}
		}
	}`)

	assertJSONEqual(t, got, want)
}

func TestAnnotateSchema_RepeatedFieldItems(t *testing.T) {
	fd := buildTestFile(t)
	repeated := msgByName(t, fd, "Repeated")

	raw := mustJSON(t, `{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"value": {"type": "string"}
					}
				}
			}
		}
	}`)

	got := annotateSchema(raw, repeated)

	want := mustJSON(t, `{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"value": {"type": "string", "description": "Inner value comment."}
					}
				}
			}
		}
	}`)

	assertJSONEqual(t, got, want)
}

func TestAnnotateSchema_MapFieldValue(t *testing.T) {
	fd := buildTestFile(t)
	withMap := msgByName(t, fd, "WithMap")

	raw := mustJSON(t, `{
		"type": "object",
		"properties": {
			"entries": {
				"type": "object",
				"additionalProperties": {
					"type": "object",
					"properties": {
						"value": {"type": "string"}
					}
				}
			}
		}
	}`)

	got := annotateSchema(raw, withMap)

	want := mustJSON(t, `{
		"type": "object",
		"properties": {
			"entries": {
				"type": "object",
				"additionalProperties": {
					"type": "object",
					"properties": {
						"value": {"type": "string", "description": "Inner value comment."}
					}
				}
			}
		}
	}`)

	assertJSONEqual(t, got, want)
}

func TestAnnotateSchema_PreservesCannedDescription(t *testing.T) {
	fd := buildTestFile(t)
	outer := msgByName(t, fd, "Outer")

	raw := mustJSON(t, `{
		"type": "object",
		"properties": {
			"body": {"type": "string", "description": "Canned explanation."}
		}
	}`)

	got := annotateSchema(raw, outer)

	want := mustJSON(t, `{
		"type": "object",
		"properties": {
			"body": {"type": "string", "description": "Body comment.\n\nCanned explanation."}
		}
	}`)

	assertJSONEqual(t, got, want)
}

func TestAnnotateSchema_MalformedInputUnchanged(t *testing.T) {
	fd := buildTestFile(t)
	outer := msgByName(t, fd, "Outer")

	notJSON := json.RawMessage("not json")
	if got := annotateSchema(notJSON, outer); string(got) != string(notJSON) {
		t.Fatalf("malformed JSON: got %q, want unchanged %q", got, notJSON)
	}

	if got := annotateSchema(nil, outer); got != nil {
		t.Fatalf("nil input: got %q, want nil", got)
	}

	valid := mustJSON(t, `{"type":"object","properties":{"body":{"type":"string"}}}`)
	if got := annotateSchema(valid, nil); string(got) != string(valid) {
		t.Fatalf("nil descriptor: got %q, want unchanged %q", got, valid)
	}
}

func TestAnnotateSchema_RealDescriptorNoComments(t *testing.T) {
	req := (&grpcviewv1.GetRequest{}).ProtoReflect().Descriptor()
	raw := mustJSON(t, `{"type":"object","properties":{"collection":{"type":"string"}}}`)

	got := annotateSchema(raw, req)

	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	collection := doc["properties"].(map[string]any)["collection"].(map[string]any)
	if _, ok := collection["description"]; ok {
		t.Fatalf("generated Go descriptors have no source_code_info; expected no description, got %#v", collection["description"])
	}
}

func TestAnnotateSchema_RecursiveItemFolderTerminates(t *testing.T) {
	md := (&grpcviewv1.Folder{}).ProtoReflect().Descriptor()

	raw, err := json.Marshal(gen.MessageSchema(md, gen.SchemaOptions{}))
	if err != nil {
		t.Fatalf("gen.MessageSchema: %v", err)
	}

	got := annotateSchema(raw, md)

	var doc any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("annotateSchema produced invalid JSON: %v", err)
	}
}
