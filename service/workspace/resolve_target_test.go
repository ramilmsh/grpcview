package workspace

import (
	"context"
	"testing"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// TestResolveTargetServiceAware covers resolveTarget's service-aware default — the
// multi-source mis-default fix. With no explicit target: a request resolves to the
// reflection source its service was attributed to (Service.source), NOT merely the
// workspace's first reflection source; a service with no attributed source (as a
// descriptor-set upload / older cache leaves it) and an unrecognized service both
// fall back to the first reflection source; and an explicit target overrides all.
//
// The sources + services are seeded through the store (PutDescriptorState) exactly
// as the source merge persists them — sources to the manifest, services (carrying
// Source) to the derived cache — so resolveTarget reads them back via
// coll.Services / coll.Sources without a live reflection round-trip.
func TestResolveTargetServiceAware(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	serverA := &grpcviewv1.Server{Address: "a.example.com:50051"}
	serverB := &grpcviewv1.Server{Address: "b.example.com:50052"}

	// Two reflection sources (A first, B second) and two services: OrderService
	// attributed to the second source B, LegacyService with no attributed source.
	sources := []*grpcviewv1.DescriptorSource{
		{Id: "reflection:" + serverA.GetAddress(), Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: serverA}},
		{Id: "reflection:" + serverB.GetAddress(), Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: serverB}},
	}
	services := []*grpcviewv1.Service{
		{Package: "acme.v1", Name: "OrderService", Source: serverB},
		{Package: "acme.v1", Name: "LegacyService"}, // no attributed source
	}
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{Sources: sources, Services: services}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	// A service attributed to B resolves to B — not the first source A. This is the
	// bug fix: before, this defaulted to A.
	got, err := w.resolveTarget(ctx, nil, testWorkspace, "acme.v1.OrderService")
	if err != nil {
		t.Fatalf("resolveTarget(OrderService): %v", err)
	}
	if got.GetAddress() != "b.example.com:50052" {
		t.Errorf("OrderService target = %s, want b.example.com:50052 (its attributed source)",
			got.GetAddress())
	}

	// A service with no attributed source falls back to the first reflection source A.
	got, err = w.resolveTarget(ctx, nil, testWorkspace, "acme.v1.LegacyService")
	if err != nil {
		t.Fatalf("resolveTarget(LegacyService): %v", err)
	}
	if got.GetAddress() != "a.example.com:50051" {
		t.Errorf("LegacyService target = %s, want a.example.com:50051 (first reflection source)",
			got.GetAddress())
	}

	// An unrecognized service likewise falls back to the first reflection source.
	got, err = w.resolveTarget(ctx, nil, testWorkspace, "acme.v1.UnknownService")
	if err != nil {
		t.Fatalf("resolveTarget(UnknownService): %v", err)
	}
	if got.GetAddress() != "a.example.com:50051" {
		t.Errorf("unknown service target = %s, want a.example.com:50051 (first reflection source)", got.GetAddress())
	}

	// An explicit target overrides the service-aware default entirely (returned as-is,
	// no store read), even for a service that would otherwise resolve to B.
	override := &grpcviewv1.Server{Address: "override.example.com:9999"}
	got, err = w.resolveTarget(ctx, override, testWorkspace, "acme.v1.OrderService")
	if err != nil {
		t.Fatalf("resolveTarget(override): %v", err)
	}
	if got.GetAddress() != "override.example.com:9999" {
		t.Errorf("override target = %s, want override.example.com:9999", got.GetAddress())
	}
}
