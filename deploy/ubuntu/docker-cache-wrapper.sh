#!/usr/bin/env bash
# Guest PATH wrapper: /usr/local/bin/docker
# Adds BuildKit registry cache flags for docker build / docker buildx build.
# Sourced by docker-cache-wrapper_test.sh (defines temperci_docker_rewrite only).

# Returns 0 if docker buildx is usable (or tests force it).
temperci_have_buildx() {
  if [[ -n "${TEMPERCI_DISABLE_BUILDX:-}" ]]; then
    return 1
  fi
  if [[ -n "${TEMPERCI_FORCE_BUILDX:-}" ]]; then
    return 0
  fi
  if [[ -x /usr/libexec/docker/cli-plugins/docker-buildx ]] || [[ -x /usr/lib/docker/cli-plugins/docker-buildx ]]; then
    return 0
  fi
  if command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1; then
    return 0
  fi
  return 1
}

# Prints rewritten argv as NUL-separated fields on stdout when a rewrite applies.
# Empty stdout means "run the original command".
temperci_docker_rewrite() {
  local repo="${GITHUB_REPOSITORY:-}"
  if [[ -z "$repo" || "$repo" != */* ]]; then
    return 0
  fi
  if [[ $# -lt 1 ]]; then
    return 0
  fi

  local -a args=("$@")
  local is_build=0
  local is_buildx=0
  if [[ "${args[0]}" == "build" ]]; then
    is_build=1
  elif [[ "${args[0]}" == "buildx" && ${#args[@]} -ge 2 && "${args[1]}" == "build" ]]; then
    is_build=1
    is_buildx=1
  fi
  if [[ "$is_build" -eq 0 ]]; then
    return 0
  fi

  local a
  for a in "${args[@]}"; do
    case "$a" in
      --cache-to|--cache-to=*)
        return 0
        ;;
    esac
  done

  # No buildx: leave docker build alone so the job still compiles.
  if ! temperci_have_buildx; then
    return 0
  fi

  local ref="ghcr.io/__temperci_cache/${repo}/buildkit"
  local from="type=registry,ref=${ref}"
  local mode="${TEMPERCI_BUILD_CACHE_MODE:-min}"
  local to="type=registry,ref=${ref},mode=${mode}"

  if [[ "$is_buildx" -eq 1 ]]; then
    printf '%s\n' "buildx build --cache-from ${from} --cache-to ${to} ${args[*]:2}"
    return 0
  fi
  printf '%s\n' "buildx build --load --cache-from ${from} --cache-to ${to} ${args[*]:1}"
}

# Array-safe rewrite used by the PATH wrapper (avoids word-splitting paths).
temperci_docker_rewrite_args() {
  local -n _out=$1
  shift
  _out=()
  local line
  line="$(temperci_docker_rewrite "$@")"
  if [[ -z "$line" ]]; then
    return 1
  fi
  # shellcheck disable=SC2206
  _out=($line)
  return 0
}

temperci_docker_main() {
  local real="/usr/bin/docker"
  if ! temperci_have_buildx; then
    exec "$real" "$@"
  fi
  local line
  line="$(temperci_docker_rewrite "$@")"
  if [[ -z "$line" ]]; then
    exec "$real" "$@"
  fi
  # Preserve quoted tokens from the rewrite line via eval of a single array
  # assignment — rewrite output is produced by this script, not the user.
  local -a rewritten_args
  # shellcheck disable=SC2206
  rewritten_args=($line)
  exec "$real" "${rewritten_args[@]}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  temperci_docker_main "$@"
fi
