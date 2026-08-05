package store

import (
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

// Both source conversions preserve priority order, and neither carries descriptor bytes: a
// source is a pointer plus authored config on both sides of the boundary, and its bytes live in
// the descriptor store (see blobs.go).
func diskToWireSources(in []*grpcviewstorev1.DescriptorSource) []*grpcviewv1.DescriptorSource {
	if len(in) == 0 {
		return nil
	}
	out := make([]*grpcviewv1.DescriptorSource, 0, len(in))
	for _, ds := range in {
		out = append(out, diskToWireSource(ds))
	}
	return out
}

func diskToWireSource(ds *grpcviewstorev1.DescriptorSource) *grpcviewv1.DescriptorSource {
	out := &grpcviewv1.DescriptorSource{Id: ds.GetId(), CommitDescriptors: ds.GetCommitDescriptors()}
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

func wireToDiskSources(in []*grpcviewv1.DescriptorSource) []*grpcviewstorev1.DescriptorSource {
	if len(in) == 0 {
		return nil
	}
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(in))
	for _, ws := range in {
		out = append(out, wireToDiskSource(ws))
	}
	return out
}

func wireToDiskSource(ws *grpcviewv1.DescriptorSource) *grpcviewstorev1.DescriptorSource {
	out := &grpcviewstorev1.DescriptorSource{Id: ws.GetId(), CommitDescriptors: ws.GetCommitDescriptors()}
	switch src := ws.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		out.Source = &grpcviewstorev1.DescriptorSource_Reflection{Reflection: serverToReflection(src.Reflection)}
	case *grpcviewv1.DescriptorSource_Upload:
		out.Source = &grpcviewstorev1.DescriptorSource_Upload{
			Upload: &grpcviewstorev1.Upload{FileName: src.Upload.GetFileName()},
		}
	}
	return out
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
