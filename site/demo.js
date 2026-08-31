(function attachMihariDemo(root) {
  "use strict";

  function createDemoController(options) {
    const {
      pageCount,
      intervalMs = 3600,
      manualPauseMs = 10000,
      setTimer = root.setTimeout.bind(root),
      clearTimer = root.clearTimeout.bind(root),
      onChange = function noop() {},
      onPauseChange = function noop() {},
    } = options;

    if (!Number.isInteger(pageCount) || pageCount < 1) {
      throw new TypeError("pageCount must be a positive integer");
    }

    let index = 0;
    let autoTimer = null;
    let resumeTimer = null;
    let running = false;

    function clearAutoTimer() {
      if (autoTimer !== null) {
        clearTimer(autoTimer);
        autoTimer = null;
      }
    }

    function clearResumeTimer() {
      if (resumeTimer !== null) {
        clearTimer(resumeTimer);
        resumeTimer = null;
      }
    }

    function scheduleAuto() {
      clearAutoTimer();
      if (!running) return;
      autoTimer = setTimer(function advanceAutomatically() {
        autoTimer = null;
        index = (index + 1) % pageCount;
        onChange(index, "auto");
        scheduleAuto();
      }, intervalMs);
    }

    function start() {
      if (running) return;
      running = true;
      scheduleAuto();
    }

    function stop() {
      running = false;
      clearAutoTimer();
      clearResumeTimer();
      onPauseChange(false);
    }

    function select(nextIndex, manual) {
      if (!Number.isInteger(nextIndex) || nextIndex < 0 || nextIndex >= pageCount) {
        throw new RangeError("page index is out of range");
      }

      index = nextIndex;
      onChange(index, manual ? "manual" : "programmatic");
      clearAutoTimer();
      clearResumeTimer();

      if (!manual) {
        scheduleAuto();
        return;
      }

      onPauseChange(true);
      resumeTimer = setTimer(function resumeAfterManualSelection() {
        resumeTimer = null;
        onPauseChange(false);
        if (!running) return;
        index = (index + 1) % pageCount;
        onChange(index, "auto");
        scheduleAuto();
      }, manualPauseMs);
    }

    return {
      start,
      stop,
      select,
      getIndex: function getIndex() { return index; },
    };
  }

  function mountDemo(documentRef) {
    const demo = documentRef.querySelector("[data-demo-root]");
    if (!demo) return null;

    const controls = Array.from(demo.querySelectorAll("[data-demo-target]"));
    const panels = Array.from(demo.querySelectorAll("[data-demo-page]"));
    const mode = demo.querySelector("[data-demo-mode]");

    function render(index, reason) {
      controls.forEach(function updateControl(control, controlIndex) {
        const active = controlIndex === index;
        control.classList.toggle("is-active", active);
        control.setAttribute("aria-selected", String(active));
        control.tabIndex = active ? 0 : -1;
      });

      panels.forEach(function updatePanel(panel, panelIndex) {
        const active = panelIndex === index;
        panel.classList.toggle("is-active", active);
        panel.setAttribute("aria-hidden", String(!active));
      });

      demo.dataset.changeReason = reason;
    }

    const controller = createDemoController({
      pageCount: panels.length,
      intervalMs: 3600,
      manualPauseMs: 10000,
      onChange: render,
      onPauseChange: function updatePauseState(paused) {
        demo.classList.toggle("is-manual", paused);
        if (mode) {
          mode.textContent = paused ? "manual hold · resumes in 10s" : "autoplay · 3.6s per page";
        }
      },
    });

    controls.forEach(function bindControl(control, index) {
      control.addEventListener("click", function selectPage() {
        controller.select(index, true);
      });

      control.addEventListener("keydown", function moveFocus(event) {
        let next = null;
        if (event.key === "ArrowDown" || event.key === "ArrowRight") next = (index + 1) % controls.length;
        if (event.key === "ArrowUp" || event.key === "ArrowLeft") next = (index - 1 + controls.length) % controls.length;
        if (event.key === "Home") next = 0;
        if (event.key === "End") next = controls.length - 1;
        if (next === null) return;
        event.preventDefault();
        controls[next].focus();
        controller.select(next, true);
      });
    });

    render(0, "initial");
    controller.start();
    return controller;
  }

  const api = { createDemoController, mountDemo };
  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  }
  root.MihariDemo = api;

  if (root.document) {
    if (root.document.readyState === "loading") {
      root.document.addEventListener("DOMContentLoaded", function startDemo() {
        mountDemo(root.document);
      }, { once: true });
    } else {
      mountDemo(root.document);
    }
  }
}(typeof globalThis !== "undefined" ? globalThis : this));
