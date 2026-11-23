import type { PropsWithChildren, ReactNode } from "react";
import {
  Alert,
  AppBar,
  Box,
  Button,
  Chip,
  CircularProgress,
  Container,
  CssBaseline,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControl,
  InputLabel,
  List,
  ListItem,
  ListItemButton,
  ListItemText,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  ThemeProvider,
  Toolbar,
  Typography,
  createTheme,
} from "@mui/material";

const theme = createTheme({
  cssVariables: true,
  typography: {
    fontFamily: 'Inter, "Segoe UI", Arial, sans-serif',
    button: { textTransform: "none", fontWeight: 650 },
  },
  palette: {
    primary: { main: "#228255", dark: "#176b45", light: "#dff2e8" },
    background: { default: "#f5f7f5", paper: "#ffffff" },
    text: { primary: "#202622", secondary: "#68716b" },
  },
  shape: { borderRadius: 9 },
  components: {
    MuiPaper: { styleOverrides: { root: { backgroundImage: "none" } } },
    MuiButton: { styleOverrides: { root: { borderRadius: 8 } } },
  },
});

export function OdsThemeProvider({ children }: PropsWithChildren): ReactNode {
  return <ThemeProvider theme={theme}><CssBaseline />{children}</ThemeProvider>;
}

export function Page({ title, children }: PropsWithChildren<{ title: string }>): ReactNode {
  return <Container maxWidth={false} sx={{ py: { xs: 2, md: 3 }, px: { xs: 2, md: 3 }, minHeight: "calc(100vh - 58px)" }}><Typography variant="h4" component="h1" sx={{ mb: 2.5, fontSize: { xs: "1.5rem", md: "1.75rem" }, fontWeight: 750, letterSpacing: "-0.035em" }}>{title}</Typography>{children}</Container>;
}

export function ShellHeader({ title, label = "ODS workspace" }: { title: string; label?: string }): ReactNode {
  return <AppBar position="sticky" elevation={0} sx={{ bgcolor: "rgba(255,255,255,.96)", color: "text.primary", borderBottom: "1px solid #e2e7e3", backdropFilter: "blur(12px)" }}><Toolbar sx={{ minHeight: "58px !important", px: { xs: 2, md: 3 }, gap: 1.25 }}>
    <Box aria-hidden="true" sx={{ width: 30, height: 30, display: "grid", placeItems: "center", borderRadius: 2, bgcolor: "primary.main", color: "white", fontWeight: 800, fontSize: 13, boxShadow: "0 0 0 3px #e7f5ec" }}>B</Box>
    <Box sx={{ display: "grid", lineHeight: 1.15 }}><Typography variant="subtitle2" sx={{ fontWeight: 800, letterSpacing: ".035em" }}>{title}</Typography><Typography variant="caption" color="primary.main" sx={{ fontWeight: 650 }}>Process workspace</Typography></Box>
    <Box sx={{ flex: 1 }} />
    <Chip label={label} size="small" variant="outlined" sx={{ borderColor: "#b9dfca", color: "primary.dark", bgcolor: "#eff9f3", fontWeight: 650 }} />
  </Toolbar></AppBar>;
}

export function ErrorPanel({ title, detail, onRetry }: { title: string; detail?: string; onRetry?: () => void }): ReactNode {
  return <Alert severity="error" action={onRetry ? <Button color="inherit" onClick={onRetry}>Retry</Button> : undefined}>
    <Typography fontWeight={700}>{title}</Typography>{detail ? <Typography>{detail}</Typography> : null}
  </Alert>;
}

export function LoadingPanel({ label }: { label: string }): ReactNode {
  return <Stack direction="row" spacing={2} alignItems="center" role="status"><CircularProgress size={22} /><Typography>{label}</Typography></Stack>;
}

export interface SimpleListItem {
  id: string;
  primary: string;
  secondary?: string;
  onClick?: () => void;
  action?: ReactNode;
}

export function SimpleList({ items, emptyLabel }: { items: SimpleListItem[]; emptyLabel: string }): ReactNode {
  if (items.length === 0) return <Paper variant="outlined" sx={{ p: 4, textAlign: "center", borderStyle: "dashed", bgcolor: "#fbfcfb" }}><Typography color="text.secondary">{emptyLabel}</Typography></Paper>;
  return <List disablePadding sx={{ display: "grid", gap: 1 }}>{items.map((item) => (
    <ListItem key={item.id} disablePadding sx={{ border: "1px solid #e5eae6", borderRadius: 2, bgcolor: "background.paper", overflow: "hidden", alignItems: "stretch", "&:hover": { borderColor: "#b9dfca", bgcolor: "#fcfefc" } }}>
      {item.onClick ? (
        <ListItemButton onClick={item.onClick} sx={{ minWidth: 0, py: 1.5, px: 2, gap: 1.5, "&:hover": { bgcolor: "transparent" } }}>
          <Box aria-hidden="true" sx={{ width: 34, height: 34, flex: "0 0 auto", display: "grid", placeItems: "center", borderRadius: 1.5, bgcolor: "#e8f5ed", color: "primary.dark", fontWeight: 750, fontSize: 12 }}>{item.primary.trim().slice(0, 1).toUpperCase()}</Box>
          <ListItemText primary={item.primary} secondary={item.secondary} primaryTypographyProps={{ fontWeight: 700, fontSize: 13 }} secondaryTypographyProps={{ fontSize: 11.5, sx: { mt: 0.35 } }} />
        </ListItemButton>
      ) : <ListItemText sx={{ px: 2, py: 1.5 }} primary={item.primary} secondary={item.secondary} />}
      {item.action ? <Box sx={{ display: "flex", alignItems: "center", gap: 1, px: 1.5, flexShrink: 0 }}>{item.action}</Box> : null}
    </ListItem>
  ))}</List>;
}

export function SlaBadge({ deadlineEpochMs }: { deadlineEpochMs?: number | undefined }): ReactNode {
  if (!deadlineEpochMs) return null;
  const now = Date.now();
  const diffHours = (deadlineEpochMs - now) / (1000 * 60 * 60);
  if (diffHours < 0) {
    return <Chip label="SLA Breached" size="small" color="error" variant="filled" />;
  }
  if (diffHours < 2) {
    return <Chip label={`At Risk (${Math.max(1, Math.ceil(diffHours * 60))}m)`} size="small" color="warning" variant="filled" />;
  }
  return <Chip label={`Due in ${Math.round(diffHours)}h`} size="small" color="success" variant="outlined" />;
}

export function StalenessIndicator({
  lastSyncedAt,
  onRefresh,
  isRefreshing,
}: {
  lastSyncedAt?: Date;
  onRefresh?: () => void;
  isRefreshing?: boolean;
}): ReactNode {
  const formatted = lastSyncedAt ? lastSyncedAt.toLocaleTimeString() : "Never";
  return (
    <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 1.5 }}>
      <Typography variant="caption" color="text.secondary">
        Last reconciled: {formatted}
      </Typography>
      {onRefresh && (
        <Button size="small" variant="text" onClick={onRefresh} disabled={isRefreshing}>
          {isRefreshing ? "Refreshing..." : "Reconcile"}
        </Button>
      )}
    </Stack>
  );
}

export function StatusPanel({
  title,
  detail,
  pending = false,
}: {
  title: string;
  detail?: string;
  pending?: boolean;
}): ReactNode {
  return (
    <Alert severity={pending ? "info" : "success"} role="status" aria-live="polite">
      <Typography fontWeight={700}>{title}</Typography>
      {detail ? <Typography>{detail}</Typography> : null}
    </Alert>
  );
}

export {
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControl,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  Typography,
};
