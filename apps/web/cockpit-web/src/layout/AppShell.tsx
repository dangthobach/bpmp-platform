import {
  Activity,
  Building2,
  ClipboardList,
  Code2,
  FileClock,
  LogOut,
  KeyRound,
  LayoutGrid,
  BarChart3,
  Settings2,
  PanelLeftClose,
  Play,
  Workflow,
  Users,
  Database,
  Wifi,
  WifiOff,
} from "lucide-react";
import { useState, type PropsWithChildren } from "react";
import { useAuth } from "../auth/AuthContext";
import { IconButton } from "../components/IconButton";
import { AppLink, usePathname } from "../routing/router";
import { useRealtimeStatus } from "../realtime/RealtimeContext";

const navigation = [
  { section: "Workspace", to: "/portal", label: "Client Portal", icon: Building2 },
  { section: "Workspace", to: "/modeler", label: "Modeler", icon: Code2 },
  { section: "Workspace", to: "/operation", label: "Operation", icon: Workflow },
  { section: "Workspace", to: "/optimize", label: "Optimize", icon: BarChart3 },
  { section: "Workspace", to: "/tasklist", label: "Tasklist", icon: ClipboardList },
  { section: "Administration", to: "/identity", label: "Identity", icon: Users },
  { section: "Administration", to: "/access", label: "Access", icon: KeyRound },
  { section: "Administration", to: "/task-management", label: "Task Management", icon: Settings2 },
  { section: "Administration", to: "/foundation", label: "Foundation", icon: LayoutGrid },
  { section: "Administration", to: "/audit", label: "Audit log", icon: FileClock },
  { section: "Administration", to: "/processes", label: "Process explorer", icon: Database },
  { section: "Administration", to: "/organizations", label: "Organization tree", icon: Building2 },
  { section: "Administration", to: "/configuration", label: "Configuration", icon: Settings2 },
  { section: "Administration", to: "/start", label: "Start workflow", icon: Play },
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
          {(["Workspace", "Administration"] as const).map((section) => (
            <div className="nav-section" key={section}>
              {!compact ? <div className="nav-section__label">{section}</div> : null}
              {navigation.filter((item) => item.section === section).map(({ to, label, icon: Icon }) => (
                <AppLink
                  key={to}
                  to={to}
                  className={pathname === to ? "active" : undefined}
                  aria-label={label}
                  title={compact ? label : undefined}
                >
                  <Icon size={17} aria-hidden="true" />
                  <span>{label}</span>
                </AppLink>
              ))}
            </div>
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
          <div className="topbar__context">
            <span className="topbar__eyebrow">BPMP Platform</span>
            <strong>{navigation.find((item) => item.to === pathname)?.label ?? "Tasklist"}</strong>
          </div>
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
          <div className="tenant-chip" title="Authenticated tenant">
            <Building2 size={14} aria-hidden="true" />
            <span>{identity?.tenantId}</span>
          </div>
          <IconButton icon={LogOut} label="Disconnect" onClick={disconnect} />
        </header>
        <div className="page">{children}</div>
      </main>
    </div>
  );
}
