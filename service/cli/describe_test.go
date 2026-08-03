package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func (f *fakeClient) DescribeMethod(_ context.Context, r *connect.Request[grpcviewv1.DescribeMethodRequest]) (*connect.Response[grpcviewv1.DescribeMethodResponse], error) {
	f.gotDescribe = append(f.gotDescribe, r.Msg)
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return connect.NewResponse(f.described), nil
}

func describedEcho(t *testing.T) *grpcviewv1.DescribeMethodResponse {
	t.Helper()
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("proto/echo/v1/echo.proto"),
		Package: proto.String("echo.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("EchoRequest"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:   proto.String("message"),
				Number: proto.Int32(1),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			}},
		}},
	}}}
	raw, err := proto.Marshal(set)
	if err != nil {
		t.Fatalf("marshal the fixture descriptor set: %v", err)
	}
	return &grpcviewv1.DescribeMethodResponse{
		ProtoText:     "rpc Unary ( EchoRequest ) returns ( EchoResponse );\n",
		DescriptorSet: raw,
		SourceId:      "reflection:127.0.0.1:50055",
	}
}

func TestDescribe(t *testing.T) {
	t.Run("proto text on stdout, the source on stderr", func(t *testing.T) {
		fc := newFake()
		fc.described = describedEcho(t)

		out, errOut, code := runCLI(fc, "", "describe", "echo.v1.EchoService/Unary")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, errOut)
		}
		if !strings.Contains(out, "rpc Unary") {
			t.Errorf("stdout = %q, want the rendered rpc", out)
		}
		if !strings.Contains(errOut, "reflection:127.0.0.1:50055") {
			t.Errorf("stderr = %q, want the source id", errOut)
		}
		if strings.Contains(out, "reflection:") {
			t.Errorf("stdout = %q, must not carry the diagnostic", out)
		}
		if len(fc.gotDescribe) != 1 {
			t.Fatalf("DescribeMethod called %d time(s), want 1", len(fc.gotDescribe))
		}
		got := fc.gotDescribe[0]
		if got.GetService() != "echo.v1.EchoService" || got.GetMethod() != "Unary" {
			t.Errorf("service/method = %q/%q, want the argument split on the LAST slash",
				got.GetService(), got.GetMethod())
		}
		if got.GetWorkspaceName() != "default" {
			t.Errorf("workspace_name = %q, want the inherited default", got.GetWorkspaceName())
		}
	})

	t.Run("-o json round-trips into a FileDescriptorSet", func(t *testing.T) {
		fc := newFake()
		fc.described = describedEcho(t)

		out, _, code := runCLI(fc, "", "describe", "echo.v1.EchoService/Unary", "-o", "json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		var set descriptorpb.FileDescriptorSet
		if err := protojson.Unmarshal([]byte(out), &set); err != nil {
			t.Fatalf("stdout does not parse as a FileDescriptorSet: %v (%q)", err, out)
		}
		if len(set.GetFile()) != 1 || set.GetFile()[0].GetName() != "proto/echo/v1/echo.proto" {
			t.Errorf("parsed set = %v, want the one fixture file", set.GetFile())
		}
		if msgs := set.GetFile()[0].GetMessageType(); len(msgs) != 1 || msgs[0].GetName() != "EchoRequest" {
			t.Errorf("messages = %v, want EchoRequest", msgs)
		}
	})

	t.Run("failures", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			args     []string
			fake     func(*fakeClient)
			wantErr  string
			wantCall bool
		}{
			{
				name:    "an argument with no slash",
				args:    []string{"describe", "EchoUnary"},
				wantErr: "describe takes <service>/<method>",
			},
			{
				name:    "an invalid -o",
				args:    []string{"describe", "echo.v1.EchoService/Unary", "-o", "text"},
				wantErr: `invalid --output "text"`,
			},
			{
				name: "a Connect error from the RPC",
				args: []string{"describe", "echo.v1.EchoService/Nope"},
				fake: func(fc *fakeClient) {
					fc.describeErr = connect.NewError(connect.CodeNotFound,
						errors.New(`method "Nope" is not in service "echo.v1.EchoService"`))
				},
				wantErr:  "failed to describe",
				wantCall: true,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fc := newFake()
				fc.described = describedEcho(t)
				if tc.fake != nil {
					tc.fake(fc)
				}

				out, errOut, code := runCLI(fc, "", tc.args...)
				if code != 2 {
					t.Errorf("exit code = %d, want 2 (stderr=%q)", code, errOut)
				}
				if out != "" {
					t.Errorf("stdout = %q, want empty on failure", out)
				}
				if !strings.Contains(errOut, tc.wantErr) {
					t.Errorf("stderr = %q, want it to mention %q", errOut, tc.wantErr)
				}
				if called := len(fc.gotDescribe) > 0; called != tc.wantCall {
					t.Errorf("DescribeMethod called = %v, want %v", called, tc.wantCall)
				}
			})
		}
	})
}
