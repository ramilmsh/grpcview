import type { ReactNode } from "react";
import { TopBar } from "./TopBar";
import { Rail } from "./Rail";
import { StatusBar } from "./StatusBar";

// v4: the ground fills the window; the app is one rounded, raised card inset 12px into
// it. Header, rail, sidebars and status bar all sit on the shell colour, borderless —
// only each view's MainPanel lifts off it.
export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div
      className="flex text-text"
      style={{
        height: "100vh",
        boxSizing: "border-box",
        padding: 12,
        overflow: "hidden",
        background: "var(--v-ground)",
        fontFamily: "var(--font-body)",
      }}
    >
      <div
        className="flex flex-col"
        style={{
          flex: 1,
          minWidth: 0,
          minHeight: 0,
          overflow: "hidden",
          borderRadius: "var(--v-r-app)",
          background: "var(--v-shell)",
          boxShadow: "var(--v-raise)",
        }}
      >
        <TopBar />
        {/* minWidth:0 all the way down: without it a flex row sizes to its widest
            descendant, so one long service name or source id widens the whole shell
            past the viewport instead of truncating. */}
        <div className="flex" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <Rail />
          <div className="flex" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
            {children}
          </div>
        </div>
        <StatusBar />
      </div>
    </div>
  );
}
