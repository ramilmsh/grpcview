"""Lint aspects wired via aspect_rules_lint. Referenced by label from .bazelrc's
--aspects flags, so each name here is a stable, load-bearing symbol."""

load("@aspect_rules_lint//lint:buf.bzl", "lint_buf_aspect")
load("@aspect_rules_lint//lint:eslint.bzl", "lint_eslint_aspect")
load("@aspect_rules_lint//lint:shellcheck.bzl", "lint_shellcheck_aspect")
load("@multitool//:tools.bzl", "TOOLS")

eslint = lint_eslint_aspect(
    binary = Label("//tools/lint:eslint"),
    # A js_library, not the bare source file: the aspect gathers configs through
    # js_lib_helpers.gather_files_from_js_infos, which only picks up targets with a JsInfo
    # provider, so a plain exports_files() label is silently dropped from the action's
    # inputs. Once it's copied into the bin tree, ESLint's flat-config auto-discovery walks
    # up from each linted file and finds it there — no explicit --config arg needed.
    configs = [Label("//ui:eslint_config")],
)

shellcheck = lint_shellcheck_aspect(
    binary = TOOLS["shellcheck"],
    config = Label("//:.shellcheckrc"),
)

buf = lint_buf_aspect(
    config = Label("//:buf.yaml"),
)
