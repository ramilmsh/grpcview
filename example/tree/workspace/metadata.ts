export default async (): Promise<Metadata> => (
// grpcview:script start
{
  // Folder metadata: written once here, inherited by every request below.
  // Everything under this folder talks to grpcview's OWN workspace server on
  // localhost:10000 — the collection's only reflection source — so no request
  // carries a target of its own.
  //
  // The region rules are the ones a request body follows: the two marker
  // comments delimit what you edit, and there is no import block above the
  // start marker because nothing here needs a binding.
  "x-demo-suite": ["workspace"],
  "x-demo-inherited-from": ["Workspace folder"],
}
// grpcview:script end
)
