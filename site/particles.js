(function initializeParticleBackground(root) {
  "use strict";

  if (!root.document || root.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  if (!root.tsParticles || !root.loadSlim) return;

  (async function loadParticles() {
    await root.loadSlim(root.tsParticles);
    await root.tsParticles.load({
      id: "tsparticles",
      options: {
        fullScreen: { enable: false },
        fpsLimit: 60,
        detectRetina: true,
        background: { color: "transparent" },
        particles: {
          number: {
            value: 54,
            density: { enable: true, width: 1200, height: 900 }
          },
          color: { value: ["#52525b", "#71717a", "#22c55e", "#eab308"] },
          shape: { type: "circle" },
          opacity: { value: { min: 0.12, max: 0.42 } },
          size: { value: { min: 1, max: 2.4 } },
          links: {
            enable: true,
            distance: 150,
            color: "#52525b",
            opacity: 0.13,
            width: 1
          },
          move: {
            enable: true,
            speed: 0.48,
            direction: "none",
            random: true,
            straight: false,
            outModes: { default: "out" }
          }
        },
        interactivity: {
          events: {
            onHover: { enable: true, mode: "grab" },
            onClick: { enable: false }
          },
          modes: {
            grab: { distance: 130, links: { opacity: 0.25 } }
          }
        }
      }
    });
  }()).catch(function markParticleFailure() {
    const host = root.document.getElementById("tsparticles");
    if (host) host.dataset.loadFailed = "true";
  });
}(typeof globalThis !== "undefined" ? globalThis : this));
