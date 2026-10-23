#!/bin/sh
set -eu

if ! description=$(git -C "${1:-.}" describe --tags --match 'client/v[0-9]*' --dirty 2>/dev/null); then
	printf '%s\n' dev
	exit 0
fi

version=${description#client/v}
# Only stable CLI versions, Git commit-distance suffixes and dirty markers may
# enter the linker flags. Other tags cannot become shell syntax in a Make recipe.
if printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9]+-g[0-9a-f]+)?(-dirty)?$'; then
	printf '%s\n' "$version"
else
	printf '%s\n' dev
fi
