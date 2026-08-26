package workspace

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoprint"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

func (w Workspace) describeMethod(ctx context.Context, collectionID, service, method string) (*desc.MethodDescriptor, string, error) {
	defs, err := w.definitions(ctx, collectionID)
	if err != nil {
		return nil, "", err
	}
	methodDesc, err := defs.method(collectionID, service, method)
	if err != nil {
		return nil, "", err
	}
	return methodDesc, defs.wonBy(service), nil
}

func (w Workspace) DescribeMethod(ctx context.Context, request *connect.Request[grpcviewv1.DescribeMethodRequest]) (*connect.Response[grpcviewv1.DescribeMethodResponse], error) {
	msg := request.Msg
	methodDesc, sourceID, err := w.describeMethod(ctx, msg.GetCollection(), msg.GetService(), msg.GetMethod())
	if err != nil {
		return nil, err
	}

	closure := typeClosure(methodDesc)
	protoText, err := renderProtoText(methodDesc, closure)
	if err != nil {
		return nil, err
	}
	set, err := proto.Marshal(closureDescriptorSet(methodDesc, closure))
	if err != nil {
		return nil, fmt.Errorf("marshal the described method's descriptor set: %w", err)
	}

	return connect.NewResponse(&grpcviewv1.DescribeMethodResponse{
		ProtoText:          protoText,
		DescriptorSet:      set,
		SourceId:           sourceID,
		ClientStreaming:    methodDesc.IsClientStreaming(),
		ServerStreaming:    methodDesc.IsServerStreaming(),
		NotInvocableReason: notInvocableReason(methodDesc),
	}), nil
}

const streamingNotInvocable = "streaming: run it with the invoke_streaming or invoke_saved_streaming MCP tool, " +
	"the grpcview invoke CLI verb, or the UI; the unary invoke and invoke_saved reject it with Unimplemented"

// Honesty at authoring time. Every surface that hands out a method — describe, ls,
// create_request — says this before the author saves, instead of letting them find out
// at invoke time.
func notInvocableReason(methodDesc *desc.MethodDescriptor) string {
	if methodDesc.IsClientStreaming() || methodDesc.IsServerStreaming() {
		return streamingNotInvocable
	}
	return ""
}

func typeClosure(methodDesc *desc.MethodDescriptor) []desc.Descriptor {
	var (
		out     []desc.Descriptor
		visited = map[string]bool{}
		queue   = []desc.Descriptor{methodDesc.GetInputType(), methodDesc.GetOutputType()}
	)
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if d == nil || visited[d.GetFullyQualifiedName()] {
			continue
		}
		visited[d.GetFullyQualifiedName()] = true
		out = append(out, d)

		if top := topLevel(d); top != d {
			queue = append(queue, top)
		}

		md, ok := d.(*desc.MessageDescriptor)
		if !ok {
			continue
		}
		for _, fd := range md.GetFields() {
			queue = append(queue, fieldTypes(fd)...)
		}
		for _, ext := range md.GetNestedExtensions() {
			queue = append(queue, fieldTypes(ext)...)
		}
		for _, nested := range md.GetNestedMessageTypes() {
			queue = append(queue, nested)
		}
		for _, nested := range md.GetNestedEnumTypes() {
			queue = append(queue, nested)
		}
	}
	return out
}

func fieldTypes(fd *desc.FieldDescriptor) []desc.Descriptor {
	switch {
	case fd.GetMessageType() != nil:
		return []desc.Descriptor{fd.GetMessageType()}
	case fd.GetEnumType() != nil:
		return []desc.Descriptor{fd.GetEnumType()}
	default:
		return nil
	}
}

func renderProtoText(methodDesc *desc.MethodDescriptor, closure []desc.Descriptor) (string, error) {
	printer := protoprint.Printer{Compact: true, OmitDetachedComments: true}

	var b strings.Builder
	fmt.Fprintf(&b, "// %s/%s — %s\n",
		methodDesc.GetService().GetFullyQualifiedName(), methodDesc.GetName(), streamingKind(methodDesc))
	rpc, err := printer.PrintProtoToString(methodDesc)
	if err != nil {
		return "", fmt.Errorf("render method %s: %w", methodDesc.GetFullyQualifiedName(), err)
	}
	b.WriteString(strings.TrimRight(rpc, "\n"))
	b.WriteString("\n")

	roles := map[string]string{
		methodDesc.GetInputType().GetFullyQualifiedName():  "the request message",
		methodDesc.GetOutputType().GetFullyQualifiedName(): "the response message",
	}
	printed := map[string]bool{}
	for _, d := range closure {
		top := topLevel(d)
		if printed[top.GetFullyQualifiedName()] {
			continue
		}
		printed[top.GetFullyQualifiedName()] = true

		text, err := printer.PrintProtoToString(top)
		if err != nil {
			return "", fmt.Errorf("render type %s: %w", top.GetFullyQualifiedName(), err)
		}
		b.WriteString("\n")
		if role := roles[top.GetFullyQualifiedName()]; role != "" {
			fmt.Fprintf(&b, "// %s — %s, from %s\n", top.GetFullyQualifiedName(), role, top.GetFile().GetName())
		} else {
			fmt.Fprintf(&b, "// %s — from %s\n", top.GetFullyQualifiedName(), top.GetFile().GetName())
		}
		b.WriteString(strings.TrimRight(text, "\n"))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func streamingKind(methodDesc *desc.MethodDescriptor) string {
	switch {
	case methodDesc.IsClientStreaming() && methodDesc.IsServerStreaming():
		return "bidirectional streaming"
	case methodDesc.IsClientStreaming():
		return "client streaming"
	case methodDesc.IsServerStreaming():
		return "server streaming"
	default:
		return "unary"
	}
}

func topLevel(d desc.Descriptor) desc.Descriptor {
	for {
		parent, ok := d.GetParent().(*desc.MessageDescriptor)
		if !ok {
			return d
		}
		d = parent
	}
}

func closureDescriptorSet(methodDesc *desc.MethodDescriptor, closure []desc.Descriptor) *descriptorpb.FileDescriptorSet {
	var (
		files []*desc.FileDescriptor
		seen  = map[string]bool{}
	)
	add := func(fd *desc.FileDescriptor) {
		if fd == nil || seen[fd.GetName()] {
			return
		}
		seen[fd.GetName()] = true
		files = append(files, fd)
	}
	add(methodDesc.GetFile())
	for _, d := range closure {
		add(d.GetFile())
	}
	return desc.ToFileDescriptorSet(files...)
}
