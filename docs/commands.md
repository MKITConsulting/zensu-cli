# Command contract

[`commands.json`](commands.json) describes the command tree of this CLI: every
command path with its aliases, positional arguments and flags. Documentation
checks resolve each documented `zensu …` line against it. zensu-monorepo vendors
the file of a released tag for its public-docs check.

The file is generated; do not edit it by hand. After changing commands or flags,
regenerate it from the repository root and commit it with the change:

```sh
make commands        # same as: go run ./internal/tools/cmdtree
```

`go test ./internal/tools/cmdtree` fails while the committed file does not match
the command tree.

## Format (schema 1)

| Field | Meaning |
|-------|---------|
| `schemaVersion` | `1`. It increases when a field is removed or changes meaning; new fields can appear without an increase. |
| `generator` | The command that generates the file. |
| `root` | Path of the root command: `zensu`. |
| `commands` | Every command, keyed by its full path, such as `zensu features list`. |

Each command:

| Field | Meaning |
|-------|---------|
| `use` | Usage of the command: its name and argument notation, such as `get <id>`. |
| `aliases` | Other names for the last word of the path. |
| `subcommands` | Names of the direct subcommands; each has its own entry under `<path> <name>`. |
| `runnable` | `false` for a command group, which only prints its help. |
| `hidden` | The command is not listed in help. |
| `deprecated` | The command prints a deprecation notice when used. |
| `args` | `min` and `max` positional arguments; `max` is `null` when there is no upper bound. A command that is not runnable takes none. |
| `flags` | Every flag the command accepts, keyed by long name, flags inherited from parent commands included. |

Each flag:

| Field | Meaning |
|-------|---------|
| `shorthand` | Single-letter form, such as `h`, or `""`. |
| `takesValue` | `true` when the flag needs a value, as `--flag value` or `--flag=value`. `false` for flags that work bare, such as booleans; they take a value only as `--flag=value`. |
| `required` | `true` when the command declares the flag required (see below). `false` does not mean optional. |
| `persistent` | Defined as persistent on this command or a parent, so every subcommand accepts it too. |
| `hidden` | The flag is not listed in help. |
| `deprecated` | The flag prints a deprecation notice when used. |

Every field is always present, and keys and lists are sorted, so regenerating
the file never produces a spurious diff. The file holds no version, commit or
build date; record the tag or commit you took it from.

The contract includes what cobra adds when the CLI runs: the `help` command,
`-h, --help` on every command and `-v, --version` on `zensu`.

## Resolving a documented line

1. Start at `commands[root]`.
2. For each following word, find the subcommand whose name or alias equals it
   and continue at `commands[path + " " + name]`. Stop at the first word that is
   not a subcommand. A flag may come before a subcommand, as in
   `zensu --api-url <url> auth status`; skip it and its value.
3. Look up every `--name` in the resolved command's `flags`, and `-x` by
   `shorthand`. When `takesValue` is `true` and no `=` is attached, the next word
   is the value.
4. A line that passes `-h` or `--help` stops here: the CLI prints help without
   checking positional arguments or required flags, so
   `zensu ai-work submit --help` is valid. Only on `zensu` itself does a leftover
   word still fail, as an unknown command.
5. The remaining words are positional arguments; their number must lie within
   `args`.
6. Every flag with `required: true` must be present.

## Required flags

A flag is `required` when cobra marks it required (`MarkFlagRequired`) or when
its usage text ends in exactly one of this repository's markers: `(required)`,
`(…, required)` or `(…; required)`. The skill-docs tests in `internal/cmd` apply
the same rule to `skills/zensu/*.md`. A flag that its command always rejects when
missing carries one of these markers; give every new flag of that kind one too.

`false` does not mean optional. A command can still reject a missing flag when it
runs, and the contract cannot see that check: a conditional requirement such as
`(required unless --blocking)`, a marker followed by more text such as
`(required; or pass as positional arg)`, and a flag whose usage names no
requirement at all, such as `--approve-ids` of `zensu ghost batch`, which the
command needs unless `--reject-ids` is given.

## Positional arguments

`args` comes from each command's own argument rule, checked with 0 to 64
arguments. A runnable command that declares no rule accepts any number of
arguments, so its `max` is `null`.
