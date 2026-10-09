#!/usr/bin/env bash
set -euo pipefail

# Deliberately limited to a task-owned emulator; never clears app data or uses a site key.
: "${CROPTOP_TEST_EMULATOR_SERIAL:?Set the isolated emulator serial (emulator-NNNN)}"
: "${CROPTOP_TEST_AVD_NAME:?Set the task-owned AVD name}"
: "${CROPTOP_TEST_OUTPUT_DIR:?Set a directory for screenshots and logs}"
: "${ANDROID_HOME:?Set the isolated Android SDK directory}"
case "$CROPTOP_TEST_EMULATOR_SERIAL" in emulator-[0-9]*) ;; *) printf '%s\n' 'Refusing a non-emulator target.' >&2; exit 1;; esac
case "$CROPTOP_TEST_AVD_NAME" in croptop-*) ;; *) printf '%s\n' 'Refusing an AVD which is not task-owned.' >&2; exit 1;; esac
adb="$ANDROID_HOME/platform-tools/adb"
target=(-s "$CROPTOP_TEST_EMULATOR_SERIAL")
actual_avd="$($adb "${target[@]}" emu avd name | tr -d '\r' | head -n 1)"
[[ "$actual_avd" == "$CROPTOP_TEST_AVD_NAME" ]] || { printf '%s\n' 'AVD identity mismatch.' >&2; exit 1; }
[[ "$($adb "${target[@]}" shell getprop ro.kernel.qemu | tr -d '\r')" == 1 ]] || { printf '%s\n' 'Target is not an emulator.' >&2; exit 1; }
mkdir -p "$CROPTOP_TEST_OUTPUT_DIR"

"$adb" "${target[@]}" shell am instrument -w -r -e class top.crop.mobile.NativeIntakeTest \
    top.crop.mobile.test/top.crop.mobile.NativeInstrumentationRunner | tee "$CROPTOP_TEST_OUTPUT_DIR/native-intake-tests.txt"
if rg -q 'FAILURES!!!|INSTRUMENTATION_FAILED|shortMsg=' "$CROPTOP_TEST_OUTPUT_DIR/native-intake-tests.txt"; then exit 1; fi
rg -q 'OK \([1-9][0-9]* tests?\)' "$CROPTOP_TEST_OUTPUT_DIR/native-intake-tests.txt"
rg -q 'INSTRUMENTATION_CODE: -1' "$CROPTOP_TEST_OUTPUT_DIR/native-intake-tests.txt"

run_phase() {
    local phase="$1"
    "$adb" "${target[@]}" shell am instrument -w -r -e phase "$phase" top.crop.mobile.test/top.crop.mobile.NativeInstrumentationRunner \
        | tee "$CROPTOP_TEST_OUTPUT_DIR/process-death-$phase.txt"
    if rg -q 'FAIL:|INSTRUMENTATION_FAILED|shortMsg=' "$CROPTOP_TEST_OUTPUT_DIR/process-death-$phase.txt"; then exit 1; fi
    rg -q 'PASS:' "$CROPTOP_TEST_OUTPUT_DIR/process-death-$phase.txt"
    rg -q 'INSTRUMENTATION_CODE: -1' "$CROPTOP_TEST_OUTPUT_DIR/process-death-$phase.txt"
}

capture_composer() {
    local name="$1"
    # am start -W can return during the launch splash. Wait for an idle, real composer.
    "$adb" "${target[@]}" shell uiautomator dump "/data/local/tmp/croptop-native-$name-ui.xml" > "$CROPTOP_TEST_OUTPUT_DIR/$name-dump.txt"
    rg -q 'UI hierchary dumped to:' "$CROPTOP_TEST_OUTPUT_DIR/$name-dump.txt"
    "$adb" "${target[@]}" exec-out cat "/data/local/tmp/croptop-native-$name-ui.xml" > "$CROPTOP_TEST_OUTPUT_DIR/$name-ui.xml"
    rg -q 'text="Your screenshot"' "$CROPTOP_TEST_OUTPUT_DIR/$name-ui.xml"
    "$adb" "${target[@]}" exec-out screencap -p > "$CROPTOP_TEST_OUTPUT_DIR/$name.png"
}

run_phase seed
"$adb" "${target[@]}" shell am start -W -n top.crop.mobile/.MainActivity -a android.intent.action.MAIN
before_pid="$($adb "${target[@]}" shell pidof top.crop.mobile | tr -d '\r')"
[[ -n "$before_pid" ]]
capture_composer before-force-stop
"$adb" "${target[@]}" shell am force-stop top.crop.mobile
stopped_pid="$($adb "${target[@]}" shell pidof top.crop.mobile | tr -d '\r' || true)"
[[ -z "$stopped_pid" ]] || { printf '%s\n' 'Application process survived force-stop.' >&2; exit 1; }
"$adb" "${target[@]}" shell am start -W -n top.crop.mobile/.MainActivity -a android.intent.action.MAIN
after_pid="$($adb "${target[@]}" shell pidof top.crop.mobile | tr -d '\r')"
[[ -n "$after_pid" && "$before_pid" != "$after_pid" ]]
capture_composer after-force-stop
printf 'Force-stop process evidence: before=%s; absent after stop; relaunch=%s\n' "$before_pid" "$after_pid" | tee "$CROPTOP_TEST_OUTPUT_DIR/process-evidence.txt"
run_phase verify
