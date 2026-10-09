#!/usr/bin/env bash
set -euo pipefail

# No keystore is opened or generated. These are configuration-failure gates.
task_dir="$(mktemp -d "${TMPDIR:-/tmp}/croptop-signing-check.XXXXXX")"
trap 'rm -f "$task_dir/output"; rmdir "$task_dir"' EXIT
unsigned_env=(env -u CROPTOP_ANDROID_KEYSTORE -u CROPTOP_ANDROID_STORE_PASSWORD -u CROPTOP_ANDROID_KEY_ALIAS -u CROPTOP_ANDROID_KEY_PASSWORD)

expect_failure() {
    local message="$1"
    shift
    if "$@" > "$task_dir/output" 2>&1; then
        printf '%s\n' 'Expected signing configuration to fail, but it succeeded.' >&2
        exit 1
    fi
    if ! grep -Fq "$message" "$task_dir/output"; then
        printf '%s\n' 'Signing gate failed for an unexpected reason; inspect the Gradle setup.' >&2
        exit 1
    fi
}

expect_failure 'Release signing was required, but no external keystore was supplied.' \
    "${unsigned_env[@]}" ./gradlew --no-daemon help -Pcroptop.requireReleaseSigning=true
expect_failure 'Incomplete Android signing configuration.' \
    "${unsigned_env[@]}" CROPTOP_ANDROID_KEYSTORE=/nonexistent/croptop-test-only.keystore ./gradlew --no-daemon help
expect_failure 'croptop.requireReleaseSigning must be exactly true or false.' \
    "${unsigned_env[@]}" ./gradlew --no-daemon help -Pcroptop.requireReleaseSigning=TRUE
printf '%s\n' 'PASS: required, partial, and malformed release-signing inputs fail closed.'
