"""Lint aspects wired via aspect_rules_lint. Referenced by label from .bazelrc's
--aspects flags, so each name here is a stable, load-bearing symbol.

Each public aspect wraps a rules_lint one (`requires`) and fails the build on its findings
for main-repo targets only. Building third-party labels top-level (service/bazelbuild does,
for a proto_library closure) would otherwise lint e.g. @protobuf's protos against our
buf.yaml and fail. rules_lint 2.8.0 has no filter hook (only rule_kinds and the "no-lint"
tag), and its lint action runs as a `_validation` output on every top-level target, so
.bazelrc leaves fail_on_violation off: rules_lint then records exit codes in files, and the
wrapper's own validation action turns a non-zero one into a failure."""

load("@aspect_rules_lint//lint:buf.bzl", "lint_buf_aspect")
load("@aspect_rules_lint//lint:eslint.bzl", "lint_eslint_aspect")
load("@aspect_rules_lint//lint:shellcheck.bzl", "lint_shellcheck_aspect")
load("@multitool//:tools.bzl", "TOOLS")

_eslint = lint_eslint_aspect(
    binary = Label("//tools/lint:eslint"),
    # A js_library, not the bare source file: the aspect gathers configs through
    # js_lib_helpers.gather_files_from_js_infos, which only picks up targets with a JsInfo
    # provider, so a plain exports_files() label is silently dropped from the action's
    # inputs. Once it's copied into the bin tree, ESLint's flat-config auto-discovery walks
    # up from each linted file and finds it there — no explicit --config arg needed.
    configs = [Label("//ui:eslint_config")],
)

_shellcheck = lint_shellcheck_aspect(
    binary = TOOLS["shellcheck"],
    config = Label("//:.shellcheckrc"),
)

_buf = lint_buf_aspect(
    config = Label("//:buf.yaml"),
)

def _main_repo_only(inner, name):
    def _impl(target, ctx):
        if target.label.workspace_name or OutputGroupInfo not in target:
            return []
        groups = target[OutputGroupInfo]
        if not hasattr(groups, "rules_lint_human"):
            return []
        reports = groups.rules_lint_human.to_list()
        codes = [f for f in reports if f.basename.endswith(".exit_code")]
        if not codes:
            return []
        ok = ctx.actions.declare_file("{}.{}.lint_ok".format(target.label.name, name))
        ctx.actions.run_shell(
            inputs = reports,
            outputs = [ok],
            arguments = [ok.path] + [f.path for f in codes],
            # A report sits next to its exit code: <x>.out / <x>.out.exit_code.
            command = """ok=$1; shift; rc=0
for c in "$@"; do [ "$(cat "$c")" = 0 ] || { cat "${c%.exit_code}" >&2; rc=1; }; done
[ $rc = 0 ] && touch "$ok"; exit $rc""",
            mnemonic = "LintCheck",
            progress_message = "Checking %s lint of %%{label}" % name,
        )
        return [OutputGroupInfo(_validation = depset([ok]))]

    return aspect(implementation = _impl, requires = [inner])

eslint = _main_repo_only(_eslint, "eslint")
shellcheck = _main_repo_only(_shellcheck, "shellcheck")
buf = _main_repo_only(_buf, "buf")
