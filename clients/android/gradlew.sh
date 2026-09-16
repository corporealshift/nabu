#!/usr/bin/env bash
# Pins the JDK, SDK and Gradle under ~/android-toolchain. None of them are on
# PATH, which is why a plain `gradle` or `java` finds nothing.
set -euo pipefail
export JAVA_HOME="${JAVA_HOME:-C:/Users/corpo/android-toolchain/jdk}"
export ANDROID_HOME="${ANDROID_HOME:-C:/Users/corpo/android-toolchain/sdk}"
exec "C:/Users/corpo/android-toolchain/gradle/bin/gradle" "$@"
