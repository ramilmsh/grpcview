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

// The three -o shapes (D8). stdout is data in all three; latency, status text
// and warnings are stderr in all three.
const (
	// outputBody prints the response message as one line of JSON.
	outputBody = "body"
	// outputJSON prints the whole Request.Response as one line of protojson, for jq.
	outputJSON = "json"
	// outputRaw prints the response bytes unchanged.
	outputRaw = "raw"
)

// invokeFlags are invoke's own flags. --workspace, --server and --timeout are
// inherited persistent flags read off globalFlags instead (D6).
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
			"One workspace snapshot decides which form the argument is: an argument that\n" +
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
	flags.StringArrayVar(&f.params, "param", nil, "k=v for this run's gv.request.params; v is parsed as JSON, else taken literally (repeatable, saved requests only)")
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

	// --timeout bounds the whole verb, the resolving Get included: a hung
	// snapshot read is as much a failure to invoke as a hung call.
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}

	sess, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer func() { _ = sess.close(ctx) }()

	snapshot, err := sess.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: g.Workspace}))
	if err != nil {
		return fmt.Errorf("failed to read workspace %q: %w", g.Workspace, err)
	}

	target, err := resolveInvokeArg(snapshot.Msg.GetWorkspace(), arg)
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
		return invokeSaved(ctx, s, sess, g, f, target, messages)
	}
	return invokeAdhoc(ctx, s, sess, g, f, target, messages)
}

// checkFormFlags refuses the flag/form combinations that have no wire field to
// land in. Silently dropping a --param would be worse than refusing it.
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
			"--param does not apply to the ad-hoc method %s: params reach a body as gv.request.params, and an ad-hoc call sends the body you hand it verbatim",
			target.arg)
	}
	if f.dryRun {
		return fmt.Errorf(
			"--dry-run does not apply to the ad-hoc method %s: a dry run reports what a saved request resolves to, and there is nothing saved here",
			target.arg)
	}
	return nil
}

func invokeSaved(ctx context.Context, s Streams, sess session, g *globalFlags, f *invokeFlags, target invokeTarget, messages []string) error {
	params, err := buildParams(f.paramsFile, f.params)
	if err != nil {
		return err
	}

	msg := &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: g.Workspace,
		Path:          target.parent,
		ItemName:      target.itemName,
		Params:        params,
		Target:        buildTarget(f),
		Messages:      messages,
		DryRun:        f.dryRun,
	}
	// record_history defaults to true server-side, so only the opt-out is sent.
	if f.noHistory {
		msg.RecordHistory = proto.Bool(false)
	}

	if f.dryRun {
		resp, err := sess.InvokeSaved(ctx, connect.NewRequest(msg))
		if err != nil {
			return fmt.Errorf("failed to resolve %s: %w", target.arg, err)
		}
		return renderDryRun(s, target.arg, resp.Msg.GetResolved())
	}

	if target.kind.streaming() {
		return renderStream(s, f.output, target.arg, func(send func(*grpcviewv1.InvokeStreamResponse) error) error {
			return sess.InvokeSavedStream(ctx, msg, send)
		})
	}

	resp, err := sess.InvokeSaved(ctx, connect.NewRequest(msg))
	if err != nil {
		return fmt.Errorf("failed to invoke %s: %w", target.arg, err)
	}
	return renderUnary(s, f.output, target.arg, resp.Msg.GetResponse())
}

func invokeAdhoc(ctx context.Context, s Streams, sess session, g *globalFlags, f *invokeFlags, target invokeTarget, messages []string) error {
	md, err := buildMetadata(f.metadataFile, f.metadata)
	if err != nil {
		return err
	}
	server := buildTarget(f)

	if target.kind.streaming() {
		msg := &grpcviewv1.InvokeStreamRequest{
			WorkspaceName: g.Workspace,
			Service:       target.service,
			Method:        target.method,
			Messages:      messages,
			Metadata:      md,
			Target:        server,
		}
		return renderStream(s, f.output, target.arg, func(send func(*grpcviewv1.InvokeStreamResponse) error) error {
			return sess.InvokeStream(ctx, msg, send)
		})
	}

	// A non-streaming ad-hoc call takes exactly one body, and bodyMessages did
	// not split it: messages holds it whole or is empty.
	var body string
	if len(messages) > 0 {
		body = messages[0]
	}
	msg := &grpcviewv1.InvokeRequest{
		WorkspaceName: g.Workspace,
		Service:       target.service,
		Method:        target.method,
		Body:          body,
		Metadata:      md,
		Target:        server,
	}
	resp, err := sess.Invoke(ctx, connect.NewRequest(msg))
	if err != nil {
		return fmt.Errorf("failed to invoke %s: %w", target.arg, err)
	}
	return renderUnary(s, f.output, target.arg, resp.Msg.GetResponse())
}

// buildTarget maps --target/--tls onto the Server override. Server.TLS is an
// empty message, so the flag is one bool mapped by hand.
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

// buildParams merges --params-file and --param into this run's params object.
// Each --param value is parsed as JSON and falls back to the literal string, so
// `n=3` is a number and `n=three` is a string. An explicit --param wins over the
// file.
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

// paramValue parses a --param value as JSON, falling back to the literal string.
// A shell has no types, and "3" meaning the number 3 is what a request body
// almost always wants; a value that is not JSON at all is a plain string.
func paramValue(value string) any {
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return value
	}
	return parsed
}

// buildMetadata merges --metadata-file and --metadata into the outgoing metadata
// Struct. Every key is sent as a ListValue of strings: the backend flattens both
// a bare string and a list, and a list is the unambiguous shape for a repeatable
// flag. --metadata appends to whatever the file supplied for the same key.
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

// renderUnary applies D8 to a completed unary call and D9 to its status.
//
// Under -o body and -o raw a non-OK status writes NOTHING to stdout: a script
// piping stdout into jq must not receive a payload for a call that failed. Under
// -o json the whole Request.Response IS the data and the status is part of it, so
// it is written either way — the same rule renderStream applies to its terminal
// frame, so the two forms do not disagree about what -o json means.
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

// renderStream renders a streaming invoke as NDJSON — one message per line on
// stdout — and takes its exit code from the terminal frame's status. call is the
// RPC, already bound to its request message.
func renderStream(s Streams, output, label string, call func(send func(*grpcviewv1.InvokeStreamResponse) error) error) error {
	var terminal *grpcviewv1.Request_Response

	err := call(func(frame *grpcviewv1.InvokeStreamResponse) error {
		switch event := frame.GetEvent().(type) {
		case *grpcviewv1.InvokeStreamResponse_Result:
			terminal = event.Result
			return nil
		case *grpcviewv1.InvokeStreamResponse_Message:
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

	// The terminal frame is a diagnostic, so it belongs on stderr — except under
	// -o json, where the whole Request.Response IS the data and goes out as the
	// last stdout line.
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

// statusFailure turns a non-OK gRPC status into the exit-1 error in D9's format.
// The status arrives INSIDE Request.Response with a nil transport error, which is
// the whole reason the 1-vs-2 split needs its own line of code. The "grpcview: "
// prefix is added by execute.
func statusFailure(label string, response *grpcviewv1.Request_Response) error {
	code := response.GetStatus().GetCode()
	if code == 0 {
		return nil
	}
	return statusError{code: 1, err: fmt.Errorf(
		"%s: %s: %s   (%s)",
		label, statusCodeName(code), response.GetStatus().GetMessage(), formatLatency(response))}
}

// renderDryRun prints the resolved request as indented JSON on stdout, exit 0.
// It is deliberately json.Indent over compact protojson rather than protojson's
// own Multiline: protojson randomizes indentation between runs, and a dry run is
// output a script diffs.
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

// indentJSON pretty-prints JSON for the two outputs a human reads as often as a
// script parses them: a dry run and a described method. It is json.Indent rather
// than protojson's own Multiline because protojson randomizes its indentation
// between runs, and it returns the input unchanged if it does not parse — a
// rendering helper is not the place to reject data that already came back
// successfully.
func indentJSON(raw []byte) []byte {
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return raw
	}
	return indented.Bytes()
}

// statusCodeName is the gRPC code's canonical SCREAMING_SNAKE name. connect's
// Code.String is the same vocabulary in lower snake case, so upper-casing it
// needs no second 17-entry table to drift out of sync — including its
// "code_<n>" fallback for a code no version of this binary knows.
func statusCodeName(code int32) string {
	if code == 0 {
		return "OK"
	}
	return strings.ToUpper(connect.Code(code).String())
}

// formatLatency renders the latency for the one-line error/status text. It rounds
// to whole milliseconds, the unit the plan's error format shows, and falls back
// to microseconds for a sub-millisecond call so a fast local run does not print
// "0s".
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

// marshalOneLine renders a proto message as one line of protojson. protojson
// injects unstable whitespace by design, so the output goes through json.Compact
// to become byte-stable — which a caller piping into jq, and a golden test,
// both need.
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

// compactJSON flattens a response body to one line. A body that does not parse
// as JSON is passed through with its surrounding whitespace trimmed rather than
// rejected: rendering is not the place to re-litigate what the target returned.
func compactJSON(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		// Matches gv.invoke's own rendering, which reports a missing body as the
		// JSON literal null so a consumer needs no "" special case.
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
