package store

import (
	"context"
	"errors"
	"fmt"
	"os"

	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// The only place that knows both the on-disk schema (grpcview.store.v1) and the wire one.

func diskToWireRequest(name string, dr *grpcviewstorev1.Request) *grpcviewv1.Request {
	return &grpcviewv1.Request{
		Name:                name,
		Service:             dr.GetService(),
		Method:              dr.GetMethod(),
		DraftBody:           dr.GetDraftBody(),
		DraftMetadataScript: dr.GetDraftMetadataScript(),
		Middleware:          dr.GetMiddleware(),
		Target:              targetToServer(dr.GetTarget()),
	}
}

func diskToWireScript(name string, ds *grpcviewstorev1.Script) *grpcviewv1.Script {
	return &grpcviewv1.Script{
		Name:   name,
		Kind:   diskToWireScriptKind(ds.GetKind()),
		Source: ds.GetSource(),
	}
}

func diskToWireScriptKind(k grpcviewstorev1.ScriptKind) grpcviewv1.ScriptKind {
	switch k {
	case grpcviewstorev1.ScriptKind_SCRIPT_KIND_GENERATOR:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR
	case grpcviewstorev1.ScriptKind_SCRIPT_KIND_MIDDLEWARE:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE
	case grpcviewstorev1.ScriptKind_SCRIPT_KIND_SCENARIO:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO
	default:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_UNSPECIFIED
	}
}

func wireToDiskScriptKind(k grpcviewv1.ScriptKind) grpcviewstorev1.ScriptKind {
	switch k {
	case grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR:
		return grpcviewstorev1.ScriptKind_SCRIPT_KIND_GENERATOR
	case grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE:
		return grpcviewstorev1.ScriptKind_SCRIPT_KIND_MIDDLEWARE
	case grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO:
		return grpcviewstorev1.ScriptKind_SCRIPT_KIND_SCENARIO
	default:
		return grpcviewstorev1.ScriptKind_SCRIPT_KIND_UNSPECIFIED
	}
}

// diskToWireSources preserves priority order; an upload's descriptors stay on disk.
func diskToWireSources(in []*grpcviewstorev1.DescriptorSource) ([]*grpcviewv1.DescriptorSource, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*grpcviewv1.DescriptorSource, 0, len(in))
	for _, ds := range in {
		out = append(out, diskToWireSource(ds))
	}
	return out, nil
}

func diskToWireSource(ds *grpcviewstorev1.DescriptorSource) *grpcviewv1.DescriptorSource {
	out := &grpcviewv1.DescriptorSource{Id: ds.GetId()}
	switch src := ds.GetSource().(type) {
	case *grpcviewstorev1.DescriptorSource_Reflection:
		out.Source = &grpcviewv1.DescriptorSource_Reflection{Reflection: reflectionToServer(src.Reflection)}
	case *grpcviewstorev1.DescriptorSource_Upload:
		out.Source = &grpcviewv1.DescriptorSource_Upload{
			Upload: &grpcviewv1.Upload{FileName: src.Upload.GetFileName()},
		}
	}
	return out
}

// wireToDiskSources takes uploadFor because the wire form omits an upload's descriptors.
func wireToDiskSources(in []*grpcviewv1.DescriptorSource, uploadFor func(id string) *descriptorpb.FileDescriptorSet) ([]*grpcviewstorev1.DescriptorSource, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(in))
	for _, ws := range in {
		d, err := wireToDiskSource(ws, uploadFor)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func wireToDiskSource(ws *grpcviewv1.DescriptorSource, uploadFor func(id string) *descriptorpb.FileDescriptorSet) (*grpcviewstorev1.DescriptorSource, error) {
	out := &grpcviewstorev1.DescriptorSource{Id: ws.GetId()}
	switch src := ws.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		out.Source = &grpcviewstorev1.DescriptorSource_Reflection{Reflection: serverToReflection(src.Reflection)}
	case *grpcviewv1.DescriptorSource_Upload:
		fds := uploadFor(ws.GetId())
		if fds == nil {
			return nil, fmt.Errorf("upload source %q has no descriptors to store", ws.GetId())
		}
		out.Source = &grpcviewstorev1.DescriptorSource_Upload{
			Upload: &grpcviewstorev1.Upload{
				FileName:      src.Upload.GetFileName(),
				DescriptorSet: fds,
			},
		}
	}
	return out, nil
}

// UploadDescriptors returns an upload's committed FileDescriptorSet, or nil for a non-upload.
func (c *Collection) UploadDescriptors(_ context.Context, id string) (*descriptorpb.FileDescriptorSet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	col, err := c.readCollection()
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	for _, ds := range col.GetSources() {
		if ds.GetId() == id {
			return ds.GetUpload().GetDescriptorSet(), nil
		}
	}
	return nil, nil
}

// serverFromAddressTLS builds a wire Server from the address/tls pair on-disk messages carry.
func serverFromAddressTLS(address string, tls bool) *grpcviewv1.Server {
	s := &grpcviewv1.Server{Address: address}
	if tls {
		s.Tls = &grpcviewv1.Server_TLS{}
	}
	return s
}

func addressTLSFromServer(s *grpcviewv1.Server) (address string, tls bool) {
	return s.GetAddress(), s.GetTls() != nil
}

func reflectionToServer(r *grpcviewstorev1.Reflection) *grpcviewv1.Server {
	if r == nil {
		return nil
	}
	return serverFromAddressTLS(r.GetAddress(), r.GetTls())
}

func serverToReflection(s *grpcviewv1.Server) *grpcviewstorev1.Reflection {
	if s == nil {
		return nil
	}
	address, tls := addressTLSFromServer(s)
	return &grpcviewstorev1.Reflection{Address: address, Tls: tls}
}

func targetToServer(t *grpcviewstorev1.Target) *grpcviewv1.Server {
	if t == nil {
		return nil
	}
	return serverFromAddressTLS(t.GetAddress(), t.GetTls())
}

func serverToTarget(s *grpcviewv1.Server) *grpcviewstorev1.Target {
	if s == nil {
		return nil
	}
	address, tls := addressTLSFromServer(s)
	return &grpcviewstorev1.Target{Address: address, Tls: tls}
}
