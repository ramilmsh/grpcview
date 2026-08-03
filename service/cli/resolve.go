package cli

import (
	"fmt"
	"strings"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

// methodKind is a method's streaming shape. It decides two things at once: which
// of the four invoke RPCs the verb calls, and whether the body on stdin is one
// message or NDJSON (D13).
type methodKind struct {
	client bool
	server bool
}

// streaming reports whether the method streams in either direction. The routing
// question is "does this method stream at all", not "does it client-stream":
// InvokeSaved rejects a server-streaming method with Unimplemented, so a
// server-streaming saved request has to go through the streaming call too.
func (k methodKind) streaming() bool { return k.client || k.server }

// ndjson reports whether the input is read as one protojson message per line.
// Only client-streaming and bidi take many messages; every other kind takes one
// message verbatim, which matters because a TypeScript body is multi-line.
func (k methodKind) ndjson() bool { return k.client }

// invokeTarget is a resolved positional argument: either the saved request at a
// display-name path, or an ad-hoc <service>/<method>. Both forms carry the
// resolved method kind, because both need it to pick an RPC.
type invokeTarget struct {
	// arg is the argument as typed. It labels every diagnostic, so the message a
	// script sees names the thing the script asked for.
	arg string
	// saved distinguishes the two forms.
	saved bool
	// parent and itemName address the saved request: parent folders outermost
	// first, then the request's own display name.
	parent   []string
	itemName string
	// service and method are the full service name and the method name, resolved
	// either from the argument or out of the saved request.
	service string
	method  string
	kind    methodKind
}

// savedLookup is what walking the collection tree for a path found: the request,
// or (for a better error) the fact that the path named a folder.
type savedLookup struct {
	req *grpcviewv1.Request
	// item is the tree node the path resolved to, nil when nothing did. invoke
	// needs only req; ls needs the node itself, to list a folder's children
	// without walking the tree a second time.
	item     *grpcviewv1.Item
	parent   []string
	name     string
	isFolder bool
}

// resolveInvokeArg resolves invoke's one positional argument against BOTH
// interpretations — a saved-request path and a <service>/<method>— against a
// single Get snapshot, and refuses to guess when both match (§5).
//
// Catching NotFound from InvokeSaved cannot replace this: a miss on one
// interpretation says nothing about the other, so the only way to detect
// ambiguity is to try both up front.
func resolveInvokeArg(ws *grpcviewv1.Workspace, arg string) (invokeTarget, error) {
	saved := lookupSaved(ws, arg)
	service, method, kind, adhoc := lookupAdhoc(ws, arg)

	switch {
	case saved.req != nil && adhoc:
		return invokeTarget{}, fmt.Errorf(
			"ambiguous argument %q: it names both the saved request %s in workspace %q and the method %s/%s in the schema — nothing was invoked; rename one of the two",
			arg, arg, ws.GetName(), service, method)

	case saved.req != nil:
		kind, ok := lookupMethod(ws, saved.req.GetService(), saved.req.GetMethod())
		if !ok {
			return invokeTarget{}, fmt.Errorf(
				"cannot invoke the saved request %q: it calls %s/%s, which no definition source in workspace %q resolves; refresh the source or fix the request",
				arg, saved.req.GetService(), saved.req.GetMethod(), ws.GetName())
		}
		return invokeTarget{
			arg:      arg,
			saved:    true,
			parent:   saved.parent,
			itemName: saved.name,
			service:  saved.req.GetService(),
			method:   saved.req.GetMethod(),
			kind:     kind,
		}, nil

	case adhoc:
		return invokeTarget{arg: arg, service: service, method: method, kind: kind}, nil

	default:
		return invokeTarget{}, unknownArgError(ws, arg, saved)
	}
}

// unknownArgError says what was looked for, in both interpretations, because
// "not found" alone leaves the caller unable to tell a typo in a folder name
// from a typo in a package name.
func unknownArgError(ws *grpcviewv1.Workspace, arg string, saved savedLookup) error {
	if saved.isFolder {
		return fmt.Errorf(
			"cannot invoke %q: it is a folder, not a request; invoke addresses a saved request or a <service>/<method>", arg)
	}
	if !strings.Contains(arg, "/") {
		return fmt.Errorf(
			"unknown request %q: no saved request by that name at the top level of workspace %q, and a <service>/<method> argument needs a slash",
			arg, ws.GetName())
	}
	service, method := splitMethodPath(arg)
	return fmt.Errorf(
		"unknown request or method %q: no saved request at that path in workspace %q, and no service %q with a method %q among its %d service(s)",
		arg, ws.GetName(), service, method, len(ws.GetServices()))
}

// lookupSaved walks the collection tree by display name. The workspace's root
// Item is the collection itself, so the parent segments address folders BELOW
// it. A match has to be a request: a folder is not invokable.
func lookupSaved(ws *grpcviewv1.Workspace, arg string) savedLookup {
	parent, name, err := workspace.SplitInvokePath(arg)
	if err != nil {
		// An empty path or an empty final segment is not a saved request. The
		// error text belongs to gv.invoke's phrasing, so it is not surfaced.
		return savedLookup{}
	}

	items := ws.GetItem().GetFolder().GetItems()
	for _, segment := range parent {
		next := itemNamed(items, segment)
		if next == nil || next.GetFolder() == nil {
			return savedLookup{parent: parent, name: name}
		}
		items = next.GetFolder().GetItems()
	}

	found := itemNamed(items, name)
	if found == nil {
		return savedLookup{parent: parent, name: name}
	}
	return savedLookup{
		req:      found.GetRequest(),
		item:     found,
		parent:   parent,
		name:     name,
		isFolder: found.GetFolder() != nil,
	}
}

func itemNamed(items []*grpcviewv1.Item, name string) *grpcviewv1.Item {
	for _, item := range items {
		if item.GetName() == name {
			return item
		}
	}
	return nil
}

// lookupAdhoc reads the argument as <service>/<method>, splitting on the LAST
// slash: a service's full name has dots, never slashes, so everything before the
// final slash is the service.
func lookupAdhoc(ws *grpcviewv1.Workspace, arg string) (service, method string, kind methodKind, ok bool) {
	service, method = splitMethodPath(arg)
	if service == "" || method == "" {
		return "", "", methodKind{}, false
	}
	kind, ok = lookupMethod(ws, service, method)
	if !ok {
		return "", "", methodKind{}, false
	}
	return service, method, kind, true
}

func splitMethodPath(arg string) (service, method string) {
	i := strings.LastIndex(arg, "/")
	if i < 0 {
		return "", ""
	}
	return arg[:i], arg[i+1:]
}

// lookupMethod finds a method's streaming kind in the merged services list.
func lookupMethod(ws *grpcviewv1.Workspace, service, method string) (methodKind, bool) {
	for _, svc := range ws.GetServices() {
		if serviceFullName(svc) != service {
			continue
		}
		for _, m := range svc.GetMethods() {
			if m.GetName() == method {
				return methodKind{client: m.GetClientStreaming(), server: m.GetServerStreaming()}, true
			}
		}
	}
	return methodKind{}, false
}

func serviceFullName(svc *grpcviewv1.Service) string {
	if svc.GetPackage() == "" {
		return svc.GetName()
	}
	return fmt.Sprintf("%s.%s", svc.GetPackage(), svc.GetName())
}
