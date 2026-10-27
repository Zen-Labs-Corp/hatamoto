# Sourced by seed.sh. People, posts, submissions, redirects, linking rules, performance, analytics.
sql "update hatamoto_entities set name='Home' where slug='home'" >/dev/null

# Fictional team: name|slug|job title|short bio|avatar.
person() { # person SLUG JOB BIO AVATAR
  local photo; photo=$(upload "$ASSETS/$4")
  sql "update hatamoto_entities set attributes = attributes || jsonb_build_object('job_title','$2','short_bio','$3','photo','$photo','public',true,'on_team',true) where slug='$1'" >/dev/null
}
person mika-lindqvist "Founder and editor" "Runs Northwind Studio and writes about calm software." avatar-1.png
person aiko-tanaka "Managing editor" "Keeps the publishing calendar honest." avatar-2.png
for row in "jonas-okafor|Jonas Okafor|Engineering lead|Builds and ships the platform.|avatar-3.png" \
           "sofia-ramos|Sofia Ramos|Designer|Shapes how the studio looks and feels.|avatar-4.png" \
           "daniel-kovac|Daniel Kovac|Developer relations|Talks to the people who build with us.|avatar-5.png" \
           "elena-voss|Elena Voss|Content strategist|Plans what the studio publishes and why.|avatar-6.png"; do
  IFS='|' read -r slug name job bio avatar <<<"$row"
  # No --publish: a person's visibility follows the Public attribute, and the CLI refuses a status.
  "$BIN" entity create --schema person --slug "$slug" --name "$name" --attributes '{"public":true,"on_team":true}' --as "$OWNER" >/dev/null || return 1
  person "$slug" "$job" "$bio" "$avatar"
done

# Posts with real copy. Fields: slug|title|excerpt|tags|author slug|html
FEATURED=$(upload "$ASSETS/featured-release.png")
mkpost() { # mkpost SLUG TITLE EXCERPT TAGS_JSON AUTHOR HTML [FEATURED]
  local attrs
  attrs=$(node -e 'const [e,t,h,f]=process.argv.slice(1);console.log(JSON.stringify({excerpt:e,tags:JSON.parse(t),content:h,...(f?{featured_image:f}:{})}))' "$3" "$4" "$6" "${7:-}")
  mk post "$1" "$2" "$attrs" >/dev/null || return 1
  sql "update hatamoto_entities set byline_person_id=(select id from hatamoto_entities where slug='$5') where slug='$1'" >/dev/null
}
mkpost shipping-a-calmer-release-process "Shipping a calmer release process" \
  "How we stopped dreading Fridays: small releases, automatic rollbacks and one boring checklist." '["releases","process"]' mika-lindqvist \
  '<p>For years our releases were an event. Someone cleared an afternoon, someone else kept a terminal open, and everyone hoped nothing would need explaining on Monday. It worked, until the week it did not.</p><h2>Make the risky part boring</h2><p>We now ship small changes several times a week. Each one is checked after it goes live, and a release that fails its smoke test is rolled back without anyone being asked.</p><p>The checklist is short on purpose: the build is green, the site answers, the contact form still sends. If any of those fail, the previous version comes back on its own.</p><p>The result is not that nothing breaks. It is that breaking something no longer costs an afternoon.</p>' "$FEATURED"
mkpost writing-for-ai-assistants "Writing pages that AI assistants can quote" "A few habits that make your pages easier to cite." '["writing","ai"]' aiko-tanaka \
  '<h2>Say it plainly</h2><p>Lead with the answer, then explain it. Assistants quote the sentence that stands on its own.</p><p>Give every page one clear subject and a title that says what it is.</p>'
mkpost what-our-crawler-logs-say "What our crawler logs say" "Which AI crawlers visit, how often, and which ones are real." '["analytics","ai"]' jonas-okafor \
  '<p>A third of the visits that call themselves a famous crawler are not. Checking the address they come from separates the real ones from the imitations.</p>'
mkpost a-small-design-system "A small design system that stays small" "Fewer tokens, fewer surprises." '["design"]' sofia-ramos \
  '<p>We keep one spacing scale, one type scale and one accent colour. Everything else is a decision someone has to defend.</p>'
mkpost office-hours-notes "Notes from our first office hours" "What people asked, and what we changed because of it." '["community"]' daniel-kovac \
  '<p>Most questions were about how to move an existing site over, so we wrote that guide first.</p>'
mkpost planning-the-quarter "Planning the quarter in one page" "A planning template we actually reuse." '["process"]' elena-voss \
  '<p>One page, three goals, one owner each. If it does not fit, it is not a plan yet.</p>'
sql "update hatamoto_entities set byline_person_id=(select id from hatamoto_entities where slug='aiko-tanaka') where slug in ('how-this-site-works','hello-world')" >/dev/null


# The post type's address settings (Settings > Entity models > Post).
sql "update hatamoto_entity_schemas set settings = settings || '{\"url_pattern\":\"/blog/{slug}\",\"archive_path\":\"/blog\",\"archive_title\":\"Blog\",\"archive_crumb_label\":\"Blog\",\"archive_description\":\"Notes on releases, writing and running a small studio.\"}'::jsonb where slug='post'" >/dev/null

# Contact form submissions: fictional senders. The public endpoint rate-limits a single
# address to five, so the rows are inserted directly, spread over the last days (oldest first).
"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
INSERT INTO hatamoto_form_submissions (form_id, data, metadata, status, created_at)
SELECT (SELECT id FROM hatamoto_forms ORDER BY created_at LIMIT 1),
       jsonb_build_object('name', n, 'email', e, 'message', m), '{}'::jsonb, st, now() - ago::interval
  FROM (VALUES
    ('Yuki Mori', 'yuki.mori@example.com', 'Thanks for the quick answer last week, everything works now.', 'read', '6 days'),
    ('Henrik Solberg', 'henrik.solberg@example.com', 'The contact form on our staging copy returns an error. Any idea?', 'read', '5 days 3 hours'),
    ('Ines Duarte', 'ines.duarte@example.com', 'We would like a demo for our agency on Thursday if you have time.', 'read', '3 days 6 hours'),
    ('Omar Haddad', 'omar.haddad@example.com', 'Is there an API for publishing from our own tools?', 'read', '2 days 2 hours'),
    ('Chloe Martin', 'chloe.martin@example.com', 'Loved the post on calmer releases. Do you have a newsletter?', 'new', '1 day 4 hours'),
    ('Tomas Berg', 'tomas.berg@example.com', 'Quick question about pricing for a team of five editors.', 'new', '9 hours'),
    ('Priya Natarajan', 'priya.natarajan@example.com', 'Hi, we are moving a 200 page site over next month. Can you help with the redirects?', 'new', '35 minutes')
  ) AS v(n, e, m, st, ago);
SQL

# Redirects with believable hit counts; one is switched off.
for row in "/old-blog|/blog|301" "/team|/about|301" "/get-in-touch|/contact|301" "/launch-2024|/blog/shipping-a-calmer-release-process|302" "/press-kit|/about|307" "/studio|/about|308"; do
  IFS='|' read -r from to code <<<"$row"
  "$BIN" redirect create --from "$from" --to "$to" --status "$code" >/dev/null
done
sql "update hatamoto_redirects set hit_count = case source_path when '/old-blog' then 1284 when '/team' then 413 when '/get-in-touch' then 266 when '/launch-2024' then 97 when '/press-kit' then 31 else 8 end, is_active = (source_path <> '/launch-2024')" >/dev/null

# Internal linking rules (phrase, target slug, priority).
rule() { # rule PHRASE TARGET_SLUG PRIORITY MAXLINKS
  local id; id=$(sql "select id from hatamoto_entities where slug='$2'")
  form /admin/settings/linking/rules "phrase=$1" "target=$id" "priority=$3" "maxlinks=$4" "active=1" >/dev/null
}
rule "release process" shipping-a-calmer-release-process 90 2
rule "AI assistants" writing-for-ai-assistants 80 1
rule "crawler logs" what-our-crawler-logs-say 80 1
rule "design system" a-small-design-system 60 1
rule "get in touch" contact 70 2
rule "about us" about 50 1

# Page cache: on, 30 days, 200 MB.
curl -fs -o /dev/null -c "$jar" -b "$jar" -H "Origin: $BASE" --data-urlencode "_csrf=$(csrf)" --data-urlencode enabled=on --data-urlencode ttl=2592000 --data-urlencode max_mb=200 --data-urlencode preload=on --data-urlencode mode=missing --data-urlencode after_changes=on --data-urlencode interval=21600 "$BASE/admin/settings/performance"

"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -f "$HS/seed.d/analytics.sql"
