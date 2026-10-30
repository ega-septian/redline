import { createRootRoute, createRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { FailuresPage } from "@/pages/failures";
import { ImpactPage } from "@/pages/impact";
import { RunsPage } from "@/pages/runs";

const rootRoute = createRootRoute({ component: AppShell });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: FailuresPage,
});

const runsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runs",
  component: RunsPage,
});

const impactRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/impact",
  component: ImpactPage,
});

export const routeTree = rootRoute.addChildren([indexRoute, runsRoute, impactRoute]);
