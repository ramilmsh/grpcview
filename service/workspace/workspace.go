package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	_ "github.com/mattn/go-sqlite3"

	"codeberg.org/ramilmsh/grpcview/inspector"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

var errWorkspaceNotFound = errors.New("workspace not found")

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

func (w Workspace) getWorkspacePath(ctx context.Context, name string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}

	return path.Join(configDir, ".grpcview", name), nil
}

func (w Workspace) load(ctx context.Context, name string) (*grpcviewv1.Workspace, error) {
	workspacePath, err := w.getWorkspacePath(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace path: %w", err)
	}

	file, err := os.OpenFile(workspacePath, os.O_RDONLY, 0644)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errWorkspaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace file: %w", err)
	}

	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace file: %w", err)
	}

	workspace := &grpcviewv1.Workspace{}
	if err := proto.Unmarshal(data, workspace); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workspace: %w", err)
	}

	return workspace, nil
}

func (w Workspace) save(ctx context.Context, workspace *grpcviewv1.Workspace) error {
	data, err := proto.Marshal(workspace)
	if err != nil {
		return fmt.Errorf("failed to marshal workspace: %w", err)
	}

	workspacePath, err := w.getWorkspacePath(ctx, workspace.GetName())
	if err != nil {
		return fmt.Errorf("failed to get workspace path: %w", err)
	}

	if err := os.MkdirAll(path.Dir(workspacePath), 0755); err != nil {
		return fmt.Errorf("failed to create workspace directory: %w", err)
	}

	file, err := os.OpenFile(workspacePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open workspace file: %w", err)
	}

	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}

	return nil
}

func (w Workspace) AddDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	workspace, err := w.load(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	switch source := request.Msg.GetSource().(type) {
	case *grpcviewv1.AddDescriptorSourceRequest_DescriptorSet:
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("unimplemented source type: <%T> %+v", source, source))
	case *grpcviewv1.AddDescriptorSourceRequest_Reflection:
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

		for _, serviceName := range services {
			fileDesc, err := client.FileContainingSymbol(serviceName)
			if err != nil {
				return nil, fmt.Errorf("failed to get file for service [%s]: %w", serviceName, err)
			}

			serviceDesc := fileDesc.FindSymbol(serviceName).(*desc.ServiceDescriptor)

			service := &grpcviewv1.Service{
				Package: serviceDesc.GetFile().AsFileDescriptorProto().GetPackage(),
				Name:    serviceDesc.GetName(),
				Methods: make([]*grpcviewv1.Method, len(serviceDesc.GetMethods())),
			}

			index := slices.IndexFunc(workspace.Services, func(s *grpcviewv1.Service) bool {
				return s.GetName() == serviceDesc.GetFullyQualifiedName()
			})
			if index == -1 {
				workspace.Services = append(workspace.Services, service)
			} else {
				workspace.Services[index] = service
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

				service.Methods[j] = &grpcviewv1.Method{
					Name: methodDesc.GetName(),
					Input: &grpcviewv1.Message{
						Package: inputDesc.GetFile().AsFileDescriptorProto().GetPackage(),
						Name:    inputDesc.GetName(),
						Schema:  schemaStruct,
					},
				}
			}
		}

		if err := w.save(ctx, workspace); err != nil {
			return nil, fmt.Errorf("failed to save workspace: %w", err)
		}

		return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{
			Workspace: workspace,
		}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}
}

func (w Workspace) Get(ctx context.Context, request *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error) {
	workspace, err := w.load(ctx, request.Msg.GetWorkspaceName())
	if errors.Is(err, errWorkspaceNotFound) {
		workspace = &grpcviewv1.Workspace{
			Name: request.Msg.GetWorkspaceName(),
			Item: &grpcviewv1.Item{
				Name: request.Msg.GetWorkspaceName(),
				Content: &grpcviewv1.Item_Folder{
					Folder: &grpcviewv1.Folder{
						Items: make([]*grpcviewv1.Item, 0),
					},
				},
			},
		}

		if err := w.save(ctx, workspace); err != nil {
			return nil, fmt.Errorf("failed to save workspace: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	return connect.NewResponse(&grpcviewv1.GetResponse{
		Workspace: workspace,
	}), nil
}

func findItem(item *grpcviewv1.Item, protoPath []string) (*grpcviewv1.Item, error) {
	if len(protoPath) == 0 {
		return item, nil
	}

	currentItem := item
	for _, name := range protoPath {
		if currentItem.GetFolder() == nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("folder %s is not a folder", name))
		}

		items := currentItem.GetFolder().GetItems()

		index := slices.IndexFunc(items, func(item *grpcviewv1.Item) bool {
			return item.GetName() == name
		})
		if index == -1 {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("folder %s not found", name))
		}

		currentItem = items[index]
	}

	return currentItem, nil
}

func findFolder(item *grpcviewv1.Item, protoPath []string) (*grpcviewv1.Item, error) {
	currentItem, err := findItem(item, protoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find item: %w", err)
	}

	switch currentItem.Content.(type) {
	case *grpcviewv1.Item_Folder:
		return currentItem, nil
	default:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("item %s is not a folder", path.Join(append(protoPath, currentItem.GetName())...)))
	}
}

func findFile(item *grpcviewv1.Item, protoPath []string) (*grpcviewv1.Item, error) {
	currentItem, err := findItem(item, protoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find item: %w", err)
	}

	switch currentItem.Content.(type) {
	case *grpcviewv1.Item_Request:
		return currentItem, nil
	default:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("item %s is not a file", path.Join(append(protoPath, currentItem.GetName())...)))
	}
}

// CreateFolder implements [grpcviewv1.WorkspaceServiceHandler].
func (w *Workspace) CreateFolder(ctx context.Context, request *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error) {
	workspace, err := w.load(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	path := request.Msg.GetPath()
	folder, err := findFolder(workspace.GetItem(), path)
	if err != nil {
		return nil, err
	}

	index := slices.IndexFunc(folder.GetFolder().GetItems(), func(item *grpcviewv1.Item) bool {
		return item.GetName() == request.Msg.GetItemName()
	})
	if index != -1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("folder %s already exists", request.Msg.GetItemName()))
	}

	folder.GetFolder().Items = append(folder.GetFolder().Items, &grpcviewv1.Item{
		Name: request.Msg.GetItemName(),
		Content: &grpcviewv1.Item_Folder{
			Folder: &grpcviewv1.Folder{
				Items: make([]*grpcviewv1.Item, 0),
			},
		},
	})

	if err := w.save(ctx, workspace); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	return connect.NewResponse(&grpcviewv1.CreateFolderResponse{
		Workspace: workspace,
	}), nil
}

// CreateRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w *Workspace) CreateRequest(ctx context.Context, request *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error) {
	fmt.Println(request.Msg)
	workspace, err := w.load(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	folder, err := findFolder(workspace.GetItem(), request.Msg.GetPath())
	if err != nil {
		return nil, err
	}

	index := slices.IndexFunc(folder.GetFolder().GetItems(), func(item *grpcviewv1.Item) bool {
		return item.GetName() == request.Msg.GetItemName()
	})
	if index != -1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("request %s already exists", request.Msg.GetItemName()))
	}

	folder.GetFolder().Items = append(folder.GetFolder().Items, &grpcviewv1.Item{
		Name: request.Msg.GetItemName(),
		Content: &grpcviewv1.Item_Request{
			Request: &grpcviewv1.Request{
				Name:    request.Msg.GetItemName(),
				Service: request.Msg.GetService(),
				Method:  request.Msg.GetMethod(),
			},
		},
	})

	fmt.Println(workspace.GetItem())

	if err := w.save(ctx, workspace); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	return connect.NewResponse(&grpcviewv1.CreateRequestResponse{
		Workspace: workspace,
	}), nil
}

// DeleteRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w *Workspace) DeleteRequest(ctx context.Context, request *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error) {
	workspace, err := w.load(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	folder, err := findFolder(workspace.GetItem(), request.Msg.GetPath())
	if err != nil {
		return nil, err
	}

	folder.GetFolder().Items = slices.DeleteFunc(folder.GetFolder().GetItems(), func(item *grpcviewv1.Item) bool {
		return item.GetName() == request.Msg.GetItemName()
	})

	if err := w.save(ctx, workspace); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	return connect.NewResponse(&grpcviewv1.DeleteRequestResponse{
		Workspace: workspace,
	}), nil
}

// UpdateRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w *Workspace) UpdateRequest(ctx context.Context, request *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error) {
	workspace, err := w.load(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	file, err := findFile(workspace.GetItem(), append(request.Msg.GetPath(), request.Msg.GetItemName()))
	if err != nil {
		return nil, err
	}

	r := file.GetRequest()

	if request.Msg.Method != nil {
		r.Method = *request.Msg.Method
	}

	if request.Msg.Service != nil {
		r.Service = *request.Msg.Service
	}

	if request.Msg.DraftBody != nil {
		r.DraftBody = request.Msg.DraftBody
	}

	if request.Msg.DraftMetadata != nil {
		r.DraftMetadata = request.Msg.DraftMetadata
	}

	fmt.Println(prototext.MarshalOptions{Indent: "  "}.Format(r))

	if err = w.save(ctx, workspace); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	return connect.NewResponse(&grpcviewv1.UpdateRequestResponse{
		Workspace: workspace,
	}), nil
}
