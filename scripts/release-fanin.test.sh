#!/usr/bin/env bash
# Unit tests for the publish fan-in decisions in scripts/release-fanin.sh:
# which platforms passed, what the release notes say, who gets which dispatch.
# Pure shell over env vars and a temp tree; `gh` is a fake that logs its args.
# Run via `make test` (part of pr-checks).
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
export RUN_URL=https://example.test/run/1 VERSION=v9.9.9 CHANNEL=prerelease LLAMACPP_VERSION=b0

# --- all green
export VERSION_RESULT=success DESKTOP_RESULT=success DESKTOP_SMOKE_RESULT=success ANDROID_RESULT=success IOS_RESULT=success PUBLISH_RESULT=success
check "$(fanin_failed_platforms)" "" "all green: no failed platforms"
check "$(fanin_partial_line)" "" "all green: no partial line"
check "$(fanin_clients_to_publish | tr '\n' ,)" "anyproto/any-ui,anyproto/anytype-swift,anyproto/any-kotlin," "all green: every client published"
check "$(fanin_failed_stages)" "" "all green: no failed stages"

# --- android red
ANDROID_RESULT=failure
check "$(fanin_failed_platforms)" "android" "android red: failed platforms"
check "$(fanin_partial_line)" "> **Partial release** — missing: android (run https://example.test/run/1)" "android red: partial line"
check "$(fanin_clients_to_publish | tr '\n' ,)" "anyproto/any-ui,anyproto/anytype-swift," "android red: kotlin not published"
check "$(fanin_clients_to_fail anyproto/any-ui,anyproto/anytype-swift)" "anyproto/any-kotlin" "android red: kotlin told"
check "$(fanin_failed_stages)" "android" "android red: stages"

# --- android + ios red
IOS_RESULT=failure
check "$(fanin_partial_line)" "> **Partial release** — missing: android, ios (run https://example.test/run/1)" "two red: comma-space list"

# --- desktop built, smoke red (Review Focus 1)
ANDROID_RESULT=success IOS_RESULT=success DESKTOP_SMOKE_RESULT=failure
check "$(fanin_failed_platforms)" "desktop" "smoke red: desktop counts as failed"
check "$(fanin_clients_to_publish | tr '\n' ,)" "anyproto/anytype-swift,anyproto/any-kotlin," "smoke red: any-ui not published"

# --- every platform red → publish skipped (Review Focus 2)
DESKTOP_RESULT=failure DESKTOP_SMOKE_RESULT=skipped ANDROID_RESULT=failure IOS_RESULT=failure PUBLISH_RESULT=skipped
fanin_any_ok && { echo "FAIL: any-ok with every platform red"; fail=1; }
check "$(fanin_failed_stages)" "desktop,android,ios" "every platform red: platforms only, a skipped publish is not a stage"
check "$(fanin_clients_to_fail "" | tr '\n' ,)" "anyproto/any-ui,anyproto/anytype-swift,anyproto/any-kotlin," "every platform red: every client told"

# --- legs green, publish failed
DESKTOP_RESULT=success DESKTOP_SMOKE_RESULT=success ANDROID_RESULT=success IOS_RESULT=success PUBLISH_RESULT=failure
check "$(fanin_failed_stages)" "publish" "publish failed: stage only"

# --- version failed: nothing else ran
VERSION_RESULT=failure DESKTOP_RESULT=skipped DESKTOP_SMOKE_RESULT=skipped ANDROID_RESULT=skipped IOS_RESULT=skipped PUBLISH_RESULT=skipped
check "$(fanin_failed_stages)" "version" "version failed: the one stage, no platforms"

# --- collect + notes over a fake artifact tree, smoke red
tmp="$(mktemp -d -t fanin.XXXXXX)"
mkdir -p "$tmp/artifacts/desktop-tarballs" "$tmp/artifacts/android-aar" "$tmp/artifacts/ios-xcframework"
echo d > "$tmp/artifacts/desktop-tarballs/any-v9.9.9-linux-x86_64.tar.gz"
echo a > "$tmp/artifacts/android-aar/any.aar"
echo i > "$tmp/artifacts/ios-xcframework/any.xcframework.zip"
# The legs hash what they BUILT, in sha256sum format with the bare name, and
# ship the digest beside the artifact; publish verifies on download and never
# rehashes. The test computes the expected digests independently.
digest() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
for f in "$tmp"/artifacts/*/any*; do printf '%s  %s\n' "$(digest "$f")" "$(basename "$f")" > "$f.sha256"; done
D_TAR="$(digest "$tmp/artifacts/desktop-tarballs/any-v9.9.9-linux-x86_64.tar.gz")"
D_AAR="$(digest "$tmp/artifacts/android-aar/any.aar")"
D_XCF="$(digest "$tmp/artifacts/ios-xcframework/any.xcframework.zip")"
VERSION_RESULT=success DESKTOP_RESULT=success DESKTOP_SMOKE_RESULT=failure ANDROID_RESULT=success IOS_RESULT=success PUBLISH_RESULT=success
fanin_collect "$tmp/artifacts" "$tmp/dist" >/dev/null
[ ! -e "$tmp/dist/any-v9.9.9-linux-x86_64.tar.gz" ] || { echo "FAIL: smoke red: desktop tarballs must not ship"; fail=1; }
[ -e "$tmp/dist/any.aar" ] && [ -e "$tmp/dist/any.xcframework.zip" ] || { echo "FAIL: collect: mobile assets missing"; fail=1; }
check "$(cat "$tmp/dist/SHA256SUMS")" "$(printf '%s  any.aar\n%s  any.xcframework.zip' "$D_AAR" "$D_XCF")" "collect: manifest carries the legs' digests of the shipped assets only"
[ ! -e "$tmp/dist/any.aar.sha256" ] || { echo "FAIL: collect: per-asset .sha256 files must not ship, the manifest does"; fail=1; }
notes="$(fanin_notes "$tmp/dist")"
check "$(printf '%s\n' "$notes" | sed -n 1p)" "> **Partial release** — missing: desktop (run https://example.test/run/1)" "notes: partial line first"
check "$(printf '%s\n' "$notes" | grep -c '^not built$')" "1" "notes: exactly one not-built section"
check "$(printf '%s\n' "$notes" | grep -c "^\`any.aar\` sha256: \`$D_AAR\`\$")" "1" "notes: aar line carries the leg digest"
check "$(printf '%s\n' "$notes" | grep -c "^\`any.xcframework.zip\` sha256: \`$D_XCF\`\$")" "1" "notes: xcframework line carries the leg digest"
check "$(printf '%s\n' "$notes" | grep -c 'sha256sum -c SHA256SUMS')" "1" "notes: name the manifest and how to verify"
check "$(printf '%s\n' "$notes" | grep -c 'llama.cpp: b0')" "1" "notes: libs line"

# --- complete notes carry no marker
DESKTOP_SMOKE_RESULT=success
rm -rf "$tmp/dist"; fanin_collect "$tmp/artifacts" "$tmp/dist" >/dev/null
check "$(fanin_notes "$tmp/dist" | sed -n 1p)" "## Assets" "notes: complete release starts at the asset list"
check "$(fanin_notes "$tmp/dist" | grep -c "^\`any-v9.9.9-linux-x86_64.tar.gz\` sha256: \`$D_TAR\`\$")" "1" "notes: tarball line carries the leg digest"
check "$(wc -l < "$tmp/dist/SHA256SUMS" | tr -d ' ')" "3" "collect: complete manifest lists every shipped asset"
[ -e "$tmp/dist/any-v9.9.9-linux-x86_64.tar.gz" ] || { echo "FAIL: collect: the desktop tarball must reach dist on a green desktop"; fail=1; }
check "$(fanin_notes "$tmp/dist" | grep -c '^not built$')" "0" "notes: a complete release has no not-built section"
( cd "$tmp/dist" && { command -v sha256sum >/dev/null 2>&1 && sha256sum -c --quiet SHA256SUMS || shasum -a 256 -c --quiet SHA256SUMS; } ) || { echo "FAIL: collect: the shipped manifest does not verify against the shipped files"; fail=1; }
# --- a leg digest that does not match what arrived stops the collect (fail-closed), and a missing one does too
printf '%s  any.aar\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$tmp/artifacts/android-aar/any.aar.sha256"
rm -rf "$tmp/dist"; fanin_collect "$tmp/artifacts" "$tmp/dist" >/dev/null 2>&1 && { echo "FAIL: collect: a digest mismatch must fail the collect"; fail=1; }
printf '%s  any.aar\n' "$D_AAR" > "$tmp/artifacts/android-aar/any.aar.sha256"
mv "$tmp/artifacts/ios-xcframework/any.xcframework.zip.sha256" "$tmp/xcf.sha256"
rm -rf "$tmp/dist"; fanin_collect "$tmp/artifacts" "$tmp/dist" >/dev/null 2>&1 && { echo "FAIL: collect: a missing leg digest must fail the collect"; fail=1; }
mv "$tmp/xcf.sha256" "$tmp/artifacts/ios-xcframework/any.xcframework.zip.sha256"

# --- dispatch through a fake gh
mkdir -p "$tmp/bin"
cat > "$tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GH_LOG"
EOF
chmod +x "$tmp/bin/gh"
export GH="$tmp/bin/gh" GH_LOG="$tmp/gh.log"
ANDROID_RESULT=failure
rm -rf "$tmp/dist"; fanin_collect "$tmp/artifacts" "$tmp/dist" >/dev/null
: > "$tmp/out"
fanin_publish_dispatch "$tmp/dist" "$tmp/out" >/dev/null
check "$(grep -c 'event_type=any-published' "$GH_LOG")" "2" "publish-dispatch: two clients"
grep -q 'repos/anyproto/any-kotlin/dispatches' "$GH_LOG" && { echo "FAIL: publish-dispatch: kotlin dispatched with android red"; fail=1; }
grep -q "client_payload\[asset\]=any.xcframework.zip -f client_payload\[sha256\]=$D_XCF" "$GH_LOG" || { echo "FAIL: publish-dispatch: swift payload must carry the manifest digest"; fail=1; }
check "$(cat "$tmp/out")" "published_clients=anyproto/any-ui,anyproto/anytype-swift" "publish-dispatch: output"
: > "$GH_LOG"
PUBLISHED_CLIENTS=anyproto/any-ui,anyproto/anytype-swift fanin_fail_dispatch >/dev/null
check "$(grep -c 'event_type=any-build-failed' "$GH_LOG")" "1" "fail-dispatch: one client"
grep -q 'repos/anyproto/any-kotlin/dispatches.*client_payload\[platform\]=android.*client_payload\[failed_platforms\]=android.*client_payload\[run_url\]=https://example.test/run/1' "$GH_LOG" || { echo "FAIL: fail-dispatch: kotlin payload"; fail=1; }
: > "$GH_LOG"
VERSION_RESULT=failure VERSION='' DESKTOP_RESULT=skipped DESKTOP_SMOKE_RESULT=skipped ANDROID_RESULT=skipped IOS_RESULT=skipped PUBLISH_RESULT=skipped PUBLISHED_CLIENTS='' fanin_fail_dispatch >/dev/null
check "$(grep -c 'client_payload\[version\]=unresolved' "$GH_LOG")" "3" "fail-dispatch: version failed → unresolved to all three"
check "$(grep -c 'client_payload\[failed_platforms\]=version' "$GH_LOG")" "3" "fail-dispatch: version failed → the version stage, to all three"

# --- smoke red, mobile green: any-ui alone is told, with ITS platform (Review Focus 1, dispatch half)
: > "$GH_LOG"
VERSION_RESULT=success DESKTOP_RESULT=success DESKTOP_SMOKE_RESULT=failure ANDROID_RESULT=success IOS_RESULT=success PUBLISH_RESULT=success
PUBLISHED_CLIENTS=anyproto/anytype-swift,anyproto/any-kotlin fanin_fail_dispatch >/dev/null
check "$(grep -c 'event_type=any-build-failed' "$GH_LOG")" "1" "fail-dispatch smoke red: exactly one client told"
grep -q 'repos/anyproto/any-ui/dispatches.*client_payload\[platform\]=desktop.*client_payload\[failed_platforms\]=desktop.*client_payload\[run_url\]=https://example.test/run/1' "$GH_LOG" || { echo "FAIL: fail-dispatch smoke red: any-ui must get platform=desktop, failed_platforms=desktop"; fail=1; }

# --- legs green, publish failed before the release: every client told, stage publish
: > "$GH_LOG"
DESKTOP_SMOKE_RESULT=success PUBLISH_RESULT=failure
PUBLISHED_CLIENTS='' fanin_fail_dispatch >/dev/null
check "$(grep -c 'client_payload\[failed_platforms\]=publish' "$GH_LOG")" "3" "fail-dispatch publish failed: the publish stage, to all three"
check "$(grep -c 'client_payload\[channel\]=prerelease' "$GH_LOG")" "3" "fail-dispatch: channel rides every failure payload"

rm -rf "$tmp"
[ "$fail" = 0 ] && echo "release-fanin: ok"
exit "$fail"
