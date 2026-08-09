import { invoke } from "grpcview:invoke";
import { params } from "grpcview:request";

export default async (): Promise<RequestMessage> => (
{
  collection: "example",
  // The middleware attached to this request is a specifier, not a name:
  // `#/scripts/trace-headers.ts`. It runs after this body is evaluated, wraps
  // this expression so the scratchpad grpcview actually runs returns a prefixed
  // string, and stamps two extra headers.
  source: '"before middleware"',
}
)
