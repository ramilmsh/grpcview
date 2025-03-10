package inspector

import (
	"testing"

	_ "embed"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

var defaultGoPackage string = "a.b"

//go:embed unittest_proto3_descriptor_set.pb
var descriptorRaw []byte

func TestLoad(t *testing.T) {
	descriptorSet := &descriptorpb.FileDescriptorSet{}
	err := proto.Unmarshal(descriptorRaw, descriptorSet)
	require.NoError(t, err)

	definitions, err := Load(descriptorSet)
	require.NoError(t, err)

	schemas := make([]*jsonschema.Schema, 0, len(definitions))
	for _, schema := range definitions {
		schemas = append(schemas, schema)
	}

	_ = &jsonschema.Schema{
		Ref:         "#/$defs/proto3_unittest.TestHasbits",
		Definitions: definitions,
	}
}
