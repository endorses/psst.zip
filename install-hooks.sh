#!/bin/sh
# Install tracked hooks without replacing an existing hook configuration.
set -eu
script_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(git -C "$script_root" rev-parse --show-toplevel)
if [ "$script_root" != "$repository_root" ]; then
    echo 'Run the installer from the repository root.' >&2
    exit 1
fi
command -v python3 >/dev/null 2>&1 || {
    echo 'Install Python 3 before installing repository hooks.' >&2
    exit 1
}
command -v gitleaks >/dev/null 2>&1 || {
    echo 'Install Gitleaks before installing repository hooks.' >&2
    exit 1
}
desired_path="$repository_root/hooks"
existing_path=$(git -C "$repository_root" config --get core.hooksPath || true)
if [ -n "$existing_path" ] && [ "$existing_path" != "$desired_path" ]; then
    echo 'An existing core.hooksPath is configured; reconcile it before installing.' >&2
    exit 1
fi
common_path=$(git -C "$repository_root" rev-parse --git-common-dir)
default_path="$common_path/hooks"
case "$default_path" in
    /*) ;;
    *) default_path="$repository_root/$default_path" ;;
esac
for hook in pre-commit pre-push; do
    if [ -e "$default_path/$hook" ] || [ -L "$default_path/$hook" ]; then
        echo "Existing $hook hook found; reconcile it before installing." >&2
        exit 1
    fi
    if [ ! -x "$desired_path/$hook" ]; then
        echo "Tracked $hook hook is missing or not executable." >&2
        exit 1
    fi
done
git -C "$repository_root" config --local core.hooksPath "$desired_path"
echo 'Installed psst.zip pre-commit and pre-push checks.'
