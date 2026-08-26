package cli

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

const outputProto = "proto"

func newDescribeCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "describe <service>/<method>",
		Short: "Print a method's input and output shape",
		Long: "Print the input and output messages of one method, plus every type they\n" +
			"reference, so a body can be written without opening the UI.\n\n" +
			"The shape comes from definitions the collection has already resolved, so this\n" +
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

	cmd.Flags().StringVarP(&output, "output", "o", outputProto, "output shape: proto|json")

	return cmd
}

func runDescribe(ctx context.Context, s Streams, g *globalFlags, open clientFactory, output, arg string) error {
	switch output {
	case outputProto, outputJSON:
	default:
		return fmt.Errorf("invalid --output %q: want one of %s, %s", output, outputProto, outputJSON)
	}

	service, method := splitMethodPath(arg)
	if service == "" || method == "" {
		return fmt.Errorf(
			"invalid method %q: describe takes <service>/<method>, e.g. grpcview.echo.v1.EchoService/Unary", arg)
	}

	var described *grpcviewv1.DescribeMethodResponse
	err := withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		resp, err := sess.DescribeMethod(ctx, connect.NewRequest(&grpcviewv1.DescribeMethodRequest{
			Collection: collection,
			Service:    service,
			Method:     method,
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

	if id := described.GetSourceId(); id != "" {
		fmt.Fprintf(s.Err, "%s: from %s\n", arg, id)
	}
	if reason := described.GetNotInvocableReason(); reason != "" {
		fmt.Fprintf(s.Err, "%s: %s\n", arg, reason)
	}

	if output == outputJSON {
		return describeJSON(s, arg, described.GetDescriptorSet())
	}
	return writeLine(s.Out, []byte(described.GetProtoText()))
}

func describeJSON(s Streams, arg string, raw []byte) error {
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, set); err != nil {
		return fmt.Errorf("failed to parse the descriptor set for %s: %w", arg, err)
	}
	line, err := protojson.Marshal(set)
	if err != nil {
		return fmt.Errorf("failed to render the descriptor set for %s: %w", arg, err)
	}
	return writeLine(s.Out, indentJSON(line))
}
