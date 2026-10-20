# Record fixtures from a device

**At the end** you will have a fixture for your adapter's golden tests made
from what a real device actually returned, with a comment saying where it
came from — the bar
["No fixtures, no merge"](../reference/invariants.md#no-fixtures-no-merge)
(ADR 0008) holds an adapter PR to.

**You need** the device (or a simulator that faithfully reproduces one) on
a network you can reach, its read community, and the list of OIDs your
adapter's `Read` requests. These steps are for SNMP, the one transport that
ships today; another transport follows the same steps with its own capture
tool.

There is no recording tool in this repo yet. `snmptest.Static` replays a
map you give it; it does not capture one. So recording is a manual job, and
the provenance comment in step 4 is what makes it count. Why the comment,
not the file format, is what a reviewer checks:
[`testing-strategy.md`'s "Provenance is a review question, not a test"](../explanation/testing-strategy.md#provenance-is-a-review-question-not-a-test).

## Steps

1. **Capture exactly what your adapter asks for.** GET the same OIDs your
   `Read` requests, with the SNMP version your adapter uses, for example
   with net-snmp:

   ```bash
   snmpget -v1 -c <community> -On <host> <oid> <oid> ...
   ```

   Use GET by explicit OID, not a walk, if that is what the adapter does —
   some agents behave differently for walks (`internal/vendors/ntcip/dms.go`
   documents one that errors on them). Note the device make, model and
   firmware version while you are there.
2. **Capture the cases your tests need.** A golden test wants a healthy
   poll. If you can put the device into the other states your adapter
   decodes (flash, a fault, a blank sign), capture those too. An OID the
   device does not answer is a result worth keeping: leaving it out of the
   map is how `snmptest.Static` replays "no such object".
3. **Transcribe into the test file.** Integers go in `snmptest.Static`'s
   `Values` (`map[string]int64`), octet strings in `Octets`
   (`map[string][]byte`), keyed by numeric OID with a leading dot. Copy the
   values exactly as returned; do not round, rescale or tidy them.
4. **Write the provenance comment** directly above the fixture: what you
   captured, from which make, model and firmware (or which simulator), how
   (GET or walk, SNMP version), and the date. Mark any value you could not
   capture and wrote by hand as synthetic, and say why.
   `healthyDMSInts` in `internal/vendors/ntcip/dms_test.go` is the worked
   example.
5. **Scrub before you commit.** Remove anything that identifies the
   deployment: device addresses, community strings, site and agency names.
   Values the adapter decodes stay as recorded.
6. **Optionally, keep a live test.** `internal/vendors/ntcip/dms_live_test.go`
   reads a real sign only when `DMS_SNMP_ADDR` is set and skips otherwise,
   so CI never needs the device and the address never enters the repo. It
   is a cheap way to re-check the mapping against the hardware later.

Then write the golden and failure tests on top of the fixture, as
[`add-a-vendor-adapter.md`'s "Meet the test bar"](add-a-vendor-adapter.md#meet-the-test-bar)
describes.

## Not yet available

A fixture recorder and adapter conformance kit — a tool that captures real
responses into `testdata/` with a sanctioned way to regenerate them — is
planned work, listed in
[`docs/README.md`'s known gaps](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state).
Until it exists, the steps above are the process. `ntcip-asc`'s fixtures
predate this bar and were hand-typed; do not copy them as a model.
