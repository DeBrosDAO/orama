import { describe, expect, it } from "vitest";
import { toError } from "./errors";

describe("toError", () => {
  it("TestToError_keeps_an_error_and_its_type", () => {
    const original = new RangeError("out of range");
    expect(toError(original)).toBe(original);
  });

  it("TestToError_wraps_strings_and_other_values", () => {
    expect(toError("indexer down").message).toBe("indexer down");
    expect(toError(undefined).message).toBe("undefined");
    expect(toError({ code: 500 })).toBeInstanceOf(Error);
  });
});
