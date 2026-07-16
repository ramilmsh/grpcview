package inspector

import (
	"fmt"

	"github.com/invopop/jsonschema"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func messageId(descriptor protoreflect.MessageDescriptor) string {
	return string(descriptor.FullName())
}

func messageRef(descriptor protoreflect.MessageDescriptor) string {
	return "#/$defs/" + messageId(descriptor)
}

func convertField(descriptor protoreflect.FieldDescriptor, defs jsonschema.Definitions) (*jsonschema.Schema, error) {
	switch {
	case descriptor.IsList():
		itemSchema, err := convertFieldType(descriptor, defs)
		if err != nil {
			return nil, err
		}
		return &jsonschema.Schema{
			Type:  "array",
			Items: itemSchema,
		}, nil
	case descriptor.IsMap():
		valueSchema, err := convertFieldType(descriptor.MapValue(), defs)
		if err != nil {
			return nil, err
		}
		return &jsonschema.Schema{
			Type:                 "object",
			AdditionalProperties: valueSchema,
		}, nil
	default:
		return convertFieldType(descriptor, defs)
	}
}

func convertFieldType(descriptor protoreflect.FieldDescriptor, defs jsonschema.Definitions) (*jsonschema.Schema, error) {
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
		id := messageId(descriptor.Message())
		if _, ok := defs[id]; !ok {
			defs[id] = nil
			schema, err := convertMessage(descriptor.Message(), defs)
			if err != nil {
				return nil, err
			}
			defs[id] = schema
		}
		return &jsonschema.Schema{Ref: messageRef(descriptor.Message())}, nil
	default:
		return nil, fmt.Errorf("unknown field kind (%s)", kind)
	}
}

func convertMessage(descriptor protoreflect.MessageDescriptor, defs jsonschema.Definitions) (*jsonschema.Schema, error) {
	schema := &jsonschema.Schema{
		ID:                   jsonschema.ID(messageId(descriptor)),
		Type:                 "object",
		Properties:           jsonschema.NewProperties(),
		AdditionalProperties: jsonschema.FalseSchema,
	}

	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		fieldSchema, err := convertField(fd, defs)
		if err != nil {
			return nil, err
		}
		schema.Properties.Set(fd.JSONName(), fieldSchema)
	}

	return schema, nil
}

func ConvertMessage(descriptor protoreflect.MessageDescriptor) (*jsonschema.Schema, error) {
	defs := make(jsonschema.Definitions)
	schema, err := convertMessage(descriptor, defs)
	if err != nil {
		return nil, err
	}

	schema.Definitions = defs

	return schema, nil
}
