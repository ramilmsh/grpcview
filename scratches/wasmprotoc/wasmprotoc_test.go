package wasmprotoc

import (
	"testing"

	_ "embed"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/ramilmsh/grpcview/service"
)

var defaultGoPackage string = "example.com/pkg"

//go:embed unittest_proto3_descriptor_set.pb
var descriptorRaw []byte

func TestLoad(t *testing.T) {
	descriptorSet := &descriptorpb.FileDescriptorSet{}
	err := proto.Unmarshal(descriptorRaw, descriptorSet)
	require.NoError(t, err)

	filesToGenerate := make([]string, len(descriptorSet.File))
	for i, file := range descriptorSet.File {
		filesToGenerate[i] = file.GetName()
		if file.Options.GoPackage == nil {
			file.Options.GoPackage = &defaultGoPackage
		}
	}

	data, err := proto.Marshal(&pluginpb.CodeGeneratorRequest{
		FileToGenerate: filesToGenerate,
		ProtoFile:      descriptorSet.File,
	})
	require.NoError(t, err)
	require.NoError(t, service.Run(data))
}
