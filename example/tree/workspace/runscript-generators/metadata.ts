import { inherit } from "grpcview:metadata";
import { requestId } from "#/scripts/ids";
import { stamp } from "#/scripts/stamp";

export default async (): Promise<Metadata> => (
{
  // Metadata is the same language as the body, evaluated to a
  // { [key: string]: string[] } map. Spread the folder chain first, then add
  // the per-request headers — dropping the spread would discard them.
  ...inherit(),
  "x-request-id": [requestId()],
  "x-sent-at": [stamp()],
}
)
