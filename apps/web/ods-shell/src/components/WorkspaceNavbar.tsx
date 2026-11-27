import type { ReactNode } from "react";
import { Box, Button, Chip, Stack, TextField, Typography } from "@ods/shared-ui";

interface Props {
  title: string;
  localPreview: boolean;
  canSearchModules: boolean;
  moduleSearch: string;
  loading: boolean;
  onModuleSearch(value: string): void;
  onRefresh(): void;
  onOpenNotifications?: (() => void) | undefined;
}

export function WorkspaceNavbar({
  title,
  localPreview,
  canSearchModules,
  moduleSearch,
  loading,
  onModuleSearch,
  onRefresh,
  onOpenNotifications,
}: Props): ReactNode {
  return <Box component="header" role="region" aria-label="Workspace toolbar" sx={{ minHeight: 58, px: { xs: 2, md: 3 }, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 2, bgcolor: "rgba(255,255,255,.94)", borderBottom: "1px solid #e2e7e3", position: "sticky", top: 0, zIndex: 5, backdropFilter: "blur(12px)" }}>
    <Stack direction="row" spacing={1} alignItems="center" sx={{ minWidth: 0 }}>
      <Typography variant="body2" color="text.secondary" noWrap>Workspace</Typography>
      <Typography aria-hidden="true" color="text.disabled">›</Typography>
      <Typography variant="body2" fontWeight={750} textTransform="capitalize" noWrap>{title}</Typography>
      {localPreview && <Chip size="small" label="Local preview" color="success" variant="outlined" />}
    </Stack>
    <Stack direction="row" spacing={1} alignItems="center">
      {canSearchModules && <TextField size="small" label="Find a module" value={moduleSearch} onChange={(event) => onModuleSearch(event.target.value)} sx={{ width: { xs: 145, sm: 220 } }} />}
      {onOpenNotifications && (
        <Button type="button" variant="outlined" size="small" onClick={onOpenNotifications} title="Open Notification workspace">
          Notifications
        </Button>
      )}
      <Button type="button" variant="outlined" size="small" onClick={onRefresh} disabled={loading} title="Reload the signed module catalog">Refresh</Button>
    </Stack>
  </Box>;
}
