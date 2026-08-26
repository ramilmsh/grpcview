package workspace

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func serviceFileSet(fileName, pkg string, services ...string) *descriptorpb.FileDescriptorSet {
	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String(fileName),
		Package: proto.String(pkg),
		Syntax:  proto.String("proto3"),
	}
	for _, name := range services {
		file.Service = append(file.Service, &descriptorpb.ServiceDescriptorProto{Name: proto.String(name)})
	}
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}}
}

func TestResolveTargetServiceAware(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	serverA := &grpcviewv1.Server{Address: "a.example.com:50051"}
	serverB := &grpcviewv1.Server{Address: "b.example.com:50052"}

	const uploadID = "upload:legacy.binpb"
	sources := []*grpcviewv1.DescriptorSource{
		{Id: "reflection:" + serverA.GetAddress(), Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: serverA}},
		{Id: "reflection:" + serverB.GetAddress(), Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: serverB}},
		{Id: uploadID, Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: "legacy.binpb"}}},
	}
	legacy := serviceFileSet("acme/v1/legacy.proto", "acme.v1", "LegacyService")
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{
		Sources: sources,
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			"reflection:" + serverA.GetAddress(): {Id: "reflection:" + serverA.GetAddress()},
			"reflection:" + serverB.GetAddress(): {
				Id:            "reflection:" + serverB.GetAddress(),
				DescriptorSet: serviceFileSet("acme/v1/order.proto", "acme.v1", "OrderService"),
				ServiceNames:  []string{"acme.v1.OrderService"},
			},
			uploadID: {Id: uploadID, DescriptorSet: legacy, ServiceNames: []string{"acme.v1.LegacyService"}},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	got, err := w.resolveTarget(ctx, nil, testWorkspace, "acme.v1.OrderService")
	if err != nil {
		t.Fatalf("resolveTarget(OrderService): %v", err)
	}
	if got.GetAddress() != "b.example.com:50052" {
		t.Errorf("OrderService target = %s, want b.example.com:50052 (its attributed source)",
			got.GetAddress())
	}

	got, err = w.resolveTarget(ctx, nil, testWorkspace, "acme.v1.LegacyService")
	if err != nil {
		t.Fatalf("resolveTarget(LegacyService): %v", err)
	}
	if got.GetAddress() != "a.example.com:50051" {
		t.Errorf("LegacyService target = %s, want a.example.com:50051 (first reflection source)",
			got.GetAddress())
	}

	got, err = w.resolveTarget(ctx, nil, testWorkspace, "acme.v1.UnknownService")
	if err != nil {
		t.Fatalf("resolveTarget(UnknownService): %v", err)
	}
	if got.GetAddress() != "a.example.com:50051" {
		t.Errorf("unknown service target = %s, want a.example.com:50051 (first reflection source)", got.GetAddress())
	}

	override := &grpcviewv1.Server{Address: "override.example.com:9999"}
	got, err = w.resolveTarget(ctx, override, testWorkspace, "acme.v1.OrderService")
	if err != nil {
		t.Fatalf("resolveTarget(override): %v", err)
	}
	if got.GetAddress() != "override.example.com:9999" {
		t.Errorf("override target = %s, want override.example.com:9999", got.GetAddress())
	}
}
