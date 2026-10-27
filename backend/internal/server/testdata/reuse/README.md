Fixtures for the reuse integration tests (`reuse_*_integration_test.go`).

`theme_parts.json` is the manifest of the throwaway theme layer the tests put
over the default theme: it opts in to header and footer parts. A layer with no
`builder.parts` stands for a theme that did not opt in.
