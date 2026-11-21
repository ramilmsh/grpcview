// The one Apple-platform detector: macOS, iOS and iPadOS (whose Safari reports
// "Macintosh") all map "Mod" to ⌘ (metaKey); everything else to Ctrl.
export interface NavigatorLike {
  userAgentData?: { platform?: string };
  platform?: string;
  userAgent?: string;
}

const APPLE = /mac|iphone|ipad|ipod/i;

// Any one signal is enough: some browsers freeze `navigator.platform`, and only
// Chromium has userAgentData (whose platform is "macOS").
export function isApplePlatform(nav: NavigatorLike | undefined): boolean {
  if (!nav) return false;
  return [nav.userAgentData?.platform, nav.platform, nav.userAgent].some(
    (s) => !!s && APPLE.test(s),
  );
}

function detectIsMac(): boolean {
  // Node 21+ ships its own global `navigator` with a host-derived userAgent and
  // platform, so the Node/Electron check has to come first.
  if (
    typeof process !== "undefined" &&
    typeof process.versions?.node === "string"
  ) {
    return process.platform === "darwin";
  }
  if (typeof navigator === "undefined") return false;
  return isApplePlatform(navigator as NavigatorLike);
}

export const IS_MAC = detectIsMac();
