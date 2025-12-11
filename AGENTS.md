# Project Overview

`grpcview` is a gRPC tool similar to Postman, leveraging the Monaco editor's JSON schema autocompletion capabilities. It converts Proto descriptors into JSON schemas to provide intelligent editing for gRPC requests.

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
  bazel run //frontend:dev
  ```
  Runs the Vite dev server for the frontend.

## Directory Structure

- `frontend/`: Vue.js frontend application.
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
