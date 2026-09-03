package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/gen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The read RPC keeps everything: it is an agent's ONLY access to `history`, and stripping any of
// this from it would remove a capability rather than trim a payload.
const readMethod = "Get"

// Every mutating RPC answers with the whole `Collection`, and most of it has nothing to do with
// the edit. Each of these is kept only by the RPCs that can actually change it. Measured on
// `example` (9 requests): a request edit's response is 19.8 KB, of which 7.0 KB is `services` and
// 5.5 KB is `scripts` the caller never touched; `history` is the other bomb and, on a collection
// with real history behind it, by far the biggest.
var fieldOwners = map[protoreflect.Name]map[string]bool{
	"history": {},
	"services": {
		"AddDescriptorSource":       true,
		"RemoveDescriptorSource":    true,
		"RefreshDescriptorSource":   true,
		"ReorderDescriptorSources":  true,
		"SetDescriptorSourceCommit": true,
		"SetWorkspaceTrust":         true,
	},
	"scripts": {
		"CreateScript": true,
		"UpdateScript": true,
		"DeleteScript": true,
	},
}

// Mutates the response, which is safe only because every RPC rebuilds its own from the
// store: this is never a cached message.
//
// Unstripped, a collection of ordinary size blows the MCP client's per-result token cap — a
// recorded response holding a descriptor set is 160 KB of a 186 KB `Collection` — so without this
// every mutation comes back as an overflow error.
func trimHeavyFields(h gen.Handler) gen.Handler {
	return func(ctx context.Context, method protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
		res, err := h(ctx, method, req)
		if err != nil || res == nil {
			return res, err
		}
		clearHeavyFields(res.ProtoReflect(), dropSet(string(method.Name())))
		return res, nil
	}
}

func dropSet(method string) map[protoreflect.Name]bool {
	drop := make(map[protoreflect.Name]bool, len(fieldOwners))
	if method == readMethod {
		return drop
	}
	for name, owners := range fieldOwners {
		drop[name] = !owners[method]
	}
	return drop
}

func clearHeavyFields(m protoreflect.Message, drop map[protoreflect.Name]bool) {
	var (
		clear  []protoreflect.FieldDescriptor
		shrink []protoreflect.FieldDescriptor
	)
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Name() == "descriptor_set" && fd.Kind() == protoreflect.BytesKind && !fd.IsList():
			clear = append(clear, fd)
		case isInvokeResponseBody(m, fd):
			shrink = append(shrink, fd)
		case drop[fd.Name()] && fd.IsList() && fd.Kind() == protoreflect.MessageKind:
			clear = append(clear, fd)
		case fd.IsMap():
			if fd.MapValue().Kind() == protoreflect.MessageKind {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					clearHeavyFields(mv.Message(), drop)
					return true
				})
			}
		case fd.IsList():
			if fd.Kind() == protoreflect.MessageKind {
				l := v.List()
				for i := 0; i < l.Len(); i++ {
					clearHeavyFields(l.Get(i).Message(), drop)
				}
			}
		case fd.Kind() == protoreflect.MessageKind:
			clearHeavyFields(v.Message(), drop)
		}
		return true
	})
	for _, fd := range clear {
		m.Clear(fd)
	}
	for _, fd := range shrink {
		m.Set(fd, protoreflect.ValueOfBytes(shrinkResponseBody(m.Get(fd).Bytes())))
	}
}

// Two shapes carry a response body: the one an invoke answers with, and the one `get_collection`
// replays out of history. The second is the larger source in practice — every recorded call's body
// is kept, so one collection's history holds many of them.
var responseBodyOwners = map[protoreflect.FullName]bool{
	"grpcview.v1.Request.Response": true,
	"grpcview.v1.History.Response": true,
}

func isInvokeResponseBody(m protoreflect.Message, fd protoreflect.FieldDescriptor) bool {
	return fd.Name() == "response" && fd.Kind() == protoreflect.BytesKind && !fd.IsList() &&
		responseBodyOwners[m.Descriptor().FullName()]
}

// Well above every real field in a hand-written collection and well below a descriptor set.
const maxResponseStringBytes = 8 << 10

// clearHeavyFields walks protos, and a proto walk cannot see into a string: a descriptor set is
// base64 inside a JSON response body, which is itself TEXT inside this bytes field, and one such
// response blows the MCP client's per-result cap on its own. Nothing is special-cased by type —
// any oversized string value in the body goes, whatever produced it.
func shrinkResponseBody(raw []byte) []byte {
	if len(raw) <= maxResponseStringBytes {
		return raw
	}

	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return append(append([]byte{}, raw[:maxResponseStringBytes]...), []byte(elisionMarker(len(raw)-maxResponseStringBytes))...)
	}

	shrunk, changed := shrinkJSONStrings(body)
	if !changed {
		return raw
	}
	out, err := json.Marshal(shrunk)
	if err != nil {
		return raw
	}
	return out
}

func shrinkJSONStrings(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		if len(t) <= maxResponseStringBytes {
			return t, false
		}
		return elisionMarker(len(t)), true
	case []any:
		changed := false
		for i, item := range t {
			next, c := shrinkJSONStrings(item)
			t[i] = next
			changed = changed || c
		}
		return t, changed
	case map[string]any:
		changed := false
		for key, item := range t {
			next, c := shrinkJSONStrings(item)
			t[key] = next
			changed = changed || c
		}
		return t, changed
	default:
		return v, false
	}
}

func elisionMarker(n int) string {
	return fmt.Sprintf("[grpcview elided %d bytes: too large for a tool result. Call the RPC directly for the full value.]", n)
}
