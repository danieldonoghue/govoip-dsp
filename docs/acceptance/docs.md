# Acceptance criteria: developer documentation

Pre-registered before any other change of WP8, as requirements §9 demands.
Changes after the first documentation commit need a written justification
in the "Changes" section at the end of this file.

The documentation covers the public packages (`resample`, `buffer`,
`echo`, `preprocess`, `agc`), the root package `govoipdsp` and the pages
in `docs/`. It changes no function behaviour.

## Parity class

None. The criteria are checks of the documentation against the code.

## Scope

- **Exported identifier:** every exported constant, variable, type,
  function and method of a public package, as `go doc -all` lists them,
  and every exported field of an exported `Config` type.
- **Example:** a function `Example...` in an `example_test.go` file,
  which `go test` runs.

## Criteria

D1. **Doc comments.** Every exported identifier has a doc comment. Every
    `Config` field states its unit, its range, its default (or that it
    has none) and its effect. Every sentinel error states when it is
    returned. Checked by a script that parses the packages with `go/doc`
    and lists identifiers without a comment, and by reading each field.

D2. **Examples.** Every exported function, type and method has at least
    one example named for it (`ExampleNew`, `ExampleT`, `ExampleT_Method`,
    or a suffixed variant), so go doc and pkg.go.dev show it beside the
    identifier. Every exported constant and variable is used in at least
    one example. Checked by a script that matches example names against
    the exported identifiers.

D3. **Checked output.** Every example has an `// Output:` comment, and
    `go test` runs and passes it. No example uses `// Unordered output:`
    or prints nothing. Checked by the script of D2 and by `make check`.

D4. **End-to-end examples.** The root package has runnable examples, each
    with checked output, of:
    1. the send chain at 20 ms frames: `echo`, then `preprocess` with the
       residual, then `agc` with the VAD decision;
    2. the receive chain: `resample` with the output sized by `OutLen`;
    3. a hand-off between a network goroutine and processing through
       `buffer.SPSC`, keeping the newest audio after a stall.
    The root package contains no code except examples.

D5. **Package docs.** Each public package doc states its purpose, the
    lifecycle (`New`, process, `Reset`), rates and frame sizes, chunking,
    buffer ownership, aliasing and retention, `Reset`, the zero value,
    concurrency, allocation, errors and how to handle them, SpeexDSP
    compatibility or extension status, and limitations. Checked against a
    list in the handoff, item by item.

D6. **Text-only go doc.** `go doc` and `go doc -all` on every public
    package and the root package read cleanly as plain text: no Markdown
    syntax that go doc does not render (backticks, `**`, Markdown links,
    tables, Mermaid), headings in go doc form (`# Heading`), lists and
    code blocks indented as go doc expects. Checked by reading the output
    of each command, and by `gofmt -l` printing nothing.

D7. **Links from go doc.** Every link from a package doc to a GitHub page
    is a full `https://github.com/danieldonoghue/govoip-dsp/blob/master/`
    URL to a file in the repository, and every `#fragment` names an
    existing heading of that file. Checked by a script that resolves each
    URL to a local file and each fragment to a GitHub heading anchor.

D8. **Upstream links.** Every link to SpeexDSP documentation or source
    was fetched and read before it was cited, and the handoff records
    each URL with its fetch result. Source links are pinned to commit
    `1b28a0f61bc31162979e1f26f3981fc3637095c8`, never a branch.

D9. **Glossary.** `docs/glossary.md` defines every term in the brief's
    list (sample, frame, frame size, sample rate, Q15 and Q-format,
    saturation, residual echo, ERLE, VAD and speech probability, gate
    level, filter length, latency, bit-exact) and every other technical
    term a package doc or `docs/` page of WP8 uses. Each package doc links
    the glossary. Checked by listing the terms each package doc uses and
    finding each in the glossary.

D10. **Mermaid diagrams.** `docs/` has diagrams of the send and receive
     pipeline, the per-frame call order on the send path, the SPSC
     hand-off with the index each side owns, buffer ownership and view
     lifetimes, and the lifecycle of a processor. Each shows one idea and
     is explained in the text beside it. Each renders in a Markdown
     preview; every diagram is listed in the handoff for that check, which
     needs a person with VS Code and is not complete until done.

D11. **Usage guide.** `docs/usage.md` stays the single source of the
     wiring recipes; new pages link to it rather than repeat it. If it
     changes, `go test ./integration -run TestUsageCodeIsTested` passes.

D12. **Accuracy.** Every behaviour a doc states is backed by the code (read)
     or by a test or example (run). A defect found in code is recorded
     for the owning package, not fixed here.

D13. **Newcomer review.** A reviewer who did not write the docs builds the
     send chain (`echo`, `preprocess`, `agc` at 20 ms frames) from the
     docs alone, without reading the source or `integration/`, and
     reports every point where they got stuck or found a doc wrong. Every
     point is fixed or answered in the handoff.

D14. **Writing rules.** The rules of `CLAUDE.md`, "Writing" and
     "Comments", hold for every changed file: no em-dashes, prose wrapped
     at 80 columns, comments of at most two lines outside teaching code
     (ADR `wp7-teaching-comments`). `make check` passes.

## Changes

None yet.
