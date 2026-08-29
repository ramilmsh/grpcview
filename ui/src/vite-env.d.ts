// import.meta.env.PROD is injected at build time via esbuild --define (see
// ui/BUILD.bazel's release vs. dev esbuild targets) — no bundler-provided types
// to reference anymore, so it's declared by hand here.
interface ImportMetaEnv {
  readonly PROD: boolean;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
