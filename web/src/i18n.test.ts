import { describe, expect, it } from "vitest";

import { tr } from "./i18n";

describe("translations", () => {
  it("uses Traditional Chinese by default", () => {
    expect(tr("zh-TW", "app")).toBe("刷 B 站");
  });

  it("falls back to a complete locale when a key is missing", () => {
    expect(tr("en", "usage")).toBe("Usage & balance");
  });
});
