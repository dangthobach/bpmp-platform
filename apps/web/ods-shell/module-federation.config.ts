import { createModuleFederationConfig } from "@module-federation/vite";

export default createModuleFederationConfig({
  name: "ods_shell",
  manifest: true,
  dts: false,
  shared: {
    react: { singleton: true, requiredVersion: "19.2.8" },
    "react-dom": { singleton: true, requiredVersion: "19.2.8" },
    "@ods/mfe-contract": { singleton: true, requiredVersion: "0.1.0" },
    "@ods/call-api": { singleton: true, requiredVersion: "0.1.0" },
    "@ods/shared-ui": { singleton: true, requiredVersion: "0.1.0" },
  },
});
