import { describe, expect, it } from "vitest";

import { productName } from "./config";

describe("product configuration", () => {
  it("uses the canonical product name", () => {
    expect(productName).toBe("Relayward");
  });
});
