"use strict";

const assert = require("node:assert/strict");
const { createDemoController } = require("../../site/demo.js");

class FakeClock {
  constructor() {
    this.now = 0;
    this.nextID = 1;
    this.tasks = new Map();
  }

  setTimeout(callback, delay) {
    const id = this.nextID++;
    this.tasks.set(id, { at: this.now + delay, callback });
    return id;
  }

  clearTimeout(id) {
    this.tasks.delete(id);
  }

  tick(duration) {
    const end = this.now + duration;
    while (true) {
      const due = [...this.tasks.entries()]
        .filter(([, task]) => task.at <= end)
        .sort((left, right) => left[1].at - right[1].at || left[0] - right[0])[0];
      if (!due) break;
      const [id, task] = due;
      this.tasks.delete(id);
      this.now = task.at;
      task.callback();
    }
    this.now = end;
  }
}

const clock = new FakeClock();
const changes = [];
const pauses = [];
const controller = createDemoController({
  pageCount: 3,
  intervalMs: 3600,
  manualPauseMs: 10000,
  setTimer: clock.setTimeout.bind(clock),
  clearTimer: clock.clearTimeout.bind(clock),
  onChange: (index, reason) => changes.push([index, reason]),
  onPauseChange: (paused) => pauses.push(paused),
});

controller.start();
clock.tick(3599);
assert.deepEqual(changes, []);
clock.tick(1);
assert.deepEqual(changes, [[1, "auto"]]);

controller.select(2, true);
assert.deepEqual(changes.at(-1), [2, "manual"]);
assert.equal(pauses.at(-1), true);
clock.tick(9999);
assert.deepEqual(changes.at(-1), [2, "manual"]);
clock.tick(1);
assert.deepEqual(changes.at(-1), [0, "auto"]);
assert.equal(pauses.at(-1), false);

controller.select(1, true);
clock.tick(5000);
controller.select(2, true);
clock.tick(9999);
assert.deepEqual(changes.at(-1), [2, "manual"]);
clock.tick(1);
assert.deepEqual(changes.at(-1), [0, "auto"]);

controller.stop();
assert.equal(clock.tasks.size, 0);
