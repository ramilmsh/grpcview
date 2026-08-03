package workspace

import (
	"context"
	"testing"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func TestResolveTargetServiceAware(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	serverA := &grpcviewv1.Server{Address: "a.example.com:50051"}
	serverB := &grpcviewv1.Server{Address: "b.example.com:50052"}

	sources := []*grpcviewv1.DescriptorSource{
		{Id: "reflection:" + serverA.GetAddress(), Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: serverA}},
		{Id: "reflection:" + serverB.GetAddress(), Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: serverB}},
	}
	services := []*grpcviewv1.Service{
		{Package: "acme.v1", Name: "OrderService", Source: serverB},
		{Package: "acme.v1", Name: "LegacyService"},
	}
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{Sources: sources, Services: services}); err != nil {
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
