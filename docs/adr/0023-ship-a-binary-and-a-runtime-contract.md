# ADR 0023: Ship a binary and a runtime contract; packaging is the deployer's

**Status:** Accepted (2026-10-20)

## Context
Two deployment shapes have been written down in this repo, and they disagree:

- the [fleet-deployment spec](../specs/2026-08-09-fleet-deployment.md) chose
  "podman + quadlet" as the runtime;
- what shipped ([`deploy-a-collector.md`](../how-to/deploy-a-collector.md))
  is a native binary under a hardened systemd unit.

Neither is wrong. The disagreement shows that the project has been
choosing a packaging format when it does not have to. Packaging (container
or native, which init system, how updates roll out across a fleet) is tied
to the fleet control plane, which this repo does not build
([ADR 0012](0012-host-executed-updates.md)), and to each operator's hosts,
which this project cannot see. Every format the project owns is one it has
to build, test and support.

The collector is already easy to package: a static binary, configured by one
file and a few flags, keeping its state in the broker rather than on disk.
What a packager lacks is the contract that says so in one place.

## Decision
1. **The release artifact is the binary.** Each release ships static
   `linux/amd64` and `linux/arm64` executables in tarballs, with checksums.
   No container image, OS package or other format is a project deliverable.
2. **The project owns a runtime contract**, written down in
   [`reference/runtime-contract.md`](../reference/runtime-contract.md):
   - inputs, files, privileges and network use;
   - logs, the health endpoints and exit codes;
   - signals and shutdown bounds;
   - how updates are done.

   Changing it is a breaking change for packagers.
3. **Packaging is the deployer's choice.** A systemd unit, a container, a
   `.deb`, a fleet agent: anything that honours the contract is supported in
   the same sense.
4. **The systemd unit and the deploy guide stay, as a reference recipe.**
   They are the worked example that proves the contract on a real host: running
   them is how the unit's unreadable-config bug was found. They are not the
   only supported shape, and other recipes may be added the same way, as
   examples.

## Consequences
- The fleet spec's runtime choice becomes the fleet plane's decision, out
  of scope here. The spec says so and points at the contract.
- A request for a Dockerfile, an image or a package is answered with the
  contract first. One can still be added as a reference recipe if it adds
  something the contract cannot, but it is not a supported deliverable.
- Anything that would make packaging harder must be weighed as a contract
  change. That includes writing to disk, reading environment variables,
  needing a privilege, or changing an exit code.

## Alternatives considered
- **Ship a container image as the primary artifact.** Rejected for now: the
  cabinet hosts run a single workload, and an image adds a registry, image
  signing and a base-image patch cycle for no gain the binary lacks. A
  deployment that wants containers can build a trivial image from the binary.
- **Ship OS packages (`.deb`/`.rpm`).** Deferred until an operator asks.
  GoReleaser can build them from the same build, and doing so would not
  change this decision.
