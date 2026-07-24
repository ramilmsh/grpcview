# Project Overview

`grpcview` is a gRPC tool similar to Postman, leveraging the Monaco editor's JSON schema autocompletion capabilities. It converts Proto descriptors into JSON schemas to provide intelligent editing for gRPC requests.

## Project Stage

**This project has no users yet — it is way pre-release.** Breaking any contract you like is perfectly fine. **SIMPLICITY is the important part; backwards compatibility is IRRELEVANT at this stage.** Don't add migrations, compatibility shims, or `reserved` proto markers to preserve old on-disk/wire data — change the schema and delete freely. Always favor the simplest change that works over the most backwards-compatible one.

## Architecture

- **Frontend**: A Vue.js application using Monaco editor.
- **Service**: A Go backend that serves the frontend and handles gRPC reflection/proxying.
- **Inspector**: A Go tool that converts Proto descriptors to JSON schemas.
- **Deployment**: The frontend is compiled to a single HTML file and embedded into the Go server binary for a standalone static binary distribution.

## Routing

- **Home**: `/` - The landing page.
- **Workspace**: `/ws/:name` - The main workspace view for a given workspace name.

## Build System (Bazel)

The project uses Bazel for building, testing, proto generation, and embedding.

### Common Commands

- **Build Release Binary**:

  ```bash
  bazel build //service/cmd
  ```

  This builds the standalone binary with the embedded frontend.

- **Run Dev Server (Backend)**:

  ```bash
  bazel run //service/cmd/dev
  ```

  Runs the backend without embedding the frontend (useful for iteration).

- **Run Dev Server (Frontend)**:

  ```bash
  bazel run //ui:dev
  ```

  Runs the Vite dev server for the frontend.

- **Regenerate TypeScript Proto Types**:
  ```bash
  bazel run @@//proto/grpcview/v1:grpcviewv1_ts_proto.copy
  ```
  Copies regenerated TypeScript types from proto definitions to the source tree. Run this after modifying `.proto` files.

> [!TIP] > **VS Code Tasks**: You can also run these servers using the configured VS Code tasks:
>
> - `Run Backend (Dev)`
> - `Run Frontend (Dev)`
> - `Run All (Dev)` (Runs both in parallel)

## Directory Structure

- `ui/`: Vue.js frontend application.
  - `src/`: Source code.
  - `BUILD.bazel`: Bazel build definition for Vite build and dev server.
- `service/`: Go backend service.
  - `cmd/`: Main entry points.
    - `main.go`: Embeds `index.html` and runs the service.
    - `dev/`: Development entry point.
  - `proto/`: Proto definitions.
  - `service.go`: Main service logic.
- `inspector/`: Logic for converting Proto descriptors to JSON schemas.
  - `inspector.go`: The core conversion logic.
