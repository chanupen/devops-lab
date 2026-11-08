# TUI preview provenance

The README image is a monochrome SVG transcription of
[`internal/tui/testdata/home_up.golden`](../internal/tui/testdata/home_up.golden).
It preserves the fixture's 30 rows and text at 100 terminal columns.
It is not a live cluster screenshot. The fixture deliberately mixes healthy,
progressing and failed apps, and pins the sample clock and state.
Its configuration path and SSH host are examples.

[`TestGolden`](../internal/tui/golden_test.go) renders the actual Bubble Tea
model with that sample state. It removes ANSI styling before comparing all
30 rows with the checked-in fixture. The preview only adds a background and
monospace text; it does not invent another UI layout or recolour states.
The source fixture was not rewritten for the README.

Validate the original renderer and cached-state startup gate without any
cluster, model or drill operation:

```sh
go test ./internal/tui -run 'TestGolden/home_up$|TestFirstFrame' -v
```

If the renderer changes, review the golden diff first. The existing
`go test ./internal/tui -run Golden -update` mechanism updates test fixtures;
it is not a live screenshot command. Keep the SVG text in step with the
reviewed fixture, including column spacing. Do not make a failed sample look
healthy just for a screenshot.

## Optional real terminal capture

A real screenshot or GIF needs a terminal. It was not captured for this PR.
On an already-configured Linux host, with no active drill:

```sh
lab status
lab drills status
lab
```

Use a 100-column, 30-row terminal. Select Home with `1`. Capture the whole
terminal content, including header, tabs, both panes, operations strip and
footer. Hide unrelated desktop windows; inspect paths and host names for
private information. Quit with `q`. Starting a drill or changing stacks is
not needed for the capture.

A live demo that includes a drill requires separate approval and verified
restoration. No live lab, cloud resource, drill or inference was touched to
produce the golden preview.
