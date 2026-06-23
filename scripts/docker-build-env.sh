#!/bin/sh
set -eu

quote_sh() {
	value=$1
	printf "'%s'" "$(printf '%s' "$value" | sed "s/'/'\\\\''/g")"
}

go_env_or_default() {
	name=$1
	default=$2
	value=$(go env "$name" 2>/dev/null || true)
	if [ -n "$value" ]; then
		printf '%s' "$value"
		return
	fi
	printf '%s' "$default"
}

normalize_host_proxy() {
	value=$1
	if [ -z "$value" ]; then
		return
	fi
	printf '%s' "$value" |
		sed \
			-e 's#://127\.0\.0\.1:#://host.docker.internal:#g' \
			-e 's#://localhost:#://host.docker.internal:#g'
}

print_assignment() {
	name=$1
	value=$2
	printf '%s=' "$name"
	quote_sh "$value"
	printf '\n'
}

goproxy=${DOCKER_BUILD_GOPROXY:-${GOPROXY:-$(go_env_or_default GOPROXY "https://proxy.golang.org,direct")}}
gosumdb=${DOCKER_BUILD_GOSUMDB:-${GOSUMDB:-$(go_env_or_default GOSUMDB "sum.golang.org")}}

http_proxy_upper=$(normalize_host_proxy "${DOCKER_BUILD_HTTP_PROXY:-${HTTP_PROXY:-}}")
https_proxy_upper=$(normalize_host_proxy "${DOCKER_BUILD_HTTPS_PROXY:-${HTTPS_PROXY:-}}")
http_proxy_lower=$(normalize_host_proxy "${DOCKER_BUILD_http_proxy:-${http_proxy:-}}")
https_proxy_lower=$(normalize_host_proxy "${DOCKER_BUILD_https_proxy:-${https_proxy:-}}")
no_proxy_upper=${DOCKER_BUILD_NO_PROXY:-${NO_PROXY:-}}
no_proxy_lower=${DOCKER_BUILD_no_proxy:-${no_proxy:-}}

print_assignment DOCKER_BUILD_GOPROXY "$goproxy"
print_assignment DOCKER_BUILD_GOSUMDB "$gosumdb"
print_assignment DOCKER_BUILD_HTTP_PROXY "$http_proxy_upper"
print_assignment DOCKER_BUILD_HTTPS_PROXY "$https_proxy_upper"
print_assignment DOCKER_BUILD_NO_PROXY "$no_proxy_upper"
print_assignment DOCKER_BUILD_http_proxy "$http_proxy_lower"
print_assignment DOCKER_BUILD_https_proxy "$https_proxy_lower"
print_assignment DOCKER_BUILD_no_proxy "$no_proxy_lower"
