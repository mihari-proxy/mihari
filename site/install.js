(function attachInstallBuilder(root) {
  "use strict";

  const GITHUB_BASE = "https://raw.githubusercontent.com/mihari-proxy/mihari";
  const OFFLINE_BASE = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari";
  const VALID = {
    source: new Set(["github", "offline"]),
    os: new Set(["linux", "macos", "windows"]),
    channel: new Set(["main", "dev"]),
  };

  function validateChoice(kind, value) {
    if (!VALID[kind].has(value)) {
      throw new RangeError(`${kind} is not supported`);
    }
  }

  function buildInstallCommand(selection) {
    const { source, os, channel } = selection;
    validateChoice("source", source);
    validateChoice("os", os);
    validateChoice("channel", channel);

    const windows = os === "windows";
    const development = channel === "dev";

    if (source === "github") {
      if (windows) {
        const install = `irm ${GITHUB_BASE}/${channel}/scripts/install/install.ps1 | iex`;
        return development ? `$env:MIHARI_CHANNEL = 'dev'\n${install}` : install;
      }
      const install = `curl -fsSL ${GITHUB_BASE}/${channel}/scripts/install/install.sh | bash`;
      return development ? `${install} -s -- --channel dev` : install;
    }

    if (windows) {
      const install = `& ([scriptblock]::Create((irm ${OFFLINE_BASE}/install-aio-remote.ps1)))`;
      return development ? `${install} -Channel dev` : install;
    }
    const install = `curl -fsSL ${OFFLINE_BASE}/install-aio-remote.sh | bash`;
    return development ? `${install} -s -- --channel dev` : install;
  }

  function mountInstallBuilder(documentRef) {
    const builder = documentRef.querySelector("[data-install-builder]");
    if (!builder) return;

    const selection = { source: "github", os: "linux", channel: "main" };
    const output = builder.querySelector("[data-install-command]");
    const shell = builder.querySelector("[data-install-shell]");
    const note = builder.querySelector("[data-install-note]");
    const copy = builder.querySelector("[data-copy-command]");

    function render() {
      for (const kind of Object.keys(selection)) {
        builder.querySelectorAll(`[data-install-${kind}]`).forEach(function updateChoice(button) {
          const selected = button.dataset[`install${kind[0].toUpperCase()}${kind.slice(1)}`] === selection[kind];
          button.classList.toggle("is-selected", selected);
          button.setAttribute("aria-pressed", String(selected));
        });
      }

      output.textContent = buildInstallCommand(selection);
      shell.textContent = selection.os === "windows" ? "PowerShell" : "Shell";
      note.textContent = selection.source === "offline"
        ? builder.dataset.offlineNote
        : builder.dataset.githubNote;
    }

    for (const kind of Object.keys(selection)) {
      builder.querySelectorAll(`[data-install-${kind}]`).forEach(function bindChoice(button) {
        button.addEventListener("click", function choose() {
          selection[kind] = button.dataset[`install${kind[0].toUpperCase()}${kind.slice(1)}`];
          render();
        });
      });
    }

    copy.addEventListener("click", async function copyCommand() {
      const original = copy.dataset.copyLabel;
      try {
        await root.navigator.clipboard.writeText(output.textContent);
        copy.textContent = copy.dataset.copySuccess;
      } catch (_) {
        const range = documentRef.createRange();
        range.selectNodeContents(output);
        const selectionRef = root.getSelection();
        selectionRef.removeAllRanges();
        selectionRef.addRange(range);
        copy.textContent = copy.dataset.copyFallback;
      }
      root.setTimeout(function resetCopyLabel() {
        copy.textContent = original;
      }, 1800);
    });

    render();
  }

  const api = { buildInstallCommand, mountInstallBuilder };
  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  }
  root.MihariInstall = api;

  if (root.document) {
    if (root.document.readyState === "loading") {
      root.document.addEventListener("DOMContentLoaded", function startInstallBuilder() {
        mountInstallBuilder(root.document);
      }, { once: true });
    } else {
      mountInstallBuilder(root.document);
    }
  }
}(typeof globalThis !== "undefined" ? globalThis : this));
