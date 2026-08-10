import { describe, expect, it } from "vitest";

import { timezoneOptions } from "./timezones";

describe("timezoneOptions", () => {
  it("returns sorted unique options including UTC", () => {
    const values = timezoneOptions().map((option) => option.value);
    expect(values).toContain("UTC");
    expect(values).toEqual([...new Set(values)].sort((left, right) => left.localeCompare(right)));
  });

  it("preserves a configured timezone outside the browser list", () => {
    expect(timezoneOptions("Custom/Existing")).toContainEqual({
      value: "Custom/Existing",
      label: "Custom/Existing",
    });
  });
});
