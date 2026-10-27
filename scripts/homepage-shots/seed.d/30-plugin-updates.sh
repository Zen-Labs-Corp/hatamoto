# Sourced by seed.sh. The gtm package (signed by the local demo key, built by run.sh) and
# the update state. The package is uploaded in the browser by capture.mjs, and the API key
# form is only filled in, never submitted.
[ -f "${GTM_HMP:-/nonexistent}" ] || echo "seed: GTM_HMP is not set, the plugin shot cannot be captured" >&2
bash "$HS/seed.d/reassert-updates.sh"
