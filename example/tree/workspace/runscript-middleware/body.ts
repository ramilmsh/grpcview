export default async (): Promise<RequestMessage> => (
// grpcview:script start
{
  collection: "example",
  // The middleware attached to this request is a specifier, not a name:
  // `#/scripts/trace-headers.ts`. It runs after this body is evaluated, wraps
  // this expression so the scratchpad grpcview actually runs returns a prefixed
  // string, and stamps two extra headers.
  //
  // The middleware is configured on the request, not imported here, which is
  // why this file has no import block at all above the start marker.
  source: '"before middleware"',
  script: "f"
}
// grpcview:script end
)
