import { describe, expect, it } from "vitest";

import { APIError } from "./api";

describe("APIError", () => {
  it("exposes field violations without parsing message text", () => {
    const error = new APIError(401, {
      code: "unauthenticated",
      message: "A second factor is required.",
      retryable: false,
      violations: [{ field: "second_factor", description: "required" }],
    });
    expect(error.hasViolation("second_factor")).toBe(true);
    expect(error.hasViolation("password")).toBe(false);
  });
});
