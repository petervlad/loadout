---
name: zuul-triage
description: >-
  Find out why a CI build failed, e.g. a POST_FAILURE or a timeout. Use when
  the user pastes a build URL or asks why a job is red.
allowed-tools: Bash(curl:*) Read Grep
metadata:
  owner: ci-team
  version: "2.1"
compatibility: Needs curl and access to the CI server.
---
# CI triage

1. Fetch the build with the API.
2. Read job-output.txt from the end; see references/errors.md for known patterns.
