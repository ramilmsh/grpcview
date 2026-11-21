import { describe, it } from "node:test";
import { expect } from "expect";
import { isApplePlatform } from "./platform";

describe("isApplePlatform", () => {
  it("is true for macOS, iOS and iPadOS by any one signal", () => {
    expect(isApplePlatform({ userAgentData: { platform: "macOS" } })).toBe(
      true,
    );
    expect(isApplePlatform({ platform: "MacIntel" })).toBe(true);
    expect(isApplePlatform({ platform: "iPhone" })).toBe(true);
    expect(isApplePlatform({ platform: "iPad" })).toBe(true);
    expect(isApplePlatform({ userAgent: "Mozilla/5.0 (iPod touch)" })).toBe(
      true,
    );
    // iPadOS Safari: desktop-class UA, "Macintosh".
    expect(
      isApplePlatform({
        platform: "",
        userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
      }),
    ).toBe(true);
  });
  it("is false for Windows, Linux, Android and no navigator", () => {
    expect(
      isApplePlatform({
        userAgentData: { platform: "Windows" },
        platform: "Win32",
        userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
      }),
    ).toBe(false);
    expect(
      isApplePlatform({
        platform: "Linux x86_64",
        userAgent: "Mozilla/5.0 (X11; Linux x86_64)",
      }),
    ).toBe(false);
    expect(
      isApplePlatform({
        userAgentData: { platform: "Android" },
        userAgent: "Mozilla/5.0 (Linux; Android 14)",
      }),
    ).toBe(false);
    expect(isApplePlatform(undefined)).toBe(false);
  });
});
