#!/bin/bash

QIFDIR="${QIFDIR:-~/code/qif}"
QIFCOMMIT="${QIFCOMMIT:-master}"
CHECKED=0
ERRORS=()

# Temporary file management.
TMPFILES=()
trap 'rm -f "${TMPFILES[@]}"' EXIT
# createTempFile creates a tracked temporary file that is removed at exit.
createTempFile() {
    TMPFILES+=($(mktemp))
    echo "${TMPFILES[-1]}"
}

# Usage: runDiff <encoded> <reference>
runDiff() {
    echo "diff -u <(go run github.com/martinthomson/minhq/hc/qif decode \"$1\") \"$2\""
    diff -u <(go run github.com/martinthomson/minhq/hc/qif decode "$1") "$2" 2>&1
}

# Usage: checkDecode <encoded> <reference>
checkDecode() {
    echo -n "."
    tmp=$(createTempFile)
    CHECKED=$((CHECKED + 1))
    if ! runDiff "$@" > "$tmp"; then
        ERRORS+=("$tmp")
    fi
}

# Usage: checkQif <reference>
checkQif() {
    ref="$1"
    refbase="${ref##*/}"
    refbase="${refbase%.*}"
    for pkg in "${QIFDIR}/encoded"/*; do
       [[ ! -d "$pkg" ]] && continue

       echo -n "Testing package ${pkg##*/} "
       for f in "${pkg}/${refbase}"*; do
           checkDecode "$f" "$ref"
       done
       echo " done."
    done
}

[[ -d "$QIFDIR" ]] || git clone -b "$QIFCOMMIT" https://github.com/qpackers/qifs "$QIFDIR"
[[ "$(git rev-parse HEAD)" != "$(git rev-parse "$QIFCOMMIT")" ]] || \
  echo "Warning: qifs repo isn't at $QIFCOMMIT" 1>&2

for ref in "${QIFDIR}"/qifs/*.qif; do
    checkQif "$ref"
done

for e in "${ERRORS[@]}"; do
   cat "$e"
done
echo "Check ${CHECKED} files.  Found ${#ERRORS[@]} errors."
exit $((${#ERRORS[@]} != 0))
