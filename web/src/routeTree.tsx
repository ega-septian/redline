import { createRootRoute, createRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { FailuresPage } from "@/pages/failures";
import { ImpactPage } from "@/pages/impact";
import { RulesPage } from "@/pages/rules";
import { RunsPage } from "@/pages/runs";
import { ScoreboardPage } from "@/pages/scoreboard";

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
  runsRoute,
  impactRoute,
  rulesRoute,
  scoreboardRoute,
]);
