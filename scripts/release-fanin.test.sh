#!/usr/bin/env bash
# Unit tests for the publish fan-in decisions in scripts/release-fanin.sh:
# which platforms passed, what ships, what the release notes say, who gets
# which dispatch. Pure shell over env vars and a temp artifact tree shaped like
# actions/download-artifact leaves it; `gh` is a fake that logs its args. The
# digest files are written with the same command the legs run. Run via
# `make test-scripts` (part of `make test`, so of pr-checks).
# shellcheck disable=SC2016  # single-quoted grep patterns carry literal backticks and $ on purpose
set -uo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/release-fanin.sh
. "$here/release-fanin.sh"
set +e

fail=0
check() { [ "$1" = "$2" ] || {
    echo "FAIL: $3 (got '$1' want '$2')"
    fail=1
}; }
refuse() { # <label> <cmd…> — the command must return non-zero
    local label="$1"
    shift
    "$@" >/dev/null 2>&1 && { echo "FAIL: $label (returned 0)"; fail=1; }
}
# results <version> <desktop> <smoke> <android> <ios> <publish> — every block
# states all six; plain assignments persist, command-prefix ones would not.
results() { VERSION_RESULT=$1 DESKTOP_RESULT=$2 DESKTOP_SMOKE_RESULT=$3 ANDROID_RESULT=$4 IOS_RESULT=$5 PUBLISH_RESULT=$6; }
# the leg's own digest command (sha256sum on ubuntu, shasum on the macOS legs)
shatool() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
leg_digest() { ( cd "$(dirname "$1")" && shatool "$(basename "$1")" > "$(basename "$1").sha256" ); }
digest() { shatool "$1" | cut -d' ' -f1; }

export RUN_URL=https://example.test/run/1 VERSION=v9.9.9 CHANNEL=prerelease LLAMACPP_VERSION=b0
PARTIAL='> **Partial release** — missing: %s (run https://example.test/run/1)'

# ----------------------------------------------------------------------------
# decision table
results success success success success success success
check "$(fanin_failed_platforms)" "" "all green: no failed platforms"
check "$(fanin_partial_line)" "" "all green: no partial line"
check "$(fanin_clients_to_publish | tr '\n' ,)" "anyproto/any-ui,anyproto/anytype-swift,anyproto/any-kotlin," "all green: every client published"
check "$(fanin_failed_stages)" "" "all green: no failed stages"
fanin_any_ok || { echo "FAIL: any-ok: all green"; fail=1; }

results success success success failure success success
check "$(fanin_failed_platforms)" "android" "android red: failed platforms"
# shellcheck disable=SC2059  # PARTIAL is the format on purpose
check "$(fanin_partial_line)" "$(printf "$PARTIAL" android)" "android red: partial line"
check "$(fanin_clients_to_publish | tr '\n' ,)" "anyproto/any-ui,anyproto/anytype-swift," "android red: kotlin not published"
check "$(fanin_clients_to_fail anyproto/any-ui,anyproto/anytype-swift)" "anyproto/any-kotlin" "android red: kotlin told"
check "$(fanin_failed_stages)" "android" "android red: stages"

results success success success failure failure success
# shellcheck disable=SC2059
check "$(fanin_partial_line)" "$(printf "$PARTIAL" 'android, ios')" "two red: comma-space list"

results success success failure success success success
check "$(fanin_failed_platforms)" "desktop" "smoke red: desktop counts as failed"
check "$(fanin_clients_to_publish | tr '\n' ,)" "anyproto/anytype-swift,anyproto/any-kotlin," "smoke red: any-ui not published"

results success failure skipped failure failure skipped
fanin_any_ok && { echo "FAIL: any-ok with every platform red"; fail=1; }
check "$(fanin_failed_stages)" "desktop,android,ios" "every platform red: platforms only, a skipped publish is not a stage"
check "$(fanin_clients_to_fail "" | tr '\n' ,)" "anyproto/any-ui,anyproto/anytype-swift,anyproto/any-kotlin," "every platform red: every client told"

results success success success success success failure
check "$(fanin_failed_stages)" "publish" "publish failed: stage only"
results success success success failure success failure
check "$(fanin_failed_stages)" "android,publish" "publish failed after a red leg: the platform AND the stage"

results failure skipped skipped skipped skipped skipped
check "$(fanin_failed_stages)" "version" "version failed: the one stage, no platforms"

# ----------------------------------------------------------------------------
# the artifact tree, as download-artifact lays it out; the real six tarballs
tmp="$(mktemp -d -t fanin.XXXXXX)"
art="$tmp/artifacts"
mkdir -p "$art/desktop-tarballs" "$art/android-aar" "$art/ios-xcframework"
TARBALLS="darwin-arm64 darwin-x86_64 darwin-arm64-sandbox darwin-x86_64-sandbox linux-x86_64 windows-x86_64"
for t in $TARBALLS; do echo "d-$t" > "$art/desktop-tarballs/any-v9.9.9-$t.tar.gz"; done
echo a > "$art/android-aar/any.aar"
echo i > "$art/ios-xcframework/any.xcframework.zip"
for f in "$art"/*/any*; do leg_digest "$f"; done
D_LINUX="$(digest "$art/desktop-tarballs/any-v9.9.9-linux-x86_64.tar.gz")"
D_AAR="$(digest "$art/android-aar/any.aar")"
D_XCF="$(digest "$art/ios-xcframework/any.xcframework.zip")"
reset_dist() { rm -rf "$tmp/dist"; }

# --- parity with the workflow: the names the fixture types are the names the YAML uses
wf="$here/../.github/workflows/_build-any.yml"
for n in desktop-tarballs android-aar ios-xcframework; do check "$(grep -c "^          name: $n\$" "$wf")" "1" "parity: artifact $n is uploaded by a leg"; done
for p in 'dist/any-\*\.tar\.gz\.sha256' 'dist/android/any\.aar\.sha256' 'dist/any\.xcframework\.zip\.sha256'; do
    grep -q "$p" "$wf" || { echo "FAIL: parity: a leg no longer uploads its .sha256 ($p)"; fail=1; }
done
grep -q "needs.desktop-smoke.result == 'success'" "$wf" || { echo "FAIL: parity: the publish gate no longer folds desktop-smoke into desktop"; fail=1; }
grep -q 'release-fanin.sh collect artifacts dist' "$wf" || { echo "FAIL: parity: publish no longer runs collect"; fail=1; }
grep -q 'release-fanin.sh publish-dispatch dist "\$GITHUB_OUTPUT"' "$wf" || { echo "FAIL: parity: publish no longer runs publish-dispatch dist"; fail=1; }
grep -q 'release-fanin.sh fail-dispatch' "$wf" || { echo "FAIL: parity: notify-failed no longer runs fail-dispatch"; fail=1; }

# --- complete release: everything ships, the manifest is the legs' lines, notes render from it
results success success success success success success
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null || { echo "FAIL: collect: complete tree refused"; fail=1; }
check "$(find "$tmp/dist" -name 'any-*.tar.gz' | wc -l | tr -d ' ')" "6" "collect: every desktop tarball ships"
check "$(grep -c '^[0-9a-f]\{64\}  any-v9.9.9-.*\.tar\.gz$' "$tmp/dist/SHA256SUMS")" "6" "collect: one sha256sum-format line per tarball"
check "$(wc -l < "$tmp/dist/SHA256SUMS" | tr -d ' ')" "8" "collect: complete manifest lists every shipped asset"
check "$(grep -c "^$D_AAR  any.aar\$" "$tmp/dist/SHA256SUMS")" "1" "collect: the aar line is the leg's"
[ ! -e "$tmp/dist/any.aar.sha256" ] || { echo "FAIL: collect: per-asset .sha256 files must not ship, the manifest does"; fail=1; }
( cd "$tmp/dist" && shatool -c --quiet SHA256SUMS ) || { echo "FAIL: collect: the shipped manifest does not verify against the shipped files"; fail=1; }
notes="$(fanin_notes "$tmp/dist")" || { echo "FAIL: notes: complete release refused"; fail=1; }
check "$(printf '%s\n' "$notes" | sed -n 1p)" "## Assets" "notes: complete release starts at the asset list"
check "$(printf '%s\n' "$notes" | grep -c '^`any-v9.9.9-.*\.tar\.gz` sha256: `[0-9a-f]\{64\}`$')" "6" "notes: one line per tarball"
check "$(printf '%s\n' "$notes" | grep -c "^\`any-v9.9.9-linux-x86_64.tar.gz\` sha256: \`$D_LINUX\`\$")" "1" "notes: tarball line carries the leg digest"
check "$(printf '%s\n' "$notes" | grep -c "^\`any.aar\` sha256: \`$D_AAR\`\$")" "1" "notes: aar line carries the leg digest"
check "$(printf '%s\n' "$notes" | grep -c "^\`any.xcframework.zip\` sha256: \`$D_XCF\`\$")" "1" "notes: xcframework line carries the leg digest"
check "$(printf '%s\n' "$notes" | grep -c '^not built$')" "0" "notes: a complete release has no not-built section"
check "$(printf '%s\n' "$notes" | grep -c 'sha256sum -c SHA256SUMS')" "1" "notes: name the manifest and how to verify"
check "$(printf '%s\n' "$notes" | grep '^llama.cpp: ')" "llama.cpp: b0" "notes: libs line from the env override"
check "$(LLAMACPP_VERSION='' fanin_notes "$tmp/dist" | grep '^llama.cpp: ')" "llama.cpp: $("$here/llamacpp-version.sh")" "notes: libs line from the repo's pin script when not overridden"

# --- smoke red: desktop built but does not ship; notes say so; mobile ships
results success success failure success success success
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null || { echo "FAIL: collect: smoke-red tree refused"; fail=1; }
check "$(find "$tmp/dist" -name 'any-*.tar.gz' | wc -l | tr -d ' ')" "0" "smoke red: desktop tarballs must not ship"
check "$(cat "$tmp/dist/SHA256SUMS")" "$(printf '%s  any.aar\n%s  any.xcframework.zip' "$D_AAR" "$D_XCF")" "collect: manifest carries the shipped assets only"
notes="$(fanin_notes "$tmp/dist")"
# shellcheck disable=SC2059
check "$(printf '%s\n' "$notes" | sed -n 1p)" "$(printf "$PARTIAL" desktop)" "notes: partial line first"
check "$(printf '%s\n' "$notes" | sed -n '/^### Desktop$/{n;p;}')" "not built" "notes: the Desktop section says not built"
check "$(printf '%s\n' "$notes" | grep -c 'tar.gz` sha256')" "0" "notes: no tarball is listed when none shipped"
check "$(printf '%s\n' "$notes" | grep -c '^not built$')" "1" "notes: exactly one not-built section"

# --- refusals: the collect fails closed, loudly, and ships nothing unverified
results success success success success success success
printf '%s  any.aar\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$art/android-aar/any.aar.sha256"
reset_dist; refuse "collect: a digest mismatch must fail the collect" fanin_collect "$art" "$tmp/dist"
leg_digest "$art/android-aar/any.aar"
mv "$art/ios-xcframework/any.xcframework.zip.sha256" "$tmp/xcf.sha256"
reset_dist; refuse "collect: a missing leg digest must fail the collect" fanin_collect "$art" "$tmp/dist"
mv "$tmp/xcf.sha256" "$art/ios-xcframework/any.xcframework.zip.sha256"
cp "$art/android-aar/any.aar" "$art/android-aar/other.aar"; ( cd "$art/android-aar" && shatool other.aar > any.aar.sha256 )
reset_dist; refuse "collect: a digest written for a different file must not verify any.aar" fanin_collect "$art" "$tmp/dist"
[ ! -e "$tmp/dist/any.aar" ] || { echo "FAIL: collect: any.aar shipped unverified"; fail=1; }
rm -f "$art/android-aar/other.aar"; leg_digest "$art/android-aar/any.aar"
printf '%s *any.aar\n' "$D_AAR" > "$art/android-aar/any.aar.sha256"
reset_dist; refuse "collect: a digest line that does not name the bare asset must not enter the manifest" fanin_collect "$art" "$tmp/dist"
leg_digest "$art/android-aar/any.aar"
{ cat "$art/android-aar/any.aar.sha256"; cat "$art/android-aar/any.aar.sha256"; } > "$tmp/dup"; mv "$tmp/dup" "$art/android-aar/any.aar.sha256"
reset_dist; refuse "collect: a two-line digest file must fail the collect" fanin_collect "$art" "$tmp/dist"
leg_digest "$art/android-aar/any.aar"
mv "$art/android-aar" "$tmp/aar.aside"
reset_dist; refuse "collect: a passed platform with no artifact dir must fail the publish" fanin_collect "$art" "$tmp/dist"
mv "$tmp/aar.aside" "$art/android-aar"
reset_dist; refuse "collect: a refusal must name itself as an error annotation" bash -c '. "$0"; fanin_collect "$1" "$2" 2>&1 >/dev/null | grep -q "^::error::release-fanin: " || exit 1; exit 0' "$here/release-fanin.sh" "$tmp/missing-tree" "$tmp/dist"
results success failure skipped failure failure success
reset_dist; refuse "collect: no platform passed, nothing to publish — must refuse" fanin_collect "$art" "$tmp/dist"
# a failed copy must not leave the asset in the manifest (errexit is off inside the function — every step is guarded)
results success success success success success success
reset_dist
# shellcheck disable=SC2329  # invoked by name from inside _fanin_take
cp() { return 1; }
refuse "collect: a failed cp must fail the collect" fanin_collect "$art" "$tmp/dist"
unset -f cp
check "$(wc -l < "$tmp/dist/SHA256SUMS" | tr -d ' ')" "0" "collect: a failed cp records nothing in the manifest"

# --- notes and payloads never render an empty digest: a missing manifest line refuses
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null
sed -i.bak '/  any.aar$/d' "$tmp/dist/SHA256SUMS"; rm -f "$tmp/dist/SHA256SUMS.bak"
refuse "notes: a shipped asset with no manifest line must refuse, not render an empty digest" fanin_notes "$tmp/dist"
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null
( cd "$tmp" && mkdir -p nopin && printf '#!/usr/bin/env bash\nexit 1\n' > nopin/llamacpp-version.sh && chmod +x nopin/llamacpp-version.sh )
refuse "notes: an unreadable llama.cpp pin must refuse, not print an empty version" bash -c 'cp "$1" "$2/release-fanin.sh"; . "$2/release-fanin.sh"; LLAMACPP_VERSION="" DESKTOP_RESULT=success DESKTOP_SMOKE_RESULT=success ANDROID_RESULT=success IOS_RESULT=success fanin_notes "$3"' "$here/release-fanin.sh" "$tmp/nopin" "$tmp/dist"

# --- dispatch through a fake gh that logs its args; GH_FAIL makes it fail
mkdir -p "$tmp/bin"
cat > "$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GH_LOG"
[ -z "${GH_FAIL:-}" ] || exit 1
GH
chmod +x "$tmp/bin/gh"
export GH="$tmp/bin/gh" GH_LOG="$tmp/gh.log"

# android red: any-ui + swift get any-published with their full payloads; kotlin does not
results success success success failure success success
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null
: > "$GH_LOG"; : > "$tmp/out"
fanin_publish_dispatch "$tmp/dist" "$tmp/out" >/dev/null || { echo "FAIL: publish-dispatch: refused with a working gh"; fail=1; }
check "$(grep -c 'event_type=any-published' "$GH_LOG")" "2" "publish-dispatch: two clients"
grep -qx 'api repos/anyproto/any-ui/dispatches -f event_type=any-published -f client_payload\[version\]=v9.9.9 -f client_payload\[channel\]=prerelease' "$GH_LOG" || { echo "FAIL: publish-dispatch: any-ui payload"; fail=1; }
grep -qx "api repos/anyproto/anytype-swift/dispatches -f event_type=any-published -f client_payload\[version\]=v9.9.9 -f client_payload\[channel\]=prerelease -f client_payload\[asset\]=any.xcframework.zip -f client_payload\[sha256\]=$D_XCF" "$GH_LOG" || { echo "FAIL: publish-dispatch: swift payload"; fail=1; }
grep -q 'repos/anyproto/any-kotlin/dispatches' "$GH_LOG" && { echo "FAIL: publish-dispatch: kotlin dispatched with android red"; fail=1; }
check "$(cat "$tmp/out")" "published_clients=anyproto/any-ui,anyproto/anytype-swift" "publish-dispatch: output"
# ios red: kotlin's arm runs with the manifest digest
results success success success success failure success
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null
: > "$GH_LOG"; : > "$tmp/out"
fanin_publish_dispatch "$tmp/dist" "$tmp/out" >/dev/null
grep -qx "api repos/anyproto/any-kotlin/dispatches -f event_type=any-published -f client_payload\[version\]=v9.9.9 -f client_payload\[channel\]=prerelease -f client_payload\[asset\]=any.aar -f client_payload\[sha256\]=$D_AAR" "$GH_LOG" || { echo "FAIL: publish-dispatch: kotlin payload"; fail=1; }
grep -q 'client_payload\[sha256\]=$' "$GH_LOG" && { echo "FAIL: publish-dispatch: an empty sha256 was dispatched"; fail=1; }
# a manifest missing the aar line: kotlin is neither dispatched nor recorded as published
sed -i.bak '/  any.aar$/d' "$tmp/dist/SHA256SUMS"; rm -f "$tmp/dist/SHA256SUMS.bak"
: > "$GH_LOG"; : > "$tmp/out"
refuse "publish-dispatch: a missing manifest digest must refuse" fanin_publish_dispatch "$tmp/dist" "$tmp/out"
grep -q 'repos/anyproto/any-kotlin/dispatches' "$GH_LOG" && { echo "FAIL: publish-dispatch: kotlin told any-published with no manifest digest"; fail=1; }
grep -q 'any-kotlin' "$tmp/out" && { echo "FAIL: publish-dispatch: kotlin recorded as published"; fail=1; }
# a failing gh: warned, still recorded as published (the release IS out), and zero-of-N successes is red
results success success success success success success
reset_dist; fanin_collect "$art" "$tmp/dist" >/dev/null
: > "$GH_LOG"; : > "$tmp/out"
out="$(GH_FAIL=1 fanin_publish_dispatch "$tmp/dist" "$tmp/out" 2>&1)"; rc=$?
check "$rc" "1" "publish-dispatch: every dispatch failing returns non-zero (token set but unusable)"
check "$(printf '%s\n' "$out" | grep -c '^::warning::')" "3" "publish-dispatch: each failed dispatch warns"
check "$(cat "$tmp/out")" "published_clients=anyproto/any-ui,anyproto/anytype-swift,anyproto/any-kotlin" "publish-dispatch: attempted counts as published even when gh fails"

# fail-dispatch: android red → kotlin alone, full payload
results success success success failure success success
: > "$GH_LOG"
PUBLISHED_CLIENTS=anyproto/any-ui,anyproto/anytype-swift
fanin_fail_dispatch >/dev/null || { echo "FAIL: fail-dispatch: refused with a working gh"; fail=1; }
check "$(grep -c 'event_type=any-build-failed' "$GH_LOG")" "1" "fail-dispatch: one client"
grep -qx 'api repos/anyproto/any-kotlin/dispatches -f event_type=any-build-failed -f client_payload\[version\]=v9.9.9 -f client_payload\[channel\]=prerelease -f client_payload\[platform\]=android -f client_payload\[failed_platforms\]=android -f client_payload\[run_url\]=https://example.test/run/1' "$GH_LOG" || { echo "FAIL: fail-dispatch: kotlin payload"; fail=1; }
# smoke red, mobile green: any-ui alone, with ITS platform
results success success failure success success success
: > "$GH_LOG"
PUBLISHED_CLIENTS=anyproto/anytype-swift,anyproto/any-kotlin
fanin_fail_dispatch >/dev/null
check "$(grep -c 'event_type=any-build-failed' "$GH_LOG")" "1" "fail-dispatch smoke red: exactly one client told"
grep -q 'repos/anyproto/any-ui/dispatches.*client_payload\[platform\]=desktop -f client_payload\[failed_platforms\]=desktop ' "$GH_LOG" || { echo "FAIL: fail-dispatch smoke red: any-ui must get platform=desktop, failed_platforms=desktop"; fail=1; }
# legs green, publish failed: every client told, stage publish
results success success success success success failure
: > "$GH_LOG"
PUBLISHED_CLIENTS=''
fanin_fail_dispatch >/dev/null
check "$(grep -c 'client_payload\[failed_platforms\]=publish ' "$GH_LOG")" "3" "fail-dispatch publish failed: the publish stage, to all three"
# version failed: unresolved, stage version, to all three
results failure skipped skipped skipped skipped skipped
: > "$GH_LOG"
VERSION='' PUBLISHED_CLIENTS=''
fanin_fail_dispatch >/dev/null
VERSION=v9.9.9
check "$(grep -c 'client_payload\[version\]=unresolved ' "$GH_LOG")" "3" "fail-dispatch: version failed → unresolved to all three"
check "$(grep -c 'client_payload\[failed_platforms\]=version ' "$GH_LOG")" "3" "fail-dispatch: version failed → the version stage, to all three"
# nothing failed, nobody recorded as published (output plumbing broke): nobody is told a build failed, loudly
results success success success success success success
: > "$GH_LOG"
PUBLISHED_CLIENTS=''
out="$(fanin_fail_dispatch 2>&1)"
check "$(grep -c . "$GH_LOG")" "0" "fail-dispatch: nothing failed → nobody is told a build failed"
check "$(printf '%s\n' "$out" | grep -c '^::warning::')" "1" "fail-dispatch: nothing failed but no client recorded as published is warned about"
grep -q 'failed_platforms\]= ' "$GH_LOG" && { echo "FAIL: fail-dispatch: an empty failed_platforms was dispatched"; fail=1; }
# every dispatch failing is red here too
results success success success failure success success
: > "$GH_LOG"
PUBLISHED_CLIENTS=anyproto/any-ui,anyproto/anytype-swift
GH_FAIL=1 fanin_fail_dispatch >/dev/null 2>&1; check "$?" "1" "fail-dispatch: every dispatch failing returns non-zero"

rm -rf "$tmp"
[ "$fail" = 0 ] && echo "release-fanin: ok"
exit "$fail"
