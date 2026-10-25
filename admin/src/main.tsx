import "./styles/app.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { configureClient, noteActivity } from "./lib/api/client";
import { config } from "./lib/config";
import { initTheme } from "./lib/theme";
import { router } from "./router";

configureClient({ baseUrl: config.apiUrl });
initTheme();

// Mock mode is a build-time constant, written inline so the bundler drops this
// branch, and the mock chunk with it, from production builds.
if (import.meta.env.VITE_MOCK === "1") {
  const { installMock } = await import("./mock");
  installMock();
}

// The session is kept alive while the operator works; an idle panel lets it lapse.
noteActivity();
// Reading counts as much as clicking: scrolls of any panel reach the capturing listener.
for (const ev of ["pointerdown", "keydown", "wheel", "touchstart", "scroll"] as const) {
  window.addEventListener(ev, () => noteActivity(), { passive: true, capture: true });
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
