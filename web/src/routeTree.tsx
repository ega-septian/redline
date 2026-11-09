import { createRootRoute, createRoute, lazyRouteComponent } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { ProjectProvider } from "@/lib/project";
import { FailuresPage } from "@/pages/failures";
import { HealthPage } from "@/pages/health";
import { ImpactPage } from "@/pages/impact";
import { RulesPage } from "@/pages/rules";
import { RunDetailPage } from "@/pages/run-detail";
import { RunsPage } from "@/pages/runs";
import { ScoreboardPage } from "@/pages/scoreboard";

function RootLayout() {
  return (
    <ProjectProvider>
      <AppShell />
    </ProjectProvider>
  );
}

const rootRoute = createRootRoute({ component: RootLayout });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: FailuresPage,
});

const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/overview",
  component: lazyRouteComponent(() => import("@/pages/overview"), "OverviewPage"),
});

const runsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runs",
  component: RunsPage,
});

const runDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runs/$runId",
  component: RunDetailPage,
});

const healthRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/health",
  component: HealthPage,
});

const quarantineRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/quarantine",
  component: lazyRouteComponent(() => import("@/pages/quarantine"), "QuarantinePage"),
});

const impactRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/impact",
  component: ImpactPage,
});

const coverageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/coverage",
  component: lazyRouteComponent(() => import("@/pages/coverage"), "CoveragePage"),
});

const configRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/config",
  component: lazyRouteComponent(() => import("@/pages/config"), "ConfigPage"),
});

const rulesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/rules",
  component: RulesPage,
});

const scoreboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/scoreboard",
  component: ScoreboardPage,
});

export const routeTree = rootRoute.addChildren([
  indexRoute,
  overviewRoute,
  runsRoute,
  runDetailRoute,
  healthRoute,
  quarantineRoute,
  impactRoute,
  coverageRoute,
  configRoute,
  rulesRoute,
  scoreboardRoute,
]);
