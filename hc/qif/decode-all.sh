#!/bin/bash

QIFDIR="${QIFDIR:-$HOME/code/qifs}"
QIFCOMMIT="${QIFCOMMIT:-master}"
CHECKED=0
ERRORS=()
NOOP="${NOOP:-}"
NOOP="${VERBOSE:-}"

# Temporary file management.
TMPFILES=()
trap 'rm -f "${TMPFILES[@]}"' EXIT
# createTempFile creates a tracked temporary file that is removed at exit.
createTempFile() {
    TMPFILES+=($(mktemp))
    echo "${TMPFILES[-1]}"
}

sortBlocks() {
    acc=()
    while read -r line; do
        if [[ -n "$line" ]]; then
            acc+=("$line")
            continue
        fi
        for a in "${acc[@]}"; do
            echo "$a"
        done | sort
        echo
        acc=()
    done
    for a in "${acc[@]}"; do
        echo "$a"
    done | sort
    echo
}

# Usage: runDiff <encoded> <reference>
runDiffBasic() {
    echo "diff -u \"$2\" <(./qif decode -a \"$1\")"
    [[ -z "$NOOP" ]] && diff -u "$2" <(./qif decode -a "$1" 2>&1) 2>&1
}

runDiffSorted() {
    echo "diff -u <(cat \"$2\" | sortBlocks) <(./qif decode -a \"$1\" | sortBlocks)"
    [[ -z "$NOOP" ]] && diff -u <(cat "$2" | sortBlocks) <(./qif decode -a "$1" 2>&1 | sortBlocks) 2>&1
}

runDiff() {
    if [[ "${2%-hq.qif}" != "$2" ]]; then
        runDiffSorted "$@"
    else
        runDiffBasic "$@"
    fi
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
    for pkg in "${QIFDIR}/encoded/qpack-03"/*; do
        [[ ! -d "$pkg" ]] && continue

        echo -n "  ${pkg##*/} "
        estart="${#ERRORS[@]}"
        for f in "${pkg}/${refbase}"*; do
            [[ -r "$f" ]] && checkDecode "$f" "$ref"
        done
        eend="${#ERRORS[@]}"
        if [[ "$estart" -eq "$eend" ]]; then
            echo " OK"
        else
           echo " $(($eend - $estart)) ERRORS"
        fi
    done
}

cd "$(dirname "$0")"
go build
[[ -d "$QIFDIR" ]] || git clone -b "$QIFCOMMIT" https://github.com/qpackers/qifs "$QIFDIR"
[[ "$(git rev-parse HEAD)" != "$(git rev-parse "$QIFCOMMIT")" ]] || \
  echo "Warning: qifs repo isn't at $QIFCOMMIT" 1>&2

for ref in "${QIFDIR}"/qifs/*.qif; do
    echo "${ref##*/}:"
    checkQif "$ref"
done

[[ -n "$VERBOSE" ]] && show=(cat) || show=(head -1)
for e in "${ERRORS[@]}"; do
   "${show[@]}" "$e"
done
echo "Check ${CHECKED} files.  Found ${#ERRORS[@]} errors."
exit $((${#ERRORS[@]} != 0))
