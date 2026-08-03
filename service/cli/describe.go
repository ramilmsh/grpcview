package cli

// describe.go — `grpcview describe <service>/<method>`, the verb that makes the
// ad-hoc invoke form usable. `invoke <svc>/<method> -f body.json` requires knowing
// the field names already, and there is no other way to learn them without opening
// the UI.
//
// It answers from definitions the workspace has already resolved and dials
// nothing, so it works from a box with no route to the target — which is the
// caller who needs it most.

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// describe's -o shapes. outputProto is the human view; outputJSON (invoke's
// constant, reused so one spelling cannot drift from the other) is the protojson
// of the FileDescriptorSet — the descriptors themselves, not a shape this verb
// invents, so it round-trips into any protobuf library.
const outputProto = "proto"

func newDescribeCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "describe <service>/<method>",
		Short: "Print a method's input and output shape",
		Long: "Print the input and output messages of one method, plus every type they\n" +
			"reference, so a body can be written without opening the UI.\n\n" +
			"The shape comes from definitions the workspace has already resolved, so this\n" +
			"answers even when the target is unreachable. Which source it was read from is\n" +
			"reported on stderr, because doc comments survive only if that source carried\n" +
			"them: a server answering by reflection strips them, an uploaded descriptor set\n" +
			"built with them keeps them — so an empty-comment result is a property of the\n" +
			"source, not a bug.\n\n" +
			"-o proto renders .proto text to read. -o json emits the protojson of a\n" +
			"FileDescriptorSet covering the same types, for a program to parse.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDescribe(cmd.Context(), s, g, open, output, args[0])
		},
	}

	// -o is registered here, per verb, with describe's own two values — never as a
	// persistent flag (D8).
	cmd.Flags().StringVarP(&output, "output", "o", outputProto, "output shape: proto|json")

	return cmd
}

func runDescribe(ctx context.Context, s Streams, g *globalFlags, open clientFactory, output, arg string) error {
	// The -o check runs before the RPC: a typo'd flag must not read a workspace.
	switch output {
	case outputProto, outputJSON:
	default:
		return fmt.Errorf("invalid --output %q: want one of %s, %s", output, outputProto, outputJSON)
	}

	// Split on the LAST slash, exactly as invoke's ad-hoc form does: a service's
	// full name carries dots, never slashes.
	service, method := splitMethodPath(arg)
	if service == "" || method == "" {
		return fmt.Errorf(
			"invalid method %q: describe takes <service>/<method>, e.g. echo.v1.EchoService/Unary", arg)
	}

	var described *grpcviewv1.DescribeMethodResponse
	err := withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		resp, err := sess.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
			WorkspaceName: g.Workspace,
			Service:       service,
			Method:        method,
		}))
		if err != nil {
			return fmt.Errorf("failed to describe %s: %w", arg, err)
		}
		described = resp.Msg
		return nil
	})
	if err != nil {
		return err
	}

	// The source is a diagnostic, so it goes to stderr even though it is the thing
	// that explains the presence or absence of comments in the data on stdout.
	if id := described.GetSourceId(); id != "" {
		fmt.Fprintf(s.Err, "%s: from %s\n", arg, id)
	}

	if output == outputJSON {
		return describeJSON(s, arg, described.GetDescriptorSet())
	}
	return writeLine(s.Out, []byte(described.GetProtoText()))
}

// describeJSON renders the descriptor set as protojson. The bytes on the wire are
// a serialized FileDescriptorSet, so they are unmarshalled and re-marshalled
// rather than base64'd through: `describe -o json | jq .file[].messageType` has to
// reach the fields, which the enclosing response's own protojson (where the set is
// a bytes field) would hide.
func describeJSON(s Streams, arg string, raw []byte) error {
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, set); err != nil {
		return fmt.Errorf("failed to parse the descriptor set for %s: %w", arg, err)
	}
	// Indented, unlike the one-line -o json of invoke and get: a descriptor set is
	// something a human reads through as often as a script parses it, and jq does
	// not care either way. json.Indent over protojson's own Multiline, because
	// protojson randomizes its indentation between runs.
	line, err := protojson.Marshal(set)
	if err != nil {
		return fmt.Errorf("failed to render the descriptor set for %s: %w", arg, err)
	}
	return writeLine(s.Out, indentJSON(line))
}
