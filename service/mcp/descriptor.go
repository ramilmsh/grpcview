package mcp

import (
	_ "embed"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Not protoregistry.GlobalFiles: protoc-gen-go strips SourceCodeInfo, so it carries no
// comments, and comments are the point here.
//
//go:embed descriptor_set.pb
var descriptorSet []byte

const workspaceServiceName = "grpcview.v1.WorkspaceService"

func loadWorkspaceService() (protoreflect.ServiceDescriptor, error) {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descriptorSet, &set); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal embedded descriptor set: %w", err)
	}

	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return nil, fmt.Errorf("mcp: build descriptors from the embedded set: %w", err)
	}

	desc, err := files.FindDescriptorByName(workspaceServiceName)
	if err != nil {
		return nil, fmt.Errorf("mcp: find %s in the embedded descriptor set: %w", workspaceServiceName, err)
	}
	sd, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("mcp: %s is a %T, not a service", workspaceServiceName, desc)
	}
	return sd, nil
}

func comment(md protoreflect.MethodDescriptor) string {
	loc := md.ParentFile().SourceLocations().ByDescriptor(md)
	return loc.LeadingComments
}
