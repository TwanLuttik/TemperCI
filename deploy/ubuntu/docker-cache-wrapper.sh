#!/usr/bin/env bash
# Guest PATH wrapper: /usr/local/bin/docker
# Adds BuildKit registry cache flags for docker build / docker buildx build.
# Sourced by docker-cache-wrapper_test.sh (defines temperci_docker_rewrite only).

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

  local ref="ghcr.io/__temperci_cache/${repo}/buildkit"
  local from="type=registry,ref=${ref}"
  local to="type=registry,ref=${ref},mode=max"

  if [[ "$is_buildx" -eq 1 ]]; then
    # buildx build <flags...>
    printf '%s\n' "buildx build --cache-from ${from} --cache-to ${to} ${args[*]:2}"
    return 0
  fi
  printf '%s\n' "buildx build --load --cache-from ${from} --cache-to ${to} ${args[*]:1}"
}

temperci_docker_main() {
  local real="/usr/bin/docker"
  local rewritten
  rewritten="$(temperci_docker_rewrite "$@")"
  if [[ -z "$rewritten" ]]; then
    exec "$real" "$@"
  fi
  # shellcheck disable=SC2206
  local -a rewritten_args=($rewritten)
  exec "$real" "${rewritten_args[@]}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  temperci_docker_main "$@"
fi
