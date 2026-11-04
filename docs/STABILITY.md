# Stability and versioning

logi follows [semantic versioning](https://semver.org/) from 1.0.0: patch releases fix bugs, minor releases add features without breaking anything below, and only a major release may remove or change what is listed here.

## What is covered

| Surface | Promise in every 1.x release |
|---|---|
| **Command-line options** | Names, short forms and meanings of the options shown by `logi --help` keep working. New options may be added. |
| **Positional input** | A file, a directory, several files in one directory, `-` and piped standard input. |
| **Exit codes** | `0` success (including `--help`, `--version`, `--list-parsers`); `1` a runtime failure (a file or session could not be opened, the interface failed); `2` a usage or configuration mistake. |
| **Configuration file** | The keys documented in `docs/config.example.yaml`, and the precedence flags > environment > file > defaults. Unknown keys are rejected so typos are not silent. |
| **Environment variables** | `LOGI_CONFIG`, `LOGI_FOLLOW`, `LOGI_PARSER`, `LOGI_COLOR`, `LOGI_BUFFER`, `LOGI_DISABLE_PLUGINS`, and `NO_COLOR`. |
| **Filter language** | The terms documented in the README (`severity:`, `source:`, `message:`, `since:`, `until:`, `key=value`, `re:`, quoted phrases, `-negation`) keep their meaning. New terms may be added. |
| **Parser profile names** | The names printed by `--list-parsers` (for example `generic`, `json`, `cri`, `ncache`) are valid values of `--parser`. |
| **Default key bindings** | Existing default bindings keep their actions; new ones may be added. Rebinding stays available. |

## What is not covered

- The exact appearance of the interface: layout, colours and glyphs, column widths, and wording of messages and help.
- Error-message text. Match on the exit code, not the words.
- Performance figures (they depend on the machine) and the memory estimates in the documentation.
- Everything under `internal/`. logi is a program, not a library; its Go packages are not an API.
- The `logigen` workload generator.

## Deprecation

A feature is deprecated in a minor release (documented in `CHANGELOG.md`, and reported when used where practical) and removed no earlier than the next major release.

## Behaviour that may become smarter

Detection heuristics (which log format a file is, whether piped input carries compose or `kubectl` prefixes, where a multi-line record starts) may improve in minor releases. They are conservative by design: an input that is not recognised is shown as plain text, never altered.
