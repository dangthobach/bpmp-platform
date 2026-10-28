import type { PropsWithChildren, ReactNode } from "react";
import {
  Alert,
  AppBar,
  Box,
  Button as MuiButton,
  CircularProgress,
  Container,
  CssBaseline,
  List,
  ListItem,
  ListItemText,
  Stack,
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
  return <Alert severity="error" action={onRetry ? <MuiButton color="inherit" onClick={onRetry}>Retry</MuiButton> : undefined}>
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
}

export function SimpleList({ items, emptyLabel }: { items: SimpleListItem[]; emptyLabel: string }): ReactNode {
  if (items.length === 0) return <Typography color="text.secondary">{emptyLabel}</Typography>;
  return <List>{items.map((item) => <ListItem key={item.id} divider>
    <ListItemText primary={item.primary} secondary={item.secondary} />
  </ListItem>)}</List>;
}

export { Box, Stack, Typography };
