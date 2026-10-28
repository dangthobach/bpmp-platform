# ADR-009: ODS Frontend Baseline and Runtime Federation

Status: Accepted for ODS implementation  
Date: 2026-10-28

## Context

The existing Cockpit is a single React/Vite application using TypeScript 7, TanStack Query and Zod. The ODS requirements mandate independently deployed domain frontends using React 19, TypeScript 5.8, MUI 7, Formik/Yup, SWR/Axios and Module Federation 2. Direct MUI imports from domain applications are prohibited.

Downgrading the current Cockpit in place couples unrelated migration work and increases rollback risk. Sharing all dependencies through federation would also turn independent remotes into a runtime monolith.

## Decision

Create a separate ODS frontend workspace with one lockfile and exact compatibility ranges:

- React/React DOM 19 as required federation singletons.
- TypeScript 5.8 for ODS compile-time projects.
- MUI 7 and Emotion accessible only inside `@ods/shared-ui`.
- SWR for ODS server state, Axios encapsulated by `@ods/call-api`.
- Formik/Yup for ODS form state and client UX validation.
- Module Federation manifest-based runtime loading through a signed, allowlisted catalog.

The Shell owns bootstrap, navigation, catalog loading, global providers and top-level isolation. A remote exposes a versioned route/widget contract and never imports another remote's source. Only React, React DOM and stable ODS platform contracts are shared by default. Additional shared dependencies require demonstrated duplicate-cost benefit and compatibility tests.

Existing Cockpit stays unchanged during route-by-route migration. It is removed after the final route and compatibility window; its state/query/validation libraries are not added to ODS shared packages.

## Failure behavior

- Manifest fetch/parse/signature/compatibility failure renders a scoped unavailable state.
- Remote import timeout or render exception is contained by loader and React error boundaries.
- Shell navigation, session controls and other remotes remain usable.
- Catalog rollback selects a previous immutable manifest; it does not mutate published assets.

## Consequences

- Temporary dual frontend toolchains exist, but are isolated rather than mixed in one runtime contract.
- Shared UI becomes a maintained compatibility surface and requires semantic versioning, accessibility and visual gates.
- MFE runtime failures become explicit test cases instead of whole-page failures.

## Verification

Phase 1 must demonstrate current/previous/canary compatibility, missing/malformed manifest, import timeout, render exception and catalog rollback before this architecture is used for write commands.
