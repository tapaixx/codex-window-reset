# Serve one self-contained panel resource

The plugin registers only `/panel`. At serve time it assembles embedded HTML,
CSS, and the ordered browser modules into one document, removes module
imports/exports, and emits a classic inline script. The browser therefore does
not request `/styles.css`, `/modules/*`, or any other plugin resource. Source
files remain separated for review and Node tests, while exact-route hosts need
only one resource and no wildcard support. Contract tests verify that the
served document contains neither external stylesheet/module references nor
module syntax, and that undeclared resource paths return 404.

This decision supersedes the earlier native-ES-module resource layout in the
initial implementation plan.
