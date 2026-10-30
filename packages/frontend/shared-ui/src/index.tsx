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
  palette: {
    primary: { main: "#0b57d0" },
    background: { default: "#f7f9fc" },
  },
});

export function OdsThemeProvider({ children }: PropsWithChildren): ReactNode {
  return <ThemeProvider theme={theme}><CssBaseline />{children}</ThemeProvider>;
}

export function Page({ title, children }: PropsWithChildren<{ title: string }>): ReactNode {
  return <Container maxWidth="lg" sx={{ py: 3 }}><Typography variant="h4" component="h1" gutterBottom>{title}</Typography>{children}</Container>;
}

export function ShellHeader({ title }: { title: string }): ReactNode {
  return <AppBar position="static"><Toolbar><Typography variant="h6">{title}</Typography></Toolbar></AppBar>;
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
  if (items.length === 0) return <Typography color="text.secondary">{emptyLabel}</Typography>;
  return <List>{items.map((item) => (
    <ListItem key={item.id} divider secondaryAction={item.action}>
      {item.onClick ? (
        <ListItemButton onClick={item.onClick}>
          <ListItemText primary={item.primary} secondary={item.secondary} />
        </ListItemButton>
      ) : (
        <ListItemText primary={item.primary} secondary={item.secondary} />
      )}
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
