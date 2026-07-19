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
