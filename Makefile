# Canonical repo name from git remote
REPO_NAME := $(shell git remote get-url origin 2>/dev/null | sed 's|.*/||; s|\.git$$||')

# Issue/history paths (override before include if non-standard)
WF_ISSUES_DIR = workshop/issues
WF_HISTORY_DIR = workshop/history

# Self-refresh: when run inside nous, route make refresh through nous's own
# setup script (which re-vendors the ariadne base layer from ../ariadne).
UPSTREAM_NAME    := nous
UPSTREAM_REFRESH := nous/setup.sh

# Include ariadne workflow targets
include Makefile.workflow

# Include local targets (repo-specific)
-include Makefile.local

.PHONY: help

help: help-workflow
	@true
