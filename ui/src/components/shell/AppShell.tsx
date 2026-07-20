import type { ReactNode } from "react";
import { TopBar } from "./TopBar";
import { Rail } from "./Rail";
import { StatusBar } from "./StatusBar";

// AppShell is the outer chrome: TopBar / (Rail + content) / StatusBar. The active
// view is rendered as children by App.tsx.
export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div
      className="flex flex-col bg-bg text-text"
      style={{ height: "100vh", overflow: "hidden", fontFamily: "var(--font-body)" }}
    >
      <TopBar />
      <div className="flex" style={{ flex: 1, minHeight: 0 }}>
        <Rail />
        <div className="flex" style={{ flex: 1, minHeight: 0 }}>
          {children}
        </div>
      </div>
      <StatusBar />
    </div>
  );
}
