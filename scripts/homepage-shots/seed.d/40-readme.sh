# Sourced by seed.sh. README set only (SHOTS_SET=readme): an API key for a publishing
# agent, and a real MCP session with it (mcp-demo.mjs drafts and publishes a post and
# writes the transcript page for the "mcp" shot to MCP_HTML).
if [ "${SHOTS_SET:-homepage}" = readme ]; then
  : "${MCP_HTML:?set MCP_HTML (run.sh does)}"
  key=$(mktemp -u "${TMPDIR:-/tmp}/hm-shots-mcp-key.XXXXXX")
  "$BIN" apikey create --name "Publishing agent" --scopes read:entities,write:entities --out "$key" --as "$OWNER" >/dev/null 2>&1 \
    || { echo "seed: could not create the MCP key" >&2; return 1; }
  node "$HS/mcp-demo.mjs" "$key" "$MCP_HTML" || { rm -f "$key"; return 1; }
  rm -f "$key"
fi
