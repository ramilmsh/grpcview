package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"codeberg.org/ramilmsh/grpcview/inspector"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// scriptingMaxPages is the outer wazero linear-memory ceiling for the shared
// scripting engine, in 64 KiB pages: 4096 pages = 256 MiB. That sits above the
// most generous profile's inner heap cap (Scenario, 128 MiB) so a legitimate run
// can use its full inner budget with headroom, while still being a hard backstop
// enforced by wazero underneath QuickJS's own accounting.
const scriptingMaxPages = 4096

// Workspace is the WorkspaceService handler. It is a thin adapter over
// store.Store: each mutation delegates to the store and then reloads the whole
// Workspace so responses keep the exact shape the client already expects. It also
// owns the shared scripting Engine (compiled once, instances reused/pooled) that
// backs the RunScript RPC.
type Workspace struct {
	store  *store.Store
	engine *scripting.Engine
}

// New returns a handler persisting collections under the same base the previous
// blob storage used: os.UserConfigDir()/.grpcview/<name>. It also compiles the
// scripting engine once here (the ~660 KiB module compile is the expensive step;
// instances are cheap) so RunScript can serve from a warm engine.
func New(ctx context.Context) (Workspace, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to get user config dir: %w", err)
	}
	engine, err := scripting.NewEngine(ctx, scriptingMaxPages)
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to initialize scripting engine: %w", err)
	}
	return Workspace{
		store:  store.New(filepath.Join(configDir, ".grpcview"), slog.Default()),
		engine: engine,
	}, nil
}

func (w Workspace) Close(ctx context.Context) error {
	if w.engine != nil {
		return w.engine.Close(ctx)
	}
	return nil
}

// toConnectError maps the store's transport-agnostic sentinel errors onto the
// Connect error codes the client relies on; other errors pass through.
func toConnectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrItemNotFound), errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrNotAFolder), errors.Is(err, store.ErrNotARequest),
		errors.Is(err, store.ErrAlreadyExists), errors.Is(err, store.ErrInvalidMove):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return err
	}
}

func (w Workspace) Get(ctx context.Context, request *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}

	ws, err := coll.Load(ctx)
	if errors.Is(err, store.ErrNotFound) {
		if err := coll.EnsureCreated(ctx); err != nil {
			return nil, fmt.Errorf("failed to create workspace: %w", err)
		}
		ws, err = coll.Load(ctx)
	}
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&grpcviewv1.GetResponse{Workspace: ws}), nil
}

func (w Workspace) AddDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}

	switch source := request.Msg.GetSource().(type) {
	case *grpcviewv1.AddDescriptorSourceRequest_DescriptorSet:
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("unimplemented source type: <%T> %+v", source, source))
	case *grpcviewv1.AddDescriptorSourceRequest_Reflection:
		conn, err := dial(source.Reflection)
		if err != nil {
			return nil, fmt.Errorf("couldn't connect to %s: %w", source.Reflection, err)
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

			// Replace an existing entry for this service (matched by the
			// package/name identity we persist) or append a new one.
			index := slices.IndexFunc(ws.Services, func(s *grpcviewv1.Service) bool {
				return s.GetPackage() == service.GetPackage() && s.GetName() == service.GetName()
			})
			if index == -1 {
				ws.Services = append(ws.Services, service)
			} else {
				ws.Services[index] = service
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

		// Persist the source config (committed) and the resolved schema cache
		// (gitignored), then reload so the response reflects on-disk state.
		src := &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: source.Reflection},
		}
		ws.Sources = appendSourceUnique(ws.Sources, src)
		if err := coll.PutDescriptorState(ctx, ws.Sources, ws.Services); err != nil {
			return nil, fmt.Errorf("failed to persist descriptor state: %w", err)
		}

		reloaded, err := coll.Load(ctx)
		if err != nil {
			return nil, toConnectError(err)
		}
		return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{Workspace: reloaded}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}
}

func appendSourceUnique(sources []*grpcviewv1.DescriptorSource, src *grpcviewv1.DescriptorSource) []*grpcviewv1.DescriptorSource {
	if slices.ContainsFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return proto.Equal(s, src) }) {
		return sources
	}
	return append(sources, src)
}

// mutate applies a single store mutation to the named collection and returns the
// reloaded workspace, mapping store sentinels to Connect codes. The four
// tree-mutating RPCs share this open→mutate→reload→map policy and differ only in
// the response type, which each caller wraps.
func (w Workspace) mutate(ctx context.Context, name string, fn func(*store.Collection) error) (*grpcviewv1.Workspace, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := fn(coll); err != nil {
		return nil, toConnectError(err)
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return ws, nil
}

// CreateFolder implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) CreateFolder(ctx context.Context, request *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.CreateFolder(ctx, request.Msg.GetPath(), request.Msg.GetItemName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateFolderResponse{Workspace: ws}), nil
}

// CreateRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) CreateRequest(ctx context.Context, request *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.CreateRequest(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), request.Msg.GetService(), request.Msg.GetMethod())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateRequestResponse{Workspace: ws}), nil
}

// DeleteRequest implements [grpcviewv1.WorkspaceServiceHandler]. It removes any
// item (folder or request) by name, matching the previous behavior.
func (w Workspace) DeleteRequest(ctx context.Context, request *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.Delete(ctx, request.Msg.GetPath(), request.Msg.GetItemName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.DeleteRequestResponse{Workspace: ws}), nil
}

// UpdateRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) UpdateRequest(ctx context.Context, request *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error) {
	patch := store.RequestPatch{
		Name:          request.Msg.Name,
		Service:       request.Msg.Service,
		Method:        request.Msg.Method,
		DraftBody:     request.Msg.DraftBody,
		DraftMetadata: request.Msg.DraftMetadata,
	}
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.UpdateRequest(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateRequestResponse{Workspace: ws}), nil
}
