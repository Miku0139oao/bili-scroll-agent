// Minimal jsdom globals for component tests under `bun test`.
//
// Bun's test runner does not honour vite.config.ts's `test.environment`, so the
// DOM is installed here.
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://127.0.0.1:8788/",
  pretendToBeVisual: true,
});

const globals = globalThis as unknown as Record<string, unknown>;
globals.window = dom.window;
globals.document = dom.window.document;
globals.navigator = dom.window.navigator;
globals.HTMLElement = dom.window.HTMLElement;
globals.Element = dom.window.Element;
globals.Node = dom.window.Node;
globals.localStorage = dom.window.localStorage;

// jsdom does not implement window.confirm; components call it directly.
(dom.window as unknown as { confirm: () => boolean }).confirm = () => false;
(globalThis as { confirm: unknown }).confirm = () => false;
