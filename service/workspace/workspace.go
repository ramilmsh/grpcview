package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ramilmsh/grpcview/inspector"
	grpcviewv1 "github.com/ramilmsh/grpcview/service/proto/v1"
)

type Workspace struct {
	db *sql.DB
}

func New(ctx context.Context) (Workspace, error) {
	db, err := sql.Open("sqlite3", "/tmp/ws.db")
	if err != nil {
		return Workspace{}, nil
	}

	return Workspace{db: db}, nil
}

func (w Workspace) Close(ctx context.Context) error {
	return w.db.Close()
}

func (w Workspace) addFileDescriptorSet(fileDescriptorSet *descriptorpb.FileDescriptorSet) error {
	fmt.Println(fileDescriptorSet.String())
	return nil
}

func (w Workspace) Add(ctx context.Context, request *connect.Request[grpcviewv1.AddRequest]) (*connect.Response[grpcviewv1.AddResponse], error) {
	switch source := request.Msg.GetSource().(type) {
	case *grpcviewv1.AddRequest_DescriptorSet:
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("unimplemented source type: <%T> %+v", source, source))
	case *grpcviewv1.AddRequest_Reflection:
		conn, err := grpc.NewClient(
			fmt.Sprintf("%s:%d", source.Reflection.Host, source.Reflection.Port),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return nil, fmt.Errorf("coudn't connect to %s: %w", source.Reflection, err)
		}

		client := grpcreflect.NewClientAuto(ctx, conn)
		services, err := client.ListServices()
		if err != nil {
			return nil, fmt.Errorf("failed to list services: %w", err)
		}

		response := &grpcviewv1.AddResponse{
			Services: make([]*grpcviewv1.AddResponse_Service, len(services)),
		}

		for i, service := range services {
			fileDesc, err := client.FileContainingSymbol(service)
			if err != nil {
				return nil, fmt.Errorf("failed to get file for service [%s]: %w", service, err)
			}

			serviceDesc := fileDesc.FindSymbol(service).(*desc.ServiceDescriptor)

			response.Services[i] = &grpcviewv1.AddResponse_Service{
				Package: serviceDesc.GetFile().AsFileDescriptorProto().GetPackage(),
				Name:    serviceDesc.GetName(),
				Methods: make([]*grpcviewv1.AddResponse_Method, len(serviceDesc.GetMethods())),
			}

			for j, methodDesc := range serviceDesc.GetMethods() {
				inputDesc := methodDesc.GetInputType()
				schema, err := inspector.ConvertMessage(inputDesc.UnwrapMessage())
				if err != nil {
					return nil, fmt.Errorf("failed to convert message (%s) to schema: %w", inputDesc.Unwrap().FullName(), err)
				}

				encodedSchema, err := json.Marshal(schema)
				if err != nil {
					return nil, err
				}

				decodedSchema := make(map[string]any)
				err = json.Unmarshal(encodedSchema, &decodedSchema)
				if err != nil {
					return nil, err
				}

				schemaStruct, err := structpb.NewStruct(decodedSchema)
				if err != nil {
					return nil, err
				}

				response.Services[i].Methods[j] = &grpcviewv1.AddResponse_Method{
					Name: methodDesc.GetName(),
					Input: &grpcviewv1.AddResponse_Message{
						Package: inputDesc.GetFile().AsFileDescriptorProto().GetPackage(),
						Name:    inputDesc.GetName(),
						Schema:  schemaStruct,
					},
				}
			}
		}
		return connect.NewResponse(response), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}
}
