import type { ReactNode } from "react";
import type { RemoteCatalogEntry } from "@ods/mfe-contract";
import { Box, Button, Stack, Typography } from "@ods/shared-ui";

interface Props {
  remotes: readonly RemoteCatalogEntry[];
  filter: string;
  activeRemoteName: string | undefined;
  collapsed: boolean;
  onSelect(remote: RemoteCatalogEntry): void;
  onToggleCollapsed(): void;
}

export function WorkspaceSidebar({ remotes, filter, activeRemoteName, collapsed, onSelect, onToggleCollapsed }: Props): ReactNode {
  const query = filter.trim().toLowerCase();
  const visibleRemotes = remotes.filter((remote) => formatRemoteName(remote.name).toLowerCase().includes(query));
  return <Box component="aside" aria-label="BPMP workspace sidebar" sx={{ width: { xs: collapsed ? 64 : 224, md: collapsed ? 68 : 252 }, flex: "0 0 auto", display: "flex", flexDirection: "column", bgcolor: "#fff", borderRight: "1px solid #e2e7e3", transition: "width 160ms ease", overflow: "hidden" }}>
    <Stack direction="row" alignItems="center" spacing={1.25} sx={{ px: 1.75, py: 1.5, minHeight: 64, borderBottom: "1px solid #edf0ed" }}>
      <Box aria-hidden="true" sx={{ width: 34, height: 34, flex: "0 0 auto", display: "grid", placeItems: "center", borderRadius: 2.5, background: "linear-gradient(135deg, #168454, #39b986)", color: "white", fontWeight: 850, boxShadow: "0 0 0 3px #e7f5ec" }}>B</Box>
      {!collapsed && <Box sx={{ minWidth: 0 }}><Typography variant="subtitle2" fontWeight={800} textTransform="uppercase" letterSpacing=".035em">BPMP Platform</Typography><Typography variant="caption" color="primary.main" fontWeight={650}>Multi-Engine BPMN 2.0</Typography></Box>}
    </Stack>

    <Box component="nav" aria-label="Workspace modules" sx={{ p: 1.25, flex: 1, overflowY: "auto" }}>
      {!collapsed && <Typography variant="overline" color="text.secondary" sx={{ px: 1.25, fontWeight: 750, letterSpacing: ".09em" }}>Workspace</Typography>}
      <Stack component="ul" spacing={0.5} sx={{ m: 0, mt: 0.75, p: 0, listStyle: "none" }}>
        {visibleRemotes.map((remote) => {
          const active = remote.name === activeRemoteName;
          const label = formatRemoteName(remote.name);
          return <Box component="li" key={remote.name}>
            <Button
              fullWidth
              type="button"
              aria-label={label}
              aria-current={active ? "page" : undefined}
              title={collapsed ? label : undefined}
              onClick={() => onSelect(remote)}
              sx={{ position: "relative", justifyContent: collapsed ? "center" : "flex-start", gap: 1.25, minWidth: 0, px: 1.25, py: 1.05, color: active ? "primary.dark" : "text.secondary", bgcolor: active ? "#e8f5ed" : "transparent", fontSize: 13, fontWeight: active ? 750 : 600, textTransform: "capitalize", whiteSpace: "nowrap", overflow: "hidden", "&:before": active ? { content: '""', position: "absolute", left: 0, top: 8, bottom: 8, width: 3, borderRadius: "0 4px 4px 0", bgcolor: "primary.main" } : undefined, "&:hover": { bgcolor: active ? "#e1f1e7" : "#f4f7f4" } }}
            >
              <Box aria-hidden="true" sx={{ width: 26, height: 26, flex: "0 0 auto", display: "grid", placeItems: "center", borderRadius: 1.5, bgcolor: active ? "#d8eee1" : "#f1f4f2", color: active ? "primary.dark" : "text.secondary", fontSize: 10, fontWeight: 800 }}>{remote.name.replace(/-mfe$/, "").split("-").map((part) => part[0]).join("").slice(0, 2).toUpperCase()}</Box>
              {!collapsed && <span>{label}</span>}
            </Button>
          </Box>;
        })}
        {remotes.length === 0 && !collapsed && <Typography component="li" variant="caption" color="text.secondary" sx={{ px: 1.25, py: 1 }}>No modules are available for this session.</Typography>}
        {remotes.length > 0 && visibleRemotes.length === 0 && !collapsed && <Typography component="li" variant="caption" color="text.secondary" sx={{ px: 1.25, py: 1 }}>No matching modules.</Typography>}
      </Stack>
    </Box>

    <Box sx={{ p: 1.25, borderTop: "1px solid #edf0ed" }}>
      <Button fullWidth type="button" onClick={onToggleCollapsed} aria-label={collapsed ? "Expand navigation" : "Collapse navigation"} aria-expanded={!collapsed} sx={{ justifyContent: collapsed ? "center" : "flex-start", color: "text.secondary", fontSize: 12, fontWeight: 600 }}>
        <Box aria-hidden="true" component="span" sx={{ fontSize: 18, lineHeight: 1, mr: collapsed ? 0 : 1 }}>{collapsed ? "›" : "‹"}</Box>{!collapsed && "Collapse navigation"}
      </Button>
    </Box>
  </Box>;
}

export function formatRemoteName(name: string): string {
  return name.replace(/-mfe$/, "").replaceAll("-", " ");
}
