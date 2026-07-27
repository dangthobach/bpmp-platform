import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { loadRuntimeConfig } from "./config/runtime";
import "./styles.css";

const root = createRoot(document.getElementById("root")!);

loadRuntimeConfig()
  .then((config) => {
    root.render(
      <StrictMode>
        <App config={config} />
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
