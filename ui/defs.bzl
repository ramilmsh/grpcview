load("@aspect_rules_swc//swc:defs.bzl", "swc")
load("@aspect_rules_ts//ts:defs.bzl", _ts_project = "ts_project")
load("@bazel_skylib//lib:partial.bzl", "partial")

def ts_project(name, **kwargs):
    kwargs.setdefault("transpiler", partial.make(swc, swcrc = "//ui:.swcrc"))
    _ts_project(name = name, **kwargs)
