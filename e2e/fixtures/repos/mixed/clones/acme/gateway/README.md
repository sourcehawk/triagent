# acme/gateway

Edge routing and auth termination. This file is the seed content the e2e
harness turns into a local git clone under GitCacheDir so the regenerate
worker's EnsureClone (git fetch against a local origin) succeeds offline,
and the summary sub-agent has a working tree to run in.
