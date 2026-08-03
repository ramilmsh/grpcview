package workspace

// describe.go — "what does this method take?" answered from the workspace's OWN merged
// descriptors (cli-generator-exploration.md C2b). Deliberately never dials: the caller that
// needs this most is a shell (or an agent) on a box with no route to the target, writing a
// body it cannot otherwise guess the field names of.
//
// The output is the descriptors themselves, in two forms. Rendered .proto text is the human
// view; a FileDescriptorSet is the machine view, and it is a standard, already
// schema-documented protobuf message rather than a shape invented here — so there is no
// mapping layer and therefore no mapping bugs.

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoprint"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// describeMethod locates a method in the workspace's merged descriptor set and reports which
// source won the service it belongs to. It is the unexported half of the DescribeMethod RPC
// (the exported name belongs to the handler) and the one place the schema is resolved
// WITHOUT a network round-trip: invoke's resolveMethod reflects the live target because it
// is about to call it, while describe answers from what is already on disk.
//
// The merged descriptor_set is linked with the same call mergeSources uses, so the shape
// reported here is exactly the shape the UI and the invoke path see — not a second,
// independently-linked view that could disagree.
func (w Workspace) describeMethod(ctx context.Context, workspaceName, service, method string) (*desc.MethodDescriptor, string, error) {
	coll, err := w.store.Open(ctx, workspaceName)
	if err != nil {
		return nil, "", err
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, "", toConnectError(err)
	}

	// No descriptors at all means no source has resolved yet — a state the user fixes by
	// adding or refreshing a source, not a lookup that failed.
	if len(ws.GetDescriptorSet()) == 0 {
		return nil, "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"workspace %q has no resolved definitions: add a descriptor source (or refresh one) first", workspaceName))
	}

	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(ws.GetDescriptorSet(), fds); err != nil {
		return nil, "", fmt.Errorf("parse the workspace's descriptor set: %w", err)
	}
	files, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("link the workspace's descriptor set: %w", err))
	}

	var serviceDesc *desc.ServiceDescriptor
	for _, fd := range files {
		if sd := fd.FindService(service); sd != nil {
			serviceDesc = sd
			break
		}
	}
	if serviceDesc == nil {
		return nil, "", connect.NewError(connect.CodeNotFound, fmt.Errorf(
			"service %q is not in workspace %q's definitions", service, workspaceName))
	}
	methodDesc := serviceDesc.FindMethodByName(method)
	if methodDesc == nil {
		// Naming the service is what distinguishes a mistyped method from a mistyped
		// service: both are NotFound, and only the message says which half was wrong.
		return nil, "", connect.NewError(connect.CodeNotFound, fmt.Errorf(
			"method %q is not in service %q", method, service))
	}

	return methodDesc, wonServiceBy(ws.GetSources(), service), nil
}

// wonServiceBy returns the id of the source whose descriptors the workspace uses for a
// service, read off the per-source resolve summary (won_service_names) the merge already
// computed. It is empty when no source claims the service — which cannot happen for a
// service that was just found in the merged set, but is not worth panicking over either.
//
// Reading the recorded winner rather than re-deriving it keeps one definition of "who won":
// the merge's, whose file-level precedence is not something a second implementation here
// would reproduce.
func wonServiceBy(sources []*grpcviewv1.DescriptorSource, service string) string {
	for _, src := range sources {
		for _, name := range src.GetResolved().GetWonServiceNames() {
			if name == service {
				return src.GetId()
			}
		}
	}
	return ""
}

// DescribeMethod reports one method's input and output shape from the workspace's resolved
// definitions, as rendered .proto text and as a self-contained FileDescriptorSet.
func (w Workspace) DescribeMethod(ctx context.Context, request *connect.Request[grpcviewv1.DescribeMethodRequest]) (*connect.Response[grpcviewv1.DescribeMethodResponse], error) {
	msg := request.Msg
	methodDesc, sourceID, err := w.describeMethod(ctx, msg.GetWorkspaceName(), msg.GetService(), msg.GetMethod())
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
		ProtoText:       protoText,
		DescriptorSet:   set,
		SourceId:        sourceID,
		ClientStreaming: methodDesc.IsClientStreaming(),
		ServerStreaming: methodDesc.IsServerStreaming(),
	}), nil
}

// typeClosure collects every message and enum reachable from a method's input and output, to
// a fixpoint: field types (a map field's entry leads to its key and value types the same
// way), nested messages and enums, and extensions declared inside a message. The result is
// in discovery order — input first, then output, then what they pull in — so both views are
// deterministic, and a type that references itself terminates because full names are visited
// once.
//
// Well-known types are NOT filtered out. Any exclusion list would be a policy to defend, and
// a body author who is told a field is a google.protobuf.Timestamp still has to know what
// goes inside it.
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

		// A nested type drags in the type that DECLARES it, because that outer type is
		// what gets rendered whole (renderProtoText) — so its own fields have to be in
		// the closure too, or the text would name types it never shows.
		if top := topLevel(d); top != d {
			queue = append(queue, top)
		}

		md, ok := d.(*desc.MessageDescriptor)
		if !ok {
			continue // an enum has no references of its own
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

// fieldTypes returns the composite types a single field refers to, if any. A map field's
// type is its synthesized entry message, so walking that entry's own two fields is what
// reaches a map's value message — no special case needed here.
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

// renderProtoText is the human view: the rpc declaration, then each type in the closure as
// .proto source, each headed by a comment naming it fully and saying where it came from —
// protoprint emits a message without its package, so without that header a reader of a
// multi-package closure cannot tell which `Status` they are looking at.
//
// Only TOP-LEVEL types are printed. A nested message is already inside the printout of the
// type that declares it, so emitting it again would duplicate it.
func renderProtoText(methodDesc *desc.MethodDescriptor, closure []desc.Descriptor) (string, error) {
	// A fresh Printer per call: printProto writes its defaulted Indent back into the
	// struct, so a shared one would be a data race between concurrent requests.
	//
	// Compact drops the blank line protoprint otherwise puts between every two fields,
	// which triples the height of a wide message for no gain in a shape reference. It also
	// merges DETACHED comments into the following element's leading comment, which would
	// present a section header as if it documented one field — so detached comments are
	// dropped instead, keeping every comment that IS printed attributed to the right thing.
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

// streamingKind names a method's call shape in the words the CLI and the UI already use, so
// the rendered header says what `stream` on the rpc line implies.
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

// topLevel walks a descriptor up to the type declared directly in its file, which is the
// unit protoprint renders whole.
func topLevel(d desc.Descriptor) desc.Descriptor {
	for {
		parent, ok := d.GetParent().(*desc.MessageDescriptor)
		if !ok {
			return d
		}
		d = parent
	}
}

// closureDescriptorSet is the machine view: the FILES defining the method and every type in
// the closure, whole and unmodified, plus their transitive imports (ToFileDescriptorSet adds
// those and topo-sorts).
//
// Whole files rather than files synthesized to hold exactly the closure: pruning a file's
// contents means recomputing its import list, and a set whose imports no longer match its
// references does not link — so the "tighter" output is the one that can be silently broken,
// while a file superset is verbatim descriptor data that round-trips by construction. The
// closure still decides WHICH files appear, so an unrelated service's protos are not dragged
// in.
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
