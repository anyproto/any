#!/usr/bin/env bash
# Seed a space with curated movies / books / comics / games.
# Tarkovsky-leaning, philosophical-sci-fi-leaning. Easy to edit:
# everything lives in the four arrays at the bottom.
#
# Usage:
#   ANY_DATA_DIR=~/.any-pr15 ./any run            # in another shell
#   ./scripts/seed-mars-200.sh                    # auto-finds 'MARS 200'
#   SPACE_NAME='Mars 200' ./scripts/seed-mars-200.sh
#   SPACE_ID=spc-abc...   ./scripts/seed-mars-200.sh
#
# Idempotency: this script DOES NOT check for duplicates. Re-running
# adds another copy of every list + every object. Wipe the data dir
# (rm -rf ~/.any-pr15) and re-init if you want to start fresh.

set -euo pipefail

API="${ANY_API:-http://127.0.0.1:7001}"
SPACE_NAME="${SPACE_NAME:-Mars 2000}"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required (brew install jq)" >&2
  exit 1
fi

# ---- find the space --------------------------------------------------

if [ -z "${SPACE_ID:-}" ]; then
  SPACE_ID=$(curl -sf "$API/v1/spaces" \
    | jq -r --arg n "$SPACE_NAME" '
        .spaces[]
        | select((.name // "") | ascii_downcase | contains(($n | ascii_downcase)))
        | .id' \
    | head -1)
fi

if [ -z "${SPACE_ID:-}" ]; then
  echo "Couldn't find a space matching '$SPACE_NAME'." >&2
  echo "Set SPACE_NAME or SPACE_ID env var, or list with:" >&2
  echo "  curl -s $API/v1/spaces | jq" >&2
  exit 1
fi
echo "Using space $SPACE_ID (matched '$SPACE_NAME')"

# ---- API helpers -----------------------------------------------------

mk_type() {
  local name="$1"
  local desc="${2:-}"
  curl -sf -X POST "$API/v1/spaces/$SPACE_ID/types" \
    -H "Content-Type: application/json" \
    -d "$(jq -nc --arg n "$name" --arg d "$desc" '{name:$n, description:$d}')" \
    | jq -r '.typeId'
}

# add_prop <type_id> <name> <kind> [xKey]
add_prop() {
  local tid="$1" name="$2" kind="$3" xkey="${4:-}"
  local body
  if [ -z "$xkey" ]; then
    body=$(jq -nc --arg n "$name" --arg k "$kind" '{name:$n, kind:$k}')
  else
    body=$(jq -nc --arg n "$name" --arg k "$kind" --arg x "$xkey" '{name:$n, kind:$k, xKey:$x}')
  fi
  curl -sf -X POST "$API/v1/spaces/$SPACE_ID/types/$tid/properties" \
    -H "Content-Type: application/json" \
    -d "$body" | jq -r '.propId'
}

# mk_object <type_id> <name>  →  prints object id
mk_object() {
  local tid="$1" name="$2"
  local oid
  # Wire field is `types`, not `typeIds` — confirmed in
  # web/app/src/lib/api/objects.ts useCreateObject (the React hook
  # remaps from CreateObjectArgs.typeIds to body.types).
  oid=$(curl -sf -X POST "$API/v1/spaces/$SPACE_ID/objects" \
    -H "Content-Type: application/json" \
    -d "$(jq -nc --arg t "$tid" '{types:[$t]}')" \
    | jq -r '.objectId')
  curl -sf -X POST "$API/v1/spaces/$SPACE_ID/properties/$oid/base/any" \
    -H "Content-Type: application/json" \
    -d "$(jq -nc --arg n "$name" '{patch:{name:$n}}')" >/dev/null
  echo "$oid"
}

# set_props <object_id> <type_id> <patch_json>
set_props() {
  local oid="$1" tid="$2" patch="$3"
  curl -sf -X POST "$API/v1/spaces/$SPACE_ID/properties/$oid/base/$tid" \
    -H "Content-Type: application/json" \
    -d "$(jq -nc --argjson p "$patch" '{patch:$p}')" >/dev/null
}

# ---- build the lists --------------------------------------------------

echo "Creating list: Movies"
T_MOVIES=$(mk_type "Movies" "Films worth keeping")
P_M_DIR=$(add_prop "$T_MOVIES"  "Director" string)
P_M_YEAR=$(add_prop "$T_MOVIES" "Year"     number)
P_M_RATE=$(add_prop "$T_MOVIES" "Rating"   number)
P_M_SEEN=$(add_prop "$T_MOVIES" "Watched"  boolean)
P_M_TAGS=$(add_prop "$T_MOVIES" "Tags"     array  tags)
P_M_NOTE=$(add_prop "$T_MOVIES" "Notes"    string longtext)

echo "Creating list: Books"
T_BOOKS=$(mk_type "Books" "Reading log")
P_B_AUTH=$(add_prop "$T_BOOKS" "Author" string)
P_B_YEAR=$(add_prop "$T_BOOKS" "Year"   number)
P_B_RATE=$(add_prop "$T_BOOKS" "Rating" number)
P_B_READ=$(add_prop "$T_BOOKS" "Read"   boolean)
P_B_TAGS=$(add_prop "$T_BOOKS" "Tags"   array  tags)
P_B_NOTE=$(add_prop "$T_BOOKS" "Notes"  string longtext)

echo "Creating list: Comics"
T_COMICS=$(mk_type "Comics" "Graphic novels + manga")
P_C_AUTH=$(add_prop "$T_COMICS" "Author" string)
P_C_YEAR=$(add_prop "$T_COMICS" "Year"   number)
P_C_RATE=$(add_prop "$T_COMICS" "Rating" number)
P_C_READ=$(add_prop "$T_COMICS" "Read"   boolean)
P_C_SER=$(add_prop  "$T_COMICS" "Series" string)
P_C_TAGS=$(add_prop "$T_COMICS" "Tags"   array  tags)

echo "Creating list: Games"
T_GAMES=$(mk_type "Games" "What I played")
P_G_STUD=$(add_prop "$T_GAMES" "Studio"   string)
P_G_YEAR=$(add_prop "$T_GAMES" "Year"     number)
P_G_RATE=$(add_prop "$T_GAMES" "Rating"   number)
P_G_PLAY=$(add_prop "$T_GAMES" "Played"   boolean)
P_G_PLAT=$(add_prop "$T_GAMES" "Platform" array  tags)
P_G_NOTE=$(add_prop "$T_GAMES" "Notes"    string longtext)

echo "Seeding objects…"

# ---- helper to add a movie -------------------------------------------
add_movie() {
  local name="$1" director="$2" year="$3" rating="$4" watched="$5" tagsjson="$6" notes="$7"
  local oid; oid=$(mk_object "$T_MOVIES" "$name")
  set_props "$oid" "$T_MOVIES" "$(jq -nc \
    --arg dir "$director" --argjson y $year --argjson r $rating \
    --argjson w $watched --argjson tags "$tagsjson" --arg n "$notes" \
    --arg pdir "$P_M_DIR" --arg py "$P_M_YEAR" --arg pr "$P_M_RATE" \
    --arg pw "$P_M_SEEN" --arg pt "$P_M_TAGS" --arg pn "$P_M_NOTE" \
    '{($pdir):$dir, ($py):$y, ($pr):$r, ($pw):$w, ($pt):$tags, ($pn):$n}')"
  echo "  movie  $name"
}

add_book() {
  local name="$1" author="$2" year="$3" rating="$4" read="$5" tagsjson="$6" notes="$7"
  local oid; oid=$(mk_object "$T_BOOKS" "$name")
  set_props "$oid" "$T_BOOKS" "$(jq -nc \
    --arg a "$author" --argjson y $year --argjson r $rating \
    --argjson rd $read --argjson tags "$tagsjson" --arg n "$notes" \
    --arg pa "$P_B_AUTH" --arg py "$P_B_YEAR" --arg pr "$P_B_RATE" \
    --arg prd "$P_B_READ" --arg pt "$P_B_TAGS" --arg pn "$P_B_NOTE" \
    '{($pa):$a, ($py):$y, ($pr):$r, ($prd):$rd, ($pt):$tags, ($pn):$n}')"
  echo "  book   $name"
}

add_comic() {
  local name="$1" author="$2" year="$3" rating="$4" read="$5" series="$6" tagsjson="$7"
  local oid; oid=$(mk_object "$T_COMICS" "$name")
  set_props "$oid" "$T_COMICS" "$(jq -nc \
    --arg a "$author" --argjson y $year --argjson r $rating \
    --argjson rd $read --arg s "$series" --argjson tags "$tagsjson" \
    --arg pa "$P_C_AUTH" --arg py "$P_C_YEAR" --arg pr "$P_C_RATE" \
    --arg prd "$P_C_READ" --arg ps "$P_C_SER" --arg pt "$P_C_TAGS" \
    '{($pa):$a, ($py):$y, ($pr):$r, ($prd):$rd, ($ps):$s, ($pt):$tags}')"
  echo "  comic  $name"
}

add_game() {
  local name="$1" studio="$2" year="$3" rating="$4" played="$5" platjson="$6" notes="$7"
  local oid; oid=$(mk_object "$T_GAMES" "$name")
  set_props "$oid" "$T_GAMES" "$(jq -nc \
    --arg s "$studio" --argjson y $year --argjson r $rating \
    --argjson p $played --argjson plat "$platjson" --arg n "$notes" \
    --arg ps "$P_G_STUD" --arg py "$P_G_YEAR" --arg pr "$P_G_RATE" \
    --arg pp "$P_G_PLAY" --arg ppl "$P_G_PLAT" --arg pn "$P_G_NOTE" \
    '{($ps):$s, ($py):$y, ($pr):$r, ($pp):$p, ($ppl):$plat, ($pn):$n}')"
  echo "  game   $name"
}

# ---- the data --------------------------------------------------------

# movies — Tarkovsky + slow / philosophical / sci-fi
add_movie "Stalker"               "Andrei Tarkovsky"  1979 10 true  '["sci-fi","drama","philosophy"]' "The Zone changes you. A pilgrimage on tape."
add_movie "Solaris"               "Andrei Tarkovsky"  1972 10 true  '["sci-fi","drama"]'              "Memory is the most painful planet."
add_movie "The Mirror"            "Andrei Tarkovsky"  1975  9 true  '["drama","autobiographical"]'    "Memory in fragments. Wind through grass."
add_movie "Andrei Rublev"         "Andrei Tarkovsky"  1966  9 true  '["drama","history"]'             "Faith and silence. The bell."
add_movie "Nostalghia"            "Andrei Tarkovsky"  1983  9 true  '["drama","exile"]'               "A candle carried across an empty pool."
add_movie "The Sacrifice"         "Andrei Tarkovsky"  1986  9 true  '["drama","apocalypse"]'          "The tree planted at the start of the end."
add_movie "2001: A Space Odyssey" "Stanley Kubrick"   1968 10 true  '["sci-fi"]'                      "The monolith. Strauss."
add_movie "Blade Runner 2049"     "Denis Villeneuve"  2017  9 true  '["sci-fi","neo-noir"]'           "Memory, identity, rain."
add_movie "Annihilation"          "Alex Garland"      2018  8 true  '["sci-fi","horror"]'             "The Shimmer rewrites you."
add_movie "Arrival"               "Denis Villeneuve"  2016  9 true  '["sci-fi","drama"]'              "Time as a circle, language as time."
add_movie "Dune"                  "Denis Villeneuve"  2021  9 true  '["sci-fi","epic"]'               "He who controls the spice."
add_movie "The Tree of Life"      "Terrence Malick"   2011  8 true  '["drama","philosophy"]'          "Cosmos and grace."
add_movie "Sátántangó"            "Béla Tarr"         1994  9 false '["drama","slow-cinema"]'         "Seven hours of mud and consequence."
add_movie "Werckmeister Harmonies" "Béla Tarr"        2000  9 true  '["drama","slow-cinema"]'         "The whale arrives."

# books — Dune-adjacent + philosophical sci-fi + literary heavyweights
add_book "Dune"                          "Frank Herbert"            1965 10 true  '["sci-fi","ecology","politics"]' "Spice must flow. The bene gesserit do not have a sense of humour."
add_book "Solaris"                       "Stanisław Lem"            1961  9 true  '["sci-fi","philosophy"]'         "The ocean is the conversation."
add_book "The Left Hand of Darkness"     "Ursula K. Le Guin"        1969  9 true  '["sci-fi","anthropology"]'       "Gender as season."
add_book "Roadside Picnic"               "Arkady & Boris Strugatsky" 1972 10 true '["sci-fi","soviet"]'             "Source material for Stalker. Less hopeful."
add_book "Hyperion"                      "Dan Simmons"              1989  9 true  '["sci-fi","space-opera"]'        "The Shrike awaits at the Time Tombs."
add_book "Anathem"                       "Neal Stephenson"          2008  8 false '["sci-fi","philosophy"]'         "Mathic monks. Multiverse via Plato."
add_book "The Master and Margarita"      "Mikhail Bulgakov"         1967  9 true  '["russian","satire","magical-realism"]' "The cat had a pistol."
add_book "Blood Meridian"                "Cormac McCarthy"          1985 10 true  '["western","gnostic"]'           "Judge Holden dances. The kid is silent."
add_book "Children of Time"              "Adrian Tchaikovsky"       2015  9 true  '["sci-fi","evolution"]'          "Spider civilization, terraformed."
add_book "House of Leaves"               "Mark Z. Danielewski"      2000  8 false '["horror","ergodic"]'            "The house is bigger inside."
add_book "Foundation"                    "Isaac Asimov"             1951  8 true  '["sci-fi","politics"]'           "Hari Seldon's plan, episode one."
add_book "We"                            "Yevgeny Zamyatin"         1924  9 true  '["sci-fi","dystopia"]'           "The original cube of glass society."

# comics
add_comic "The Incal"                "Jodorowsky / Moebius"            1980 10 true  "The Incal"          '["sci-fi","mystic","art"]'
add_comic "Akira"                    "Katsuhiro Otomo"                 1982 10 true  "Akira"              '["sci-fi","manga","epic"]'
add_comic "Watchmen"                 "Alan Moore / Dave Gibbons"       1986 10 true  "one-shot"           '["superhero","deconstruction"]'
add_comic "The Sandman"              "Neil Gaiman"                     1989  9 true  "The Sandman"        '["dark-fantasy","mythology"]'
add_comic "Transmetropolitan"        "Warren Ellis / Darick Robertson" 1997  8 true  "Transmetropolitan"  '["cyberpunk","satire"]'
add_comic "Saga"                     "Brian K. Vaughan / Fiona Staples" 2012 9 false "Saga"               '["space-opera","family"]'
add_comic "Promethea"                "Alan Moore / J.H. Williams III"  1999  9 false "Promethea"          '["esoteric","mythology"]'
add_comic "Berserk"                  "Kentaro Miura"                   1989 10 true  "Berserk"            '["dark-fantasy","manga"]'

# games — slow / weird / contemplative
add_game "Death Stranding"  "Kojima Productions"  2019  9 true  '["PC","PS4"]'      "Strands across the void."
add_game "Disco Elysium"    "ZA/UM"               2019 10 true  '["PC"]'            "Thoughts as combat. The thought cabinet."
add_game "Outer Wilds"      "Mobius Digital"      2019 10 true  '["PC"]'            "Twenty-two minute loop. Knowledge as the only persistence."
add_game "NieR: Automata"   "Platinum Games"      2017  9 true  '["PC","PS4"]'      "Glory to mankind. Endings A through E."
add_game "Pathologic 2"     "Ice-Pick Lodge"      2019  8 false '["PC"]'            "Time will eat you."
add_game "Kentucky Route Zero" "Cardboard Computer" 2013 9 true '["PC"]'            "Magical realism roadtrip."
add_game "SOMA"             "Frictional Games"    2015  9 true  '["PC"]'            "What is a person? Coin flip."
add_game "Bloodborne"       "FromSoftware"        2015 10 true  '["PS4"]'           "Hunt the night."
add_game "Tunic"            "Andrew Shouldice"    2022  9 true  '["PC","Switch"]'   "Fox + manual. Read it."
add_game "Hollow Knight"    "Team Cherry"         2017 10 true  '["PC","Switch"]'   "Hallownest. Pale King's regret."
add_game "Inscryption"      "Daniel Mullins"      2021  9 true  '["PC"]'            "Card game as the wrong question."
add_game "Cocoon"           "Geometric Interactive" 2023 9 true '["PC","Switch"]'   "Worlds inside marbles."

echo
echo "Done. Open the app, switch to space '$SPACE_NAME', expand Lists."
