# Canonical repo name from git remote
REPO_NAME := $(shell git remote get-url origin 2>/dev/null | sed 's|.*/||; s|\.git$$||')

# Issue/history paths (override before include if non-standard)
WF_ISSUES_DIR = workshop/issues
WF_HISTORY_DIR = workshop/history

# Include ariadne workflow targets
include Makefile.workflow

# Include local targets (repo-specific). nous-specific concerns
# (UPSTREAM_* overrides for self-refresh via nous/setup.sh,
# Makefile.nous chain) live in Makefile.local so the root Makefile
# can be a uniform ariadne-vendored template.
-include Makefile.local

.PHONY: help

help: help-workflow help-sandbox help-tart
	@true
