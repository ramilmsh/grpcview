import { inherit } from "grpcview:metadata";

export default async (): Promise<Metadata> => (
// grpcview:script start
{
  // A nested folder carries its ancestors forward only if it spreads them.
  // Drop the spread and this folder becomes a deliberate barrier.
  //
  // `inherit` is not supplied for free: the import above the start marker is
  // what binds it, and it is there because this region calls it. A folder
  // whose headers come from another saved request imports `invoke` the same
  // way, on a line the editor writes and maintains for you.
  ...inherit(),
  "x-demo-folder": ["streaming"],
}
// grpcview:script end
)
