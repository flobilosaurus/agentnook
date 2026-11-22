#!/bin/sh
# Unit-test runtime. Shell builtins only: tests deliberately remove host tools from PATH.
if [ "$1" = --version ]; then printf '%s\n' "${AGENTNOOK_TEST_SMOLVM_VERSION:-smolvm 1.36.1}"; exit 0; fi
printf '%s\n' "$@" >> "$AGENTNOOK_TEST_SMOLVM_LOG"
case "$1 $2" in
  'machine data-dir') printf '%s\n' "$AGENTNOOK_HOME"; exit 0 ;;
  'machine create') exit "${AGENTNOOK_TEST_CREATE_EXIT:-0}" ;;
  'machine start') exit "${AGENTNOOK_TEST_START_EXIT:-0}" ;;
  'machine exec') exit "${AGENTNOOK_TEST_BUILD_EXIT:-0}" ;;
  'machine stop') exit "${AGENTNOOK_TEST_STOP_EXIT:-0}" ;;
  'machine delete') exit "${AGENTNOOK_TEST_DELETE_EXIT:-0}" ;;
  'machine run') exit "${AGENTNOOK_TEST_VERIFY_EXIT:-0}" ;;
  'pack create')
    output=''
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --output ]; then shift; output=$1; fi
      shift
    done
    [ -n "$output" ] || exit 98
    printf 'test-only-stub' > "$output"
    if [ -z "$AGENTNOOK_TEST_EMPTY_ARTIFACT" ]; then
      printf 'test-only-artifact' > "$output.smolmachine"
    else
      : > "$output.smolmachine"
    fi
    exit "${AGENTNOOK_TEST_PACK_EXIT:-0}"
    ;;
esac
exit 99
