import type { ReactNode } from "react";
import { TopBar } from "./TopBar";
import { Rail } from "./Rail";
import { StatusBar } from "./StatusBar";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div
      className="flex flex-col bg-bg text-text"
      style={{ height: "100vh", overflow: "hidden", fontFamily: "var(--font-body)" }}
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
  );
}
