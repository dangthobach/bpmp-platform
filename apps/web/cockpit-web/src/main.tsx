import { Component, StrictMode, type ErrorInfo, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { loadRuntimeConfig } from "./config/runtime";
import "./styles.css";

const root = createRoot(document.getElementById("root")!);

class RootErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(_error: Error, _info: ErrorInfo) {
    // Rendering a visible fallback is more useful than a blank page for operators.
  }

  render() {
    if (this.state.error) {
      return (
        <main className="boot-error">
          <h1>BPMP Cockpit could not start</h1>
          <p>{this.state.error.message || "An unexpected application error occurred"}</p>
        </main>
      );
    }
    return this.props.children;
  }
}

loadRuntimeConfig()
  .then((config) => {
    root.render(
      <StrictMode>
        <RootErrorBoundary>
          <App config={config} />
        </RootErrorBoundary>
      </StrictMode>,
    );
  })
  .catch((error: unknown) => {
    const message = error instanceof Error ? error.message : "Runtime configuration is invalid";
    root.render(
      <main className="boot-error">
        <h1>BPMP Cockpit could not start</h1>
        <p>{message}</p>
      </main>,
    );
  });
