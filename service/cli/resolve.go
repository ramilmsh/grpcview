package cli

import (
	"fmt"
	"strings"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

type methodKind struct {
	client bool
	server bool
}

// streaming is "streams at all": InvokeSaved rejects a server-streaming method with Unimplemented.
func (k methodKind) streaming() bool { return k.client || k.server }

// ndjson: only client-streaming and bidi take many messages.
func (k methodKind) ndjson() bool { return k.client }

type invokeTarget struct {
	arg      string
	saved    bool
	parent   []string
	itemName string
	service  string
	method   string
	kind     methodKind
}

type savedLookup struct {
	req      *grpcviewv1.Request
	item     *grpcviewv1.Item
	parent   []string
	name     string
	isFolder bool
}

func resolveInvokeArg(ws *grpcviewv1.Collection, arg string) (invokeTarget, error) {
	saved := lookupSaved(ws, arg)
	service, method, kind, adhoc := lookupAdhoc(ws, arg)

	switch {
	case saved.req != nil && adhoc:
		return invokeTarget{}, fmt.Errorf(
			"ambiguous argument %q: it names both the saved request %s in collection %q and the method %s/%s in the schema — nothing was invoked; rename one of the two",
			arg, arg, ws.GetName(), service, method)

	case saved.req != nil:
		kind, ok := lookupMethod(ws, saved.req.GetService(), saved.req.GetMethod())
		if !ok {
			return invokeTarget{}, fmt.Errorf(
				"cannot invoke the saved request %q: it calls %s/%s, which no definition source in collection %q resolves; refresh the source or fix the request",
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

func unknownArgError(ws *grpcviewv1.Collection, arg string, saved savedLookup) error {
	if saved.isFolder {
		return fmt.Errorf(
			"cannot invoke %q: it is a folder, not a request; invoke addresses a saved request or a <service>/<method>", arg)
	}
	if !strings.Contains(arg, "/") {
		return fmt.Errorf(
			"unknown request %q: no saved request by that name at the top level of collection %q, and a <service>/<method> argument needs a slash",
			arg, ws.GetName())
	}
	service, method := splitMethodPath(arg)
	return fmt.Errorf(
		"unknown request or method %q: no saved request at that path in collection %q, and no service %q with a method %q among its %d service(s)",
		arg, ws.GetName(), service, method, len(ws.GetServices()))
}

// lookupSaved walks the collection tree by display name from the root Item.
func lookupSaved(ws *grpcviewv1.Collection, arg string) savedLookup {
	parent, name, err := workspace.SplitInvokePath(arg)
	if err != nil {
		// SplitInvokePath's error text names gv.invoke, so it is not surfaced here.
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

func lookupAdhoc(ws *grpcviewv1.Collection, arg string) (service, method string, kind methodKind, ok bool) {
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

// splitMethodPath splits on the LAST slash: a service's full name has dots, never slashes.
func splitMethodPath(arg string) (service, method string) {
	i := strings.LastIndex(arg, "/")
	if i < 0 {
		return "", ""
	}
	return arg[:i], arg[i+1:]
}

func lookupMethod(ws *grpcviewv1.Collection, service, method string) (methodKind, bool) {
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
