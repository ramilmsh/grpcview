# Design

This repository is similar to it's sibling tools in general ideas, but it's made a few departures from the general mold. This document outlines some pivotal design choices

## Glossary

- `Workspace` - is the root working unit. It's similar to bazel/go/pnpm workspace, it may contain multiple collections. When grpcview is launched it finds the root of the workspace and starts from there. See detection logic for specifics
- `Collection` - is the central working unit. It's the level that contains the descriptors, requests, environments, scripts and so on
- `Folder` - is a set of requests and folders. It's mostly cosmetic and exists for the purposes of organizing requests. It only has 1 functional purpose - it allows configuring commons parts of a request, such as middleware
- `Request` - is a, well, a request. A description of how to build a request to an API
- `Descriptor` - is a protobuf descriptor, a representation of the existing messages/services/methods

## Releases

This tools is shipped as a single binary, with all the components (quickjs engine, react ui, mcp client and cli) all bundled as one single distributable artifact (similarly to grpcui). The reason for this simplicity (ironic considering other choices, i realize, but i promise it all makes sense in my own head, see Simplicity section)

## Storage

The storage is a file tree, containing components mentioned in the glossary. It is designed to be human readable, and shareable via normal git. Eventually, I might opt to use opencollection format, if it proves compatible

## Scripting

Unlike in most systems, we do not allow dynamic elements via a `{{ foo() }}`. Instead, everything is a script. Body is a script, metadata is a script. In order to make it more ergonomic, we have a wrapper that allows you to exploit the any valid JSON is valid Typescript. You can simply paste typescript and we will wrap it in boilerplate for you. But if you want, you can write any arbitrary typescript, so long at it does `export default` of a function producing the body

### Built-ins

#### invoke(...)

Invoke allows you to call any request in a collection, and use it's results as input for the next function

#### params

[finish writing]
