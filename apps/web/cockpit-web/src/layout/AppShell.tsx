import {
  Activity,
  Building2,
  ClipboardList,
  FileClock,
  LogOut,
  PanelLeftClose,
  Play,
  Workflow,
  SlidersHorizontal,
  Wifi,
  WifiOff,
} from "lucide-react";
import { useState, type PropsWithChildren } from "react";
import { useAuth } from "../auth/AuthContext";
import { IconButton } from "../components/IconButton";
import { AppLink, usePathname } from "../routing/router";
import { useRealtimeStatus } from "../realtime/RealtimeContext";

const navigation = [
  { to: "/", label: "Work queue", icon: ClipboardList },
  { to: "/start", label: "Start workflow", icon: Play },
  { to: "/cases", label: "Cases", icon: Workflow },
  { to: "/audit", label: "Audit", icon: FileClock },
  { to: "/organizations", label: "Organizations", icon: Building2 },
  { to: "/configuration", label: "Configuration", icon: SlidersHorizontal },
];

export function AppShell({ children }: PropsWithChildren) {
  const [compact, setCompact] = useState(false);
  const { identity, disconnect } = useAuth();
  const realtimeStatus = useRealtimeStatus();
  const pathname = usePathname();
  return (
    <div className={`app-shell ${compact ? "app-shell--compact" : ""}`}>
      <aside className="sidebar">
        <div className="brand">
          <span className="brand__mark"><Activity size={18} /></span>
          <strong>BPMP</strong>
          <span>Cockpit</span>
        </div>
        <nav aria-label="Primary">
          {navigation.map(({ to, label, icon: Icon }) => (
            <AppLink
              key={to}
              to={to}
              className={pathname === to ? "active" : undefined}
              aria-label={label}
            >
              <Icon size={18} aria-hidden="true" />
              <span>{label}</span>
            </AppLink>
          ))}
        </nav>
        <div className="sidebar__footer">
          <IconButton
            icon={PanelLeftClose}
            label={compact ? "Expand navigation" : "Collapse navigation"}
            onClick={() => setCompact((value) => !value)}
          />
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <span
            className={`realtime-status realtime-status--${realtimeStatus}`}
            title={`Realtime: ${realtimeStatus}`}
            aria-label={`Realtime ${realtimeStatus}`}
            role="status"
          >
            {realtimeStatus === "connected"
              ? <Wifi size={16} aria-hidden="true" />
              : <WifiOff size={16} aria-hidden="true" />}
          </span>
          <div className="tenant-chip">
            <span>{identity?.tenantId}</span>
          </div>
          <IconButton icon={LogOut} label="Disconnect" onClick={disconnect} />
        </header>
        <div className="page">{children}</div>
      </main>
    </div>
  );
}
