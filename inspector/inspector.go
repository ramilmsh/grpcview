package inspector

import (
	"fmt"

	"github.com/invopop/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func messageId(descriptor protoreflect.MessageDescriptor) string {
	return string(descriptor.FullName())
}

func messageRef(descriptor protoreflect.MessageDescriptor) string {
	return "#/$defs/" + messageId(descriptor)
}

func Load(fileDescriptorSet *descriptorpb.FileDescriptorSet) (jsonschema.Definitions, error) {
	definitions := make(jsonschema.Definitions)
	registry, err := protodesc.NewFiles(fileDescriptorSet)
	if err != nil {
		return nil, err
	}

	var descriptors []protoreflect.MessageDescriptor
	registry.RangeFiles(func(fileDescriptor protoreflect.FileDescriptor) bool {
		messages := fileDescriptor.Messages()
		for i := 0; i < messages.Len(); i++ {
			descriptors = append(descriptors, messages.Get(i))
		}
		return true
	})

	for _, descriptor := range descriptors {
		schemas, err := Convert(registry, descriptor)
		if err != nil {
			return nil, err
		}
		for _, schema := range schemas {
			definitions[string(schema.ID)] = schema
		}
	}

	return definitions, nil
}

func convertField(registry *protoregistry.Files, descriptor protoreflect.FieldDescriptor) (*jsonschema.Schema, error) {
	switch {
	case descriptor.IsList():
		itemSchema, err := convertFieldType(registry, descriptor)
		if err != nil {
			return nil, err
		}
		return &jsonschema.Schema{
			Type:  "array",
			Items: itemSchema,
		}, nil
	case descriptor.IsMap():
		valueSchema, err := convertFieldType(registry, descriptor.MapValue())
		if err != nil {
			return nil, err
		}
		return &jsonschema.Schema{
			Type:                 "object",
			AdditionalProperties: valueSchema,
		}, nil
	default:
		return convertFieldType(registry, descriptor)
	}
}

func convertFieldType(registry *protoregistry.Files, descriptor protoreflect.FieldDescriptor) (*jsonschema.Schema, error) {
	switch kind := descriptor.Kind(); kind {
	case protoreflect.BoolKind:
		return &jsonschema.Schema{
			Type: "boolean",
		}, nil
	case protoreflect.Int32Kind,
		protoreflect.Int64Kind,
		protoreflect.Uint32Kind,
		protoreflect.Uint64Kind,
		protoreflect.Sint32Kind,
		protoreflect.Sint64Kind:
		return &jsonschema.Schema{
			Type: "integer",
		}, nil
	case
		protoreflect.Fixed32Kind,
		protoreflect.Sfixed32Kind,
		protoreflect.FloatKind,
		protoreflect.Fixed64Kind,
		protoreflect.Sfixed64Kind,
		protoreflect.DoubleKind:
		return &jsonschema.Schema{
			Type: "number",
		}, nil
	case protoreflect.EnumKind:
		values := descriptor.Enum().Values()
		enumValues := make([]any, values.Len())
		for i := 0; i < values.Len(); i++ {
			enumValues[i] = string(values.Get(i).Name())
		}
		return &jsonschema.Schema{
			Enum: enumValues,
		}, nil
	case protoreflect.StringKind:
		return &jsonschema.Schema{
			Type: "string",
		}, nil
	case protoreflect.BytesKind:
		return &jsonschema.Schema{
			Type:            "string",
			ContentEncoding: "base64",
		}, nil
	case protoreflect.MessageKind:
		return &jsonschema.Schema{Ref: messageRef(descriptor.Message())}, nil
	default:
		return nil, fmt.Errorf("unknown field kind (%s)", kind)
	}
}

func Convert(registry *protoregistry.Files, descriptor protoreflect.MessageDescriptor) ([]*jsonschema.Schema, error) {
	schemas := make([]*jsonschema.Schema, 0)
	messages := descriptor.Messages()
	for i := 0; i < messages.Len(); i++ {
		schema, err := Convert(registry, messages.Get(i))
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, schema...)
	}

	schema := &jsonschema.Schema{
		ID:                   jsonschema.ID(messageId(descriptor)),
		Type:                 "object",
		Properties:           orderedmap.New[string, *jsonschema.Schema](),
		AdditionalProperties: jsonschema.FalseSchema,
	}
	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		fieldSchema, err := convertField(registry, fd)
		if err != nil {
			return nil, err
		}
		schema.Properties.Set(fd.JSONName(), fieldSchema)
	}

	return append(schemas, schema), nil
}
