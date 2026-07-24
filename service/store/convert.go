package store

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// This file maps between the on-disk schema (grpcview.store.v1) and the wire
// schema (grpcview.v1). The two are intentionally decoupled; conversions here
// are the single place that knows both.

// diskToWireRequest builds a wire Request from a disk Request.
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

// diskToWireScript builds a wire Script from a disk Script (name carried
// separately, like requests: the display name lives in meta on disk).
func diskToWireScript(name string, ds *grpcviewstorev1.Script) *grpcviewv1.Script {
	return &grpcviewv1.Script{
		Name:   name,
		Kind:   diskToWireScriptKind(ds.GetKind()),
		Source: ds.GetSource(),
	}
}

// diskToWireScriptKind / wireToDiskScriptKind bridge the two mirrored enums. The
// ordinals match, but the mapping is explicit so a future divergence is a compile
// error here rather than a silent misread.
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

// diskToWireSources converts the committed descriptor sources for the wire.
func diskToWireSources(in []*grpcviewstorev1.DescriptorSource) ([]*grpcviewv1.DescriptorSource, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*grpcviewv1.DescriptorSource, 0, len(in))
	for _, ds := range in {
		w, err := diskToWireSource(ds)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func diskToWireSource(ds *grpcviewstorev1.DescriptorSource) (*grpcviewv1.DescriptorSource, error) {
	switch src := ds.GetSource().(type) {
	case *grpcviewstorev1.DescriptorSource_Reflection:
		return &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: reflectionToServer(src.Reflection)},
		}, nil
	case *grpcviewstorev1.DescriptorSource_DescriptorSet:
		// The wire carries the descriptor set as opaque bytes; re-serialize the
		// typed on-disk FileDescriptorSet back into them.
		raw, err := proto.Marshal(src.DescriptorSet)
		if err != nil {
			return nil, fmt.Errorf("marshal descriptor set: %w", err)
		}
		return &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_DescriptorSet{DescriptorSet: raw},
		}, nil
	default:
		return &grpcviewv1.DescriptorSource{}, nil
	}
}

// wireToDiskSources converts wire descriptor sources for on-disk storage.
func wireToDiskSources(in []*grpcviewv1.DescriptorSource) ([]*grpcviewstorev1.DescriptorSource, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*grpcviewstorev1.DescriptorSource, 0, len(in))
	for _, ws := range in {
		d, err := wireToDiskSource(ws)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func wireToDiskSource(ws *grpcviewv1.DescriptorSource) (*grpcviewstorev1.DescriptorSource, error) {
	switch src := ws.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		return &grpcviewstorev1.DescriptorSource{
			Source: &grpcviewstorev1.DescriptorSource_Reflection{Reflection: serverToReflection(src.Reflection)},
		}, nil
	case *grpcviewv1.DescriptorSource_DescriptorSet:
		// Store the descriptor set typed so it round-trips as readable protojson
		// rather than a base64 blob.
		fds := &descriptorpb.FileDescriptorSet{}
		if err := proto.Unmarshal(src.DescriptorSet, fds); err != nil {
			return nil, fmt.Errorf("unmarshal descriptor set: %w", err)
		}
		return &grpcviewstorev1.DescriptorSource{
			Source: &grpcviewstorev1.DescriptorSource_DescriptorSet{DescriptorSet: fds},
		}, nil
	default:
		return &grpcviewstorev1.DescriptorSource{}, nil
	}
}

// serverFromAddressTLS builds a wire Server from the address/tls pair every
// on-disk server-shaped message carries (a source's Reflection, a request's
// Target). tls=true carries an empty TLS block; false leaves it nil.
func serverFromAddressTLS(address string, tls bool) *grpcviewv1.Server {
	s := &grpcviewv1.Server{Address: address}
	if tls {
		s.Tls = &grpcviewv1.Server_TLS{}
	}
	return s
}

// addressTLSFromServer decomposes a wire Server into the address/tls pair the
// on-disk server-shaped messages store.
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
