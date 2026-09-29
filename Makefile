# Canonical repo name from git remote (portable across worktrees and containers)
REPO_NAME := $(shell git remote get-url origin 2>/dev/null | sed 's|.*/||; s|\.git$$||')

# This project nests issues and history under workshop/
WF_ISSUES_DIR = workshop/issues
WF_HISTORY_DIR = workshop/history

.DEFAULT_GOAL := help
-include Makefile.workflow
-include Makefile.local

.PHONY: help
help: $(WF_HELP_TARGETS)
	@true

# This layer's tools, built by `weave compile` (foundation first) and put on
# dependents' PATH: the brain repos run `nous` from here. nous-build is
# Makefile.nous's (via Makefile.local), the one recipe that builds the binary.
.PHONY: tools
tools: nous-build
