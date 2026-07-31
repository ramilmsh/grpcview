# Tree spike: reusing monaco's internal tree widget — findings

Status: **spike complete, working, browser-verified.** monaco-editor's internal
`CompressibleObjectTree` (the same class VS Code's own Explorer sits on top of) runs
inside grpcview today, re-skinned to Nocturne, driving ~300 fake folders/requests
with virtual scrolling, folder-chain compression, typeahead, multi-select, and (after
one in-browser-discovered fix) drag-and-drop. It costs **~12 KB** of the release
bundle, not the ~150 KB of source the tree/list widgets themselves total — confirming
the "already bundled" premise.

**Recommendation: lean NO for production, despite the good result.** Not because it
doesn't work — it does, and the feel is legitimately good — but because getting it
working required three undocumented-internals workarounds and hit one hard dead end
in a single afternoon (details below), against a project whose own stated ethos
(`AGENTS.md`) is "SIMPLICITY is the important part." `@headless-tree/react` gets you
the same headline wins (virtualization, keyboard nav, multi-select) as a small,
public, semver'd dependency instead of an unversioned reach into monaco's package
internals. See "Recommendation" at the bottom for the full trade-off — this is a
judgment call, not a slam dunk either way, and the user should form their own view
after clicking around the live spike.

## Reach it

`bazel run //ui:dev` + `bazel run //service/cmd/dev`, then the flask icon (bottom of
the left Rail, labeled "Tree spike (throwaway — monaco tree widget)"). Everything
under it is fake, hardcoded data — nothing reads the real workspace.

## What was built

```
ui/src/features/tree-spike/
  monaco-tree.d.ts          284 lines — hand-written ambient module declarations
  fake-data.ts              180 lines — ~300-node fake collection + the deep chain
  treeRenderer.ts           256 lines — delegate + (compressible) renderer + dnd
  MonacoCollectionTree.tsx  228 lines — the React wrapper (construct-once effect)
  TreeSpikeView.tsx         183 lines — toolbar of toggles + live behavior legend
  tree-spike.css             39 lines — row content styling
```

Plus three tiny, additive edits to wire it into the shell: `ui-store.ts` (`ActiveView`
gains `"tree-spike"`), `Rail.tsx` (one nav entry), `App.tsx` (one switch case). Nothing
under `features/workspace/` was touched — `git status` confirms `TreeView.tsx` and
`CollectionPanel.tsx` are unmodified. Deleting `features/tree-spike/` and reverting
those three edits removes the spike cleanly in one commit.

## Bundle-size delta (measured, not estimated)

`bazel build //ui:ui` before vs. after, same machine, same cache state:

| | raw bytes | gzip -9 |
| --- | ---: | ---: |
| baseline (trunk) | 26,844,035 | 7,182,016 |
| with tree spike | 26,856,437 | 7,187,260 |
| **delta** | **+12,402 (~12.1 KiB)** | **+5,244 (~5.1 KiB)** |

This confirms the established-fact premise: `abstractTree.js` (95 KB) + `listWidget.js`
(64 KB) + `listView.js` (50 KB) + friends are **already** in the bundle today, pulled
in transitively via `editor.main.js → edcore.main.js → editor.all.js →
contrib/gotoSymbol/browser/goToCommands.js → peek/referencesWidget.js →
WorkbenchAsyncDataTree`. The measured delta is essentially just this spike's *own*
new code (and whatever Rollup was previously tree-shaking out of `objectTree.js`
because nothing referenced `ObjectTree`/`CompressibleObjectTree`/`unthemedListStyles`
by name yet).

## API friction — the kind the brief anticipated, confirmed by reading the `.js`

All of this was read directly out of the shipped files (versions/paths above), not
recalled:

- **Constructor order** (`objectTree.js`): `new CompressibleObjectTree(user, container,
  delegate, renderers[], options)`. `user` is just a debug label string.
- **Delegate operates on the raw element**, not a tree node — `abstractTree.js`'s
  `ComposedTreeDelegate` unwraps `node.element` before calling through, so
  `IListVirtualDelegate<T>.getHeight/getTemplateId` take `T`, while the renderer's
  `renderElement`/`renderCompressedElements` take the wrapped `ITreeNode<T>` (`.element`,
  `.depth`, `.collapsed`, `.children`, …).
- **`renderTemplate(container)` receives `.monaco-tl-contents` already**, not a bare
  div — `TreeRenderer.renderTemplate` (abstractTree.js) builds
  `.monaco-tl-row > .monaco-tl-indent, .monaco-tl-twistie, .monaco-tl-contents` itself
  and only hands the last one to your renderer. No wrapper element needed on our side.
- **`setChildren(null, elements)`** populates the whole tree; each element is
  `{element, children?, collapsible?, collapsed?}` (`ICompressedTreeElement` adds
  `incompressible?`). `ObjectTreeModel._setChildren` (objectTreeModel.js) preserves
  collapse state across re-supplies (`preserveCollapseState`), which is what makes
  re-running `setChildren` after a drag-and-drop move not collapse everything.
- **`layout(height, width)` is mandatory** — the widget has no ResizeObserver of its
  own; the `MonacoCollectionTree.tsx` wrapper owns one.
- **`tree.style(styles)` is mandatory** — `unthemedListStyles` (listWidget.js) ships
  with every color `undefined`; skipping the call renders structurally-correct but
  functionally invisible/unstyled rows (no hover, no selection, no indent guides).
- **`identityProvider`/`keyboardNavigationLabelProvider`/`accessibilityProvider` are
  each individually optional** but gate real functionality: no
  `keyboardNavigationLabelProvider` → no typeahead *and* no find-filter fuzzy scoring;
  no `identityProvider` → focus/selection/expansion don't survive a `setChildren`
  re-supply by identity (falls back to view-index heuristics).

## Surprises — friction the brief did *not* anticipate

These are the actual findings of the spike; each was confirmed by reading source, not
guessed, and two were only caught by driving the real browser.

**1. The find/filter widget is not gated behind a missing service — it's missing
code.** `abstractTree.js`'s `FindController` class defines `mode`/`matchType`
setters, `render()`, `shouldAllowFocus()`, `layout()`, `dispose()` — every method that
*reads* `this.widget` — but **no method ever assigns `this.widget`** (`grep -n widget
abstractTree.js` returns zero assignments). Fetching the current upstream VS Code
source confirms `FindController.open()`/`close()` exist there and are exactly the
methods that construct/destroy the `FindWidget` — they simply aren't in this
monaco-editor build. There is also no keybinding to trigger it even if it existed
(that's workbench-layer command registration, absent here). Passing a
`contextViewProvider` would not help — the code path that would consume it is gone.
**Conclusion: don't build the floating type-to-search overlay; it cannot be reached
through any supported entry point in this package.**

**2. Typeahead is a *different*, fully-functional mechanism — good news.**
`list/listWidget.js`'s `TypeNavigationController` (constructed whenever
`keyboardNavigationLabelProvider` is set, gated only by `typeNavigationEnabled ?? true`)
implements "start typing, focus jumps to the next label match" entirely independently
of `FindController`/`FindWidget`. It needs no `contextViewProvider` and has no
find-widget dependency. Browser-verified: focus a row, type `Ses`, focus jumps to
`Session`.

**3. Home/End are not wired anywhere in this build.** `listWidget.js`'s
`KeyboardController` switches on `Enter | UpArrow | DownArrow | PageUp | PageDown |
Escape | Ctrl/Cmd+A` only — Home/End are simply absent from the switch. This is a
real functional gap versus typical file-explorer expectations (confirmed absent, not
merely untested) — cheap to patch in userland (bind our own keydown → `tree.reveal`/
`tree.setFocus` at index 0 / length−1) but not free.

**4. Drag-and-drop crashes without a `.monaco-workbench` ancestor — found and fixed
in-browser.** A real (not synthetic) HTML5 `dragstart` on a row threw:
```
HierarchyRequestError: Failed to execute 'appendChild' on 'Node': Only one element on document allowed.
    at _ListView.onDragStart
```
Root cause, read directly out of `listView.js`'s `onDragStart`:
```js
const getDragImageContainer = (e) => {
    while (e && !e.classList.contains('monaco-workbench')) { e = e.parentElement; }
    return e || this.domNode.ownerDocument;
};
const container = getDragImageContainer(this.domNode);
container.appendChild(dragImage);
```
It walks up looking for VS Code's own top-level shell class; finding none in a page
that isn't the real workbench, it falls back to `this.domNode.ownerDocument` — i.e.
`document.appendChild(...)`, which a `Document` node rejects (it already has one
child, `<html>`). **Fix:** give the tree's container the marker class
`monaco-workbench` (`MonacoCollectionTree.tsx`) — purely a string match for this one
fallback, not real workbench chrome. The handful of `.monaco-workbench …` rules
`tree.css`/`list.css` otherwise carry are inert for the features this spike uses
(sticky scroll, the type filter widget) except one harmless indent-guide CSS
transition. This is exactly the class of risk that comes with an undocumented,
never-tested-outside-VS-Code API surface — there is no changelog entry warning you
about it.

**5. `onDragOver`/`drop`'s `data.elements` can be `undefined`, not just empty —
also found in-browser.** A real dragover fired before
`abstractTree.js`'s `asTreeDragAndDropData` had normalized the payload, throwing
`TypeError: Cannot read properties of undefined (reading '0')` at `data.elements[0]`.
Fixed with `data.elements?.[0]` (`treeRenderer.ts`). Static reading of the type
surface would not have caught this — it only showed up once a real `dragstart` fired.

**6. React StrictMode's double-mount surfaces a benign but scary console
exception.** Rapid mount→unmount→remount (StrictMode's dev-only behavior;
`main.tsx` wraps the app in `<StrictMode>`) can dispose a `CompressibleObjectTree`
while its internal `activeNodesDebounce` `Delayer(0)` (constructed inside
`AbstractTree`'s own constructor, for indent-guide active-node highlighting) has a
pending promise. `Delayer.cancel()` (`common/async.js`) rejects that promise with a
`CancellationError` and nothing downstream attaches a rejection handler, so it
surfaces as an unhandled-rejection `EXCEPTION: Canceled: Canceled` in devtools. This
is internal to `AbstractTree` — nothing our wrapper does triggers or can suppress it
short of patching monaco. It is dev-only noise (production never double-mounts) but
would alarm anyone debugging real errors during development.

**7. Automated (CDP-synthesized) mouse drag could not be confirmed to complete a
real drop.** After fix #4, a synthetic drag no longer throws (`dragstart`/`dragover`
fire clean), but the row never visibly moved and no "moved" log line appeared across
several attempts/targets. This reads as a limitation of simulating native HTML5 DnD
via synthetic mouse events (a well-known automation gap — Chrome's real drag gesture
recognition doesn't always complete from `Input.dispatchMouseEvent`-style sequences),
not necessarily a remaining bug in the `ITreeDragAndDrop` wiring, which is otherwise
confirmed correct by inspection. **The user should verify drag-and-drop with a real
mouse** before trusting it either way.

## Behavior matrix (all verified in-browser this pass, Chrome, via the live spike)

| Behavior | Status | Notes |
| --- | --- | --- |
| Virtual scroll over ~300 rows | Works (by construction) | `listView.js` windows to visible range + buffer; not separately FPS-profiled this pass |
| Expand/collapse + twisties | Works | Native chevron via Codicon font, already bundled (`editor.all.js` imports `codiconStyles.js`) |
| ↑ / ↓, PgUp / PgDn | Works | List-level `KeyboardController` |
| ← / → collapse/expand (or jump to parent/first child) | Works | Tree-level `onLeftArrow`/`onRightArrow` |
| Space / ⌥Space (recursive) | Works | Tree-level `onSpace` |
| Typeahead | Works | Separate from the find widget — see surprise #2 |
| Shift-click range-select, Cmd/Ctrl-click toggle | Works | List's default `isSelectionRangeChangeEvent`/`isSelectionSingleChangeEvent` |
| Ctrl/Cmd+A select all, Escape clear | Works | `KeyboardController` |
| Double-click / Enter "opens" a request | Works, but hand-wired | No `onDidOpen` API in this build — synthesized from `onMouseDblClick` + raw `onKeyDown` |
| Folder-chain compression, live toggle | Works | `updateOptions({compressionEnabled})` flips it with no rebuild; verified both directions, including the `com/example/api/v1/internal/deprecated/legacy` chain folding to one row |
| Expand all / collapse all | Works | No bulk API — loop `tree.expand(root, true)` per top-level root |
| Home / End | **Missing** | Confirmed absent from source (surprise #3) |
| Find/filter widget | **Non-functional** | `FindController.open()/close()` absent from this build (surprise #1); a plain filter-box-driven re-`setChildren` (CollectionPanel.tsx's existing pattern) is unaffected and still works fine as an alternative |
| Drag-and-drop | Contract correct, 2 crashes fixed, drop unconfirmed by automation | Surprises #4, #5, #7 — verify with a real mouse |

## Does `tree.css`/`list.css` collide with Nocturne (`app-tokens.css`)?

No. Both files are almost entirely **structural** CSS — flex layout, absolute
positioning for the row pool, twistie sizing/rotation, indent-guide geometry — with
**zero hardcoded colors**. All actual color comes from the runtime `tree.style(...)`
call, which just template-interpolates whatever you pass into a generated
`<style>` scoped to that tree's `domId` (`abstractTree.js`'s `style()` method).
`MonacoCollectionTree.tsx` passes Nocturne's own CSS custom properties as those
values (e.g. `listActiveSelectionBackground: "var(--color-accent-900)"`) rather than
hex, so the tree re-tints automatically if the palette ever changes — confirmed
correct in-browser (selection/hover/focus all read as on-brand Nocturne, not vscode
defaults). The one class name collision risk that exists — `.monaco-workbench` (added
per surprise #4) — is namespaced enough (`monaco-*`) that it doesn't touch anything of
ours; the existing `.treerow`/`.rowmeta`/`.rowbtns` classes were deliberately **not**
reused for row backgrounds (see `tree-spike.css`'s header comment) so the native
monaco selection/hover styling being tested wouldn't be muddied by a second,
competing background source. `.mtag`/`.mt-*` (the method-kind badge) *was* reused
directly — a generic, tree-agnostic atom — and reads identically to the existing
tree.

A handful of rules reference `--vscode-*` custom properties we don't define
(sticky-scroll background, the find-widget border) — harmless no-ops since this spike
uses neither feature; a production integration wanting sticky scroll would need to
either define those or pass explicit values the same way `tree.style()` already does
for everything else.

## How much code would a production version take?

The current hand-rolled implementation (`TreeView.tsx` + `CollectionPanel.tsx`) is
**431 lines** and already has real data binding, inline rename, the folder-metadata
gear, delete-confirm, and new-folder/new-request dialogs — but no virtualization, no
compression, single-select only, no typeahead, no drag-and-drop.

This spike is **1,170 lines**, but a large fraction is throwaway or spike-only:
`fake-data.ts` (180 lines) disappears entirely; `monaco-tree.d.ts` (284 lines) is a
one-time fixed cost that wouldn't grow much with feature usage;
`TreeSpikeView.tsx`'s toolbar/legend (183 lines) is almost entirely spike
scaffolding, though a production header still needs *something* here (a filter box,
new-folder/new-request buttons — comparable to what `CollectionPanel.tsx`'s own
non-tree-rendering chrome already costs today).

The part that would genuinely **grow** relative to this spike is `treeRenderer.ts`:
this spike's rows are read-only (icon-or-tag + name + count). Matching
`TreeView.tsx`'s actual feature set — inline rename, the hover-reveal gear/pencil/
plus/trash buttons — means wiring real DOM event listeners per pooled row template
(`renderTemplate`/`disposeTemplate`), by hand, outside React, since the row pool is
reused across data changes. **This spike does not attempt that piece**, and it is
very likely the single largest remaining cost and risk in this whole approach — call
it a genuinely open question, not a solved one.

Rough total estimate for full feature parity: ~280 (types) + ~400 (renderer with real
interactivity) + ~280 (wrapper with real data binding) + ~180 (header/dialogs) + ~50
(CSS) ≈ **1,150–1,250 lines** — roughly **2.7–2.9× today's 431 lines** — in exchange
for virtualization, compression, typeahead, real multi-select, and (with the two
fixes above) drag-and-drop, none of which the current tree has.

## Recommendation, weighed against `@headless-tree/react`

Both options can deliver the headline capability (virtualization + keyboard nav +
multi-select); they differ in what you pay and what you're exposed to:

| | monaco's internal tree | `@headless-tree/react` |
| --- | --- | --- |
| Marginal bundle cost | ~12 KB (measured) — effectively free, already bundled | New dependency: `BUILD_DEPS` + lockfile update (small package, but a real new coupling) |
| API contract | **None.** `esm/vs/base/browser/ui/**` is monaco-editor's internal file layout, not its published `monaco.d.ts` surface — no semver guarantee it still exists, or behaves the same, on the next `^0.52.2` → `^0.53.0` bump | A real public, documented, versioned npm API |
| Rendering / CSS | Ships real (if partly broken) rendering, needs re-skinning via `tree.style()` | Fully headless — you own 100% of the DOM/CSS, same as `TreeView.tsx` today |
| Demonstrated risk, this pass | 1 hard dead end (find widget) + 1 real gap (Home/End) + 2 real crashes (fixed) + 1 dev-only cosmetic exception, **all discovered in a single afternoon of spiking** | Unknown — not spiked — but low by construction: a documented library doesn't have "read the minified source to find the workaround" as its normal operating mode |
| Fit for grpcview's actual scale | Virtualization solves a problem (thousands of rows) grpcview's real collections mostly don't have yet | Same — likely equally over-provisioned, but cheaply so |

**Recommendation: lean NO.** The spike proves the *feel* is good and the *bytes* are
free, which is exactly what it was built to test, and both are genuinely true. But
"free bytes" is not the same as "free maintenance": this pass alone needed a
hand-written 284-line ambient `.d.ts` (because there is no type contract at all), hit
a real crash whose fix depends on a fallback-walk implementation detail
(`getDragImageContainer`'s `.monaco-workbench` check) that could be refactored away
in any future monaco-editor release with no notice, and concluded that one whole
requested behavior (the find widget) is architecturally unreachable in the shipped
package. Against `AGENTS.md`'s explicit "SIMPLICITY is the important part" — and
given grpcview's collections are realistically dozens-to-low-hundreds of requests,
not the tens-of-thousands VS Code's Explorer is built for — taking on an undocumented,
unversioned internal-API dependency for headline features a small, public, headless
library can also provide seems like the wrong trade for this project specifically,
despite this spike's technical success. This is a judgment call, though, and the
whole point of building it was so the user could click around and weigh in with their
own read on the feel before this gets decided either way.
