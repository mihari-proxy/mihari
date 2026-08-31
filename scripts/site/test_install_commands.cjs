"use strict";

const assert = require("node:assert/strict");
const { buildInstallCommand } = require("../../site/install.js");

const githubBase = "https://raw.githubusercontent.com/mihari-proxy/mihari";
const offlineBase = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari";

const cases = [
  ["github", "linux", "main", `curl -fsSL ${githubBase}/main/scripts/install/install.sh | bash`],
  ["github", "macos", "main", `curl -fsSL ${githubBase}/main/scripts/install/install.sh | bash`],
  ["github", "windows", "main", `irm ${githubBase}/main/scripts/install/install.ps1 | iex`],
  ["github", "linux", "dev", `curl -fsSL ${githubBase}/dev/scripts/install/install.sh | bash -s -- --channel dev`],
  ["github", "macos", "dev", `curl -fsSL ${githubBase}/dev/scripts/install/install.sh | bash -s -- --channel dev`],
  ["github", "windows", "dev", `$env:MIHARI_CHANNEL = 'dev'\nirm ${githubBase}/dev/scripts/install/install.ps1 | iex`],
  ["offline", "linux", "main", `curl -fsSL ${offlineBase}/install-aio-remote.sh | bash`],
  ["offline", "macos", "main", `curl -fsSL ${offlineBase}/install-aio-remote.sh | bash`],
  ["offline", "windows", "main", `& ([scriptblock]::Create((irm ${offlineBase}/install-aio-remote.ps1)))`],
  ["offline", "linux", "dev", `curl -fsSL ${offlineBase}/install-aio-remote.sh | bash -s -- --channel dev`],
  ["offline", "macos", "dev", `curl -fsSL ${offlineBase}/install-aio-remote.sh | bash -s -- --channel dev`],
  ["offline", "windows", "dev", `& ([scriptblock]::Create((irm ${offlineBase}/install-aio-remote.ps1))) -Channel dev`],
];

for (const [source, os, channel, expected] of cases) {
  assert.equal(buildInstallCommand({ source, os, channel }), expected, `${source}/${os}/${channel}`);
}

assert.throws(() => buildInstallCommand({ source: "mirror", os: "linux", channel: "main" }), /source/);
assert.throws(() => buildInstallCommand({ source: "github", os: "freebsd", channel: "main" }), /os/);
assert.throws(() => buildInstallCommand({ source: "github", os: "linux", channel: "nightly" }), /channel/);
