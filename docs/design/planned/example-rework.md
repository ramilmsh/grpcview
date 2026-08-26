Rebuild the example/ collection so it dogfoods grpcview against itself only — every request targets grpcview.v1.WorkspaceService on the workspace server (localhost:10000), which any grpcview command starts on demand. No echo server, no second process.

  Delete //service/echo, //proto/echo, //example:up, tools/example-up.sh, the echo descriptor source, and every Echo/ request. Build the replacement only through the grpcview MCP tools (mcp__grpcview__*) — no hand-editing under example/**.

  Keep the feature coverage the README claims, re-expressed against grpcview's own API: TypeScript bodies, generators (one with the dayjs npm dep), folder-metadata inheritance, middleware, gv.request.params, gv.invoke chaining, a streaming request on InvokeStreaming, and the smoke scenario. Two source kinds still: reflection on localhost:10000 plus bazel //grpcview/v1:grpcviewv1_proto (that one carries the .proto comments reflection strips).

   Verify through MCP: invoke_saved, invoke_saved_streaming, and the smoke assertions. Rewrite example/README.md to match. Report every MCP tool that was missing, broken, or awkward — do not fix the server.
