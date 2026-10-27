# Normalises `caddy adapt` JSON so that everything except the hatamoto.dev /
# www.hatamoto.dev sites can be compared before and after a splice.
# Usage: jq -S --arg cfg <path of the adapted file> -f deploy/caddy-others.jq
def ours: . == "hatamoto.dev" or . == "www.hatamoto.dev";
# Only a route's `match` (an array of matcher objects) names hosts; handlers such
# as handle_response carry an object-valued `match` that must be left alone.
del(.. | objects | select(has("match")) | select((.match | type) == "array")
    | select(any(.match[] | objects; any(.host[]?; ours))))
# hatamoto hosts in skip lists / subject lists / logger maps
| walk(if type == "array" then map(select((type == "string" and ours) | not))
       elif type == "object" then del(.["hatamoto.dev"], .["www.hatamoto.dev"])
       else . end)
# a server whose only error routes were ours (handle_errors) is left with an empty `errors`
| walk(if type == "object" and (.errors | type) == "object" and (.errors | length) == 1 and .errors.routes == []
       then del(.errors) else . end)
# the adapted config embeds its own path (file_server hide) ...
| walk(if . == $cfg then "<config>" else . end)
# ... and numbers `handle` groups with a counter that shifts when blocks change.
| ([.. | objects | .group? | strings] | reduce .[] as $g ([]; if index($g) then . else . + [$g] end)) as $order
| walk(if type == "object" and (.group | type) == "string"
       then .group as $g | .group = "group\($order | index($g))" else . end)
