package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

const (
	outputBody = "body"
	outputJSON = "json"
	outputRaw  = "raw"
)

type invokeFlags struct {
	file         string
	params       []string
	paramsFile   string
	target       string
	tls          bool
	metadata     []string
	metadataFile string
	noHistory    bool
	dryRun       bool
	output       string
}

func newInvokeCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	f := &invokeFlags{}

	cmd := &cobra.Command{
		Use:   "invoke <request-path>|<service>/<method>",
		Short: "Run a saved request, or call a method with a body you supply",
		Long: "Run a saved request by its display-name path (Auth/Login), or call a method\n" +
			"ad hoc by its full name (user.v1.UserService/GetUser) with a body from -f or\n" +
			"stdin.\n\n" +
			"One collection snapshot decides which form the argument is: an argument that\n" +
			"names both a saved request and a method is an error rather than a guess.\n\n" +
			"stdout is data and stderr is everything else. The exit code is 0 when the\n" +
			"call returned status OK, 1 when it returned any other gRPC status, and 2 when\n" +
			"grpcview itself failed and nothing was invoked.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvoke(cmd.Context(), s, g, open, f, args[0])
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&f.file, "file", "f", "", "read the request body from this file; - reads stdin. Without it, stdin is read when piped")
	flags.StringArrayVar(&f.params, "param", nil, "k=v for this run's params, as imported from \"grpcview:request\"; v is parsed as JSON, else taken literally (repeatable, saved requests only)")
	flags.StringVar(&f.paramsFile, "params-file", "", "merge a JSON object of params; an explicit --param wins (saved requests only)")
	flags.StringVar(&f.target, "target", "", "host:port to send this run to, overriding the saved target")
	flags.BoolVar(&f.tls, "tls", false, "dial the --target over TLS")
	flags.StringArrayVar(&f.metadata, "metadata", nil, "k=v outgoing metadata (repeatable, ad-hoc calls only; a saved request's metadata script owns its metadata)")
	flags.StringVar(&f.metadataFile, "metadata-file", "", "read outgoing metadata from a JSON object of string or string[] (ad-hoc calls only)")
	flags.BoolVar(&f.noHistory, "no-history", false, "do not append this run to the saved request's history; a no-op on an ad-hoc call, which has no saved request to record against")
	flags.BoolVar(&f.dryRun, "dry-run", false, "resolve and evaluate the saved request, print it, and send nothing")
	flags.StringVarP(&f.output, "output", "o", outputBody, "output shape: body|json|raw")

	return cmd
}

func runInvoke(ctx context.Context, s Streams, g *globalFlags, open clientFactory, f *invokeFlags, arg string) error {
	switch f.output {
	case outputBody, outputJSON, outputRaw:
	default:
		return fmt.Errorf("invalid --output %q: want one of %s, %s, %s", f.output, outputBody, outputJSON, outputRaw)
	}
	if f.tls && f.target == "" {
		return errors.New("--tls needs --target: it selects TLS for the target this run dials, and no target was given")
	}

	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		ws, err := workspaceSnapshot(ctx, sess, collection)
		if err != nil {
			return err
		}

		target, err := resolveInvokeArg(ws, arg)
		if err != nil {
			return err
		}
		if err := checkFormFlags(target, f); err != nil {
			return err
		}

		raw, err := readBody(s, f.file)
		if err != nil {
			return err
		}
		messages, err := bodyMessages(raw, target.kind)
		if err != nil {
			return err
		}

		if target.saved {
			return invokeSaved(ctx, s, sess, collection, f, target, messages)
		}
		return invokeAdhoc(ctx, s, sess, collection, f, target, messages)
	})
}

func checkFormFlags(target invokeTarget, f *invokeFlags) error {
	if target.saved {
		if len(f.metadata) > 0 || f.metadataFile != "" {
			return fmt.Errorf(
				"--metadata does not apply to the saved request %s: its own metadata script owns its metadata, and a per-run override is deliberately not implemented yet",
				target.arg)
		}
		return nil
	}
	if len(f.params) > 0 || f.paramsFile != "" {
		return fmt.Errorf(
			"--param does not apply to the ad-hoc method %s: params reach a body as `params` from \"grpcview:request\", and an ad-hoc call sends the body you hand it verbatim",
			target.arg)
	}
	if f.dryRun {
		return fmt.Errorf(
			"--dry-run does not apply to the ad-hoc method %s: a dry run reports what a saved request resolves to, and there is nothing saved here",
			target.arg)
	}
	return nil
}

func invokeSaved(ctx context.Context, s Streams, sess session, collection string, f *invokeFlags, target invokeTarget, messages []string) error {
	params, err := buildParams(f.paramsFile, f.params)
	if err != nil {
		return err
	}

	spec := &grpcviewv1.SavedInvokeSpec{
		Collection: collection,
		Path:       target.parent,
		ItemName:   target.itemName,
		Params:     params,
		Target:     buildTarget(f),
		Messages:   messages,
	}
	if f.noHistory {
		spec.RecordHistory = proto.Bool(false)
	}

	if f.dryRun {
		resp, err := sess.InvokeSaved(ctx, connect.NewRequest(&grpcviewv1.InvokeSavedRequest{Spec: spec, DryRun: true}))
		if err != nil {
			return fmt.Errorf("failed to resolve %s: %w", target.arg, err)
		}
		return renderDryRun(s, target.arg, resp.Msg.GetResolved())
	}

	if target.kind.streaming() {
		msg := &grpcviewv1.InvokeSavedStreamRequest{Spec: spec}
		return renderStream(s, f.output, target.arg, func(send func(*grpcviewv1.InvokeStreamingResponse) error) error {
			return sess.InvokeSavedStream(ctx, msg, send)
		})
	}

	resp, err := sess.InvokeSaved(ctx, connect.NewRequest(&grpcviewv1.InvokeSavedRequest{Spec: spec}))
	if err != nil {
		return fmt.Errorf("failed to invoke %s: %w", target.arg, err)
	}
	return renderUnary(s, f.output, target.arg, resp.Msg.GetResponse())
}

func invokeAdhoc(ctx context.Context, s Streams, sess session, collection string, f *invokeFlags, target invokeTarget, messages []string) error {
	md, err := buildMetadata(f.metadataFile, f.metadata)
	if err != nil {
		return err
	}
	spec := &grpcviewv1.InvokeSpec{
		Collection: collection,
		Service:    target.service,
		Method:     target.method,
		Metadata:   md,
		Target:     buildTarget(f),
	}

	if target.kind.streaming() {
		msg := &grpcviewv1.InvokeStreamRequest{Spec: spec, Messages: messages}
		return renderStream(s, f.output, target.arg, func(send func(*grpcviewv1.InvokeStreamingResponse) error) error {
			return sess.InvokeStream(ctx, msg, send)
		})
	}

	var body string
	if len(messages) > 0 {
		body = messages[0]
	}
	resp, err := sess.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{Spec: spec, Body: body}))
	if err != nil {
		return fmt.Errorf("failed to invoke %s: %w", target.arg, err)
	}
	return renderUnary(s, f.output, target.arg, resp.Msg.GetResponse())
}

func buildTarget(f *invokeFlags) *grpcviewv1.Server {
	if f.target == "" {
		return nil
	}
	server := &grpcviewv1.Server{Address: f.target}
	if f.tls {
		server.Tls = &grpcviewv1.Server_TLS{}
	}
	return server
}

func buildParams(file string, kvs []string) (*structpb.Struct, error) {
	values := map[string]any{}

	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read --params-file: %w", err)
		}
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("failed to parse --params-file %s as a JSON object: %w", file, err)
		}
	}

	for _, kv := range kvs {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --param %q: want k=v", kv)
		}
		values[key] = paramValue(value)
	}

	if len(values) == 0 {
		return nil, nil
	}
	params, err := structpb.NewStruct(values)
	if err != nil {
		return nil, fmt.Errorf("failed to build the params object: %w", err)
	}
	return params, nil
}

func paramValue(value string) any {
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return value
	}
	return parsed
}

func buildMetadata(file string, kvs []string) (*structpb.Struct, error) {
	values := map[string][]string{}

	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read --metadata-file: %w", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse --metadata-file %s as a JSON object: %w", file, err)
		}
		for key, value := range parsed {
			switch v := value.(type) {
			case string:
				values[key] = append(values[key], v)
			case []any:
				for _, item := range v {
					str, ok := item.(string)
					if !ok {
						return nil, fmt.Errorf("invalid --metadata-file %s: key %q wants a string or an array of strings", file, key)
					}
					values[key] = append(values[key], str)
				}
			default:
				return nil, fmt.Errorf("invalid --metadata-file %s: key %q wants a string or an array of strings", file, key)
			}
		}
	}

	for _, kv := range kvs {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --metadata %q: want k=v", kv)
		}
		values[key] = append(values[key], value)
	}

	if len(values) == 0 {
		return nil, nil
	}
	fields := make(map[string]any, len(values))
	for key, list := range values {
		items := make([]any, 0, len(list))
		for _, item := range list {
			items = append(items, item)
		}
		fields[key] = items
	}
	md, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("failed to build the metadata object: %w", err)
	}
	return md, nil
}

func renderUnary(s Streams, output, label string, response *grpcviewv1.Request_Response) error {
	if output == outputJSON {
		line, err := marshalOneLine(response)
		if err != nil {
			return fmt.Errorf("failed to render the response: %w", err)
		}
		if err := writeLine(s.Out, line); err != nil {
			return err
		}
		return statusFailure(label, response)
	}

	if err := statusFailure(label, response); err != nil {
		return err
	}

	if output == outputRaw {
		_, err := s.Out.Write(response.GetResponse())
		return err
	}
	return writeLine(s.Out, compactJSON(response.GetResponse()))
}

func renderStream(s Streams, output, label string, call func(send func(*grpcviewv1.InvokeStreamingResponse) error) error) error {
	var terminal *grpcviewv1.Request_Response

	err := call(func(frame *grpcviewv1.InvokeStreamingResponse) error {
		switch event := frame.GetEvent().(type) {
		case *grpcviewv1.InvokeStreamingResponse_Result:
			terminal = event.Result
			return nil
		case *grpcviewv1.InvokeStreamingResponse_Message:
			if output == outputRaw {
				if _, err := s.Out.Write(event.Message); err != nil {
					return err
				}
				return writeLine(s.Out, nil)
			}
			return writeLine(s.Out, compactJSON(event.Message))
		default:
			return nil
		}
	})
	if err != nil {
		return fmt.Errorf("failed to invoke %s: %w", label, err)
	}
	if terminal == nil {
		return fmt.Errorf("failed to invoke %s: the stream ended without a terminal frame", label)
	}

	if output == outputJSON {
		line, err := marshalOneLine(terminal)
		if err != nil {
			return fmt.Errorf("failed to render the terminal frame: %w", err)
		}
		if err := writeLine(s.Out, line); err != nil {
			return err
		}
	}
	if err := statusFailure(label, terminal); err != nil {
		return err
	}
	if output != outputJSON {
		fmt.Fprintf(s.Err, "%s: OK   (%s)\n", label, formatLatency(terminal))
	}
	return nil
}

func statusFailure(label string, response *grpcviewv1.Request_Response) error {
	code := response.GetStatus().GetCode()
	if code == 0 {
		return nil
	}
	return statusError{code: 1, err: fmt.Errorf(
		"%s: %s: %s   (%s)",
		label, statusCodeName(code), response.GetStatus().GetMessage(), formatLatency(response))}
}

func renderDryRun(s Streams, label string, resolved *grpcviewv1.ResolvedRequest) error {
	if resolved == nil {
		return fmt.Errorf("failed to resolve %s: the dry run returned no resolved request", label)
	}
	compact, err := marshalOneLine(resolved)
	if err != nil {
		return fmt.Errorf("failed to render the resolved request: %w", err)
	}
	return writeLine(s.Out, indentJSON(compact))
}

func indentJSON(raw []byte) []byte {
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return raw
	}
	return indented.Bytes()
}

func statusCodeName(code int32) string {
	if code == 0 {
		return "OK"
	}
	return strings.ToUpper(connect.Code(code).String())
}

func formatLatency(response *grpcviewv1.Request_Response) string {
	d := response.GetLatency().AsDuration()
	if d == 0 {
		return "0s"
	}
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

func marshalOneLine(m proto.Message) ([]byte, error) {
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func compactJSON(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []byte("null")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return trimmed
	}
	return compact.Bytes()
}

func writeLine(w io.Writer, line []byte) error {
	if _, err := w.Write(line); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}
