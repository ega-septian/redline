import { useQuery } from "@tanstack/react-query";
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { api } from "@/api/client";
import type { ApiProject } from "@/api/types";

const STORAGE_KEY = "redline.project";
const FALLBACK_PROJECT = "toolshop";

type ProjectContextValue = {
  project: string;
  setProject: (project: string) => void;
  projects: ApiProject[];
  loading: boolean;
};

const ProjectContext = createContext<ProjectContextValue | null>(null);

/**
 * Keeps the selected project in one place: the sidebar selector, every page query and localStorage
 * all read the same value. The first project from the API is the default for a fresh browser.
 */
export function ProjectProvider({ children }: { children: ReactNode }) {
  const projectsQuery = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const [project, setProjectState] = useState(() => {
    try {
      return window.localStorage.getItem(STORAGE_KEY) || FALLBACK_PROJECT;
    } catch {
      return FALLBACK_PROJECT;
    }
  });
  const projects = projectsQuery.data ?? [];

  useEffect(() => {
    if (projects.length === 0) return;
    if (projects.some((item) => item.project === project)) return;
    const next = projects[0].project;
    setProjectState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Private mode: keep the selection for this tab only.
    }
  }, [projects, project]);

  function setProject(next: string) {
    setProjectState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Private mode: keep the selection for this tab only.
    }
  }

  const value = useMemo(
    () => ({ project, setProject, projects, loading: projectsQuery.isPending }),
    [project, projects, projectsQuery.isPending],
  );
  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>;
}

export function useProject(): ProjectContextValue {
  const value = useContext(ProjectContext);
  if (!value) throw new Error("useProject must be used inside ProjectProvider");
  return value;
}
