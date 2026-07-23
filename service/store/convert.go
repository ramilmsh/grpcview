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
// are the single place that knows both. Where a type is genuinely identical on
// both sides — google.protobuf.Struct for draft metadata — it passes straight
// through with no copy.

// diskToWireRequest builds a wire Request from a disk Request.
func diskToWireRequest(name string, dr *grpcviewstorev1.Request) *grpcviewv1.Request {
	return &grpcviewv1.Request{
		Name:          name,
		Service:       dr.GetService(),
		Method:        dr.GetMethod(),
		DraftBody:     dr.GetDraftBody(),
		DraftMetadata: dr.GetDraftMetadata(), // Struct: identical on both sides
		Middleware:    dr.GetMiddleware(),
		BodyLanguage:  diskToWireBodyLanguage(dr.GetBodyLanguage()),
	}
}

// wireToDiskRequest builds a disk Request from a wire Request.
func wireToDiskRequest(name string, wr *grpcviewv1.Request) *grpcviewstorev1.Request {
	return &grpcviewstorev1.Request{
		Meta:          &grpcviewstorev1.ItemMeta{Name: name},
		Service:       wr.GetService(),
		Method:        wr.GetMethod(),
		DraftBody:     wr.GetDraftBody(),
		DraftMetadata: wr.GetDraftMetadata(),
		Middleware:    wr.GetMiddleware(),
		BodyLanguage:  wireToDiskBodyLanguage(wr.GetBodyLanguage()),
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

// diskToWireBodyLanguage / wireToDiskBodyLanguage bridge the two mirrored
// BodyLanguage enums, exactly like the ScriptKind bridge above: the ordinals
// match, but the mapping is explicit so a future divergence is a compile error
// here rather than a silent misread. UNSPECIFIED (the zero value, so the default
// for a request stored before this field existed) and JSON both mean "send
// as-is", so the default case correctly maps an unknown/zero value to UNSPECIFIED.
func diskToWireBodyLanguage(l grpcviewstorev1.BodyLanguage) grpcviewv1.BodyLanguage {
	switch l {
	case grpcviewstorev1.BodyLanguage_BODY_LANGUAGE_JSON:
		return grpcviewv1.BodyLanguage_BODY_LANGUAGE_JSON
	case grpcviewstorev1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT:
		return grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT
	default:
		return grpcviewv1.BodyLanguage_BODY_LANGUAGE_UNSPECIFIED
	}
}

func wireToDiskBodyLanguage(l grpcviewv1.BodyLanguage) grpcviewstorev1.BodyLanguage {
	switch l {
	case grpcviewv1.BodyLanguage_BODY_LANGUAGE_JSON:
		return grpcviewstorev1.BodyLanguage_BODY_LANGUAGE_JSON
	case grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT:
		return grpcviewstorev1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT
	default:
		return grpcviewstorev1.BodyLanguage_BODY_LANGUAGE_UNSPECIFIED
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

func reflectionToServer(r *grpcviewstorev1.Reflection) *grpcviewv1.Server {
	if r == nil {
		return nil
	}
	s := &grpcviewv1.Server{Host: r.GetHost(), Port: r.GetPort()}
	if r.GetTls() {
		s.Tls = &grpcviewv1.Server_TLS{}
	}
	return s
}

func serverToReflection(s *grpcviewv1.Server) *grpcviewstorev1.Reflection {
	if s == nil {
		return nil
	}
	return &grpcviewstorev1.Reflection{
		Host: s.GetHost(),
		Port: s.GetPort(),
		Tls:  s.GetTls() != nil,
	}
}
