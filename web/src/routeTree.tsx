import { createRootRoute, createRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { FailuresPage } from "@/pages/failures";

const rootRoute = createRootRoute({ component: AppShell });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: FailuresPage,
});

export const routeTree = rootRoute.addChildren([indexRoute]);
