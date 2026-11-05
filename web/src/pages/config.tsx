import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Trash2,
} from "lucide-react";
import { useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ApiContractSource, ApiNotifications, ApiSourceCheck } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const eventLabels: Record<string, string> = {
  new_failure: "New failure",
  regressed: "Regressed",
  quarantine_expiring: "Quarantine expiring (soon)",
};

type Notice = { tone: "ok" | "error"; text: string } | null;

type FormState = { project: string; url: string; editing: boolean };

export function ConfigPage() {
  const queryClient = useQueryClient();
  const configQuery = useQuery({ queryKey: ["config"], queryFn: api.config });
  const [form, setForm] = useState<FormState | null>(null);
  const [notice, setNotice] = useState<Notice>(null);
  const [checking, setChecking] = useState<string | null>(null);

  function invalidate() {
    return Promise.all([
      queryClient.invalidateQueries({ queryKey: ["config"] }),
      queryClient.invalidateQueries({ queryKey: ["coverage"] }),
    ]);
  }

  const check = useMutation({
    mutationFn: api.checkSource,
    onMutate: (project) => setChecking(project),
    onSuccess: async (result, project) => {
      await invalidate();
      setNotice(
        result.ok
          ? {
              tone: "ok",
              text: `${project}: ${result.openapi ?? "OpenAPI"} · ${result.endpoints} endpoints · ${result.duration_ms} ms`,
            }
          : { tone: "error", text: `${project}: ${result.error ?? "invalid contract"}` },
      );
    },
    onError: (error) => setNotice({ tone: "error", text: messageOf(error) }),
    onSettled: () => setChecking(null),
  });

  const remove = useMutation({
    mutationFn: api.deleteSource,
    onSuccess: async (_result, project) => {
      await invalidate();
      setForm(null);
      setNotice({ tone: "ok", text: `${project}: contract source removed` });
    },
    onError: (error) => setNotice({ tone: "error", text: messageOf(error) }),
  });

  if (configQuery.isPending) return <State>Loading configuration…</State>;
  if (configQuery.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load configuration</div>
        <div className="mt-1 text-xs">{messageOf(configQuery.error)}</div>
        <Button className="mt-3" onClick={() => configQuery.refetch()}>
          Retry
        </Button>
      </State>
    );
  }

  const sources = configQuery.data.contracts;
  const valid = sources.filter((source) => source.last_status === "valid").length;

  return (
    <>
      {notice && <NoticeBar notice={notice} onClose={() => setNotice(null)} />}

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
          <span className="text-[12.5px] font-semibold">Contract sources</span>
          <Badge tone={valid > 0 ? "flaky" : "neutral"} className="gap-1.5">
            <span className={cn("h-1.5 w-1.5 rounded-full", valid > 0 ? "bg-ok" : "bg-faint")} />
            {sources.length === 0 ? "no sources" : `${valid} valid`}
          </Badge>
          <span className="hidden text-[11px] text-faint md:inline">
            OpenAPI JSON used for contract analysis and coverage
          </span>
          <Button
            className="ml-auto"
            variant="default"
            onClick={() => {
              setNotice(null);
              setForm({ project: "", url: "", editing: false });
            }}
          >
            <Plus className="h-3.5 w-3.5" />
            Add source
          </Button>
        </div>

        <div className="overflow-x-auto">
          <div className="min-w-[860px]">
            <div className="grid grid-cols-[150px_minmax(0,1fr)_170px_110px_80px_150px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2 text-[9.5px] uppercase tracking-[0.7px] text-faint">
              <span>Project</span>
              <span>Spec URL</span>
              <span>Status</span>
              <span>Last checked</span>
              <span className="text-right">Endpoints</span>
              <span className="text-right">Actions</span>
            </div>
            {sources.length === 0 ? (
              <div className="px-4 py-10 text-center text-sm text-muted">
                No contract source yet. Add the OpenAPI JSON URL for a project.
              </div>
            ) : (
              sources.map((source) => (
                <SourceRow
                  key={source.project}
                  source={source}
                  checking={checking === source.project}
                  onTest={() => check.mutate(source.project)}
                  onEdit={() =>
                    setForm({ project: source.project, url: source.url, editing: true })
                  }
                  onDelete={() => {
                    if (
                      window.confirm(
                        `Remove the contract source for “${source.project}”? Historical snapshots stay in Coverage.`,
                      )
                    ) {
                      remove.mutate(source.project);
                    }
                  }}
                />
              ))
            )}
          </div>
        </div>

        {form && (
          <SourceForm
            key={`${form.project}:${form.editing ? form.url : "new"}`}
            initial={form}
            onClose={() => setForm(null)}
            onSaved={async (project) => {
              await invalidate();
              setForm(null);
              setNotice({ tone: "ok", text: `${project}: contract source saved and active` });
            }}
          />
        )}

        <div className="border-t border-line px-3.5 py-2.5 text-[11px] leading-relaxed text-faint">
          Sources are stored in the database and applied to the running server immediately. Changing a
          source does not delete old snapshots, so Coverage can still show contract drift. Back up the
          Neo4j volume to keep this configuration.
        </div>
      </Card>

      <NotificationsCard
        key={String(configQuery.data.notifications.updated_at ?? "default")}
        settings={configQuery.data.notifications}
        onSaved={invalidate}
      />

      <p className="text-[11px] leading-relaxed text-faint">
        Sources are validated by fetching and parsing the document from the Redline server, not from
        your browser. For Docker, use <span className="font-mono">host.docker.internal</span> for a
        service on the host or a public URL. Swagger UI pages are rejected: Redline needs the JSON
        document itself.
      </p>
    </>
  );
}

function SourceRow({
  source,
  checking,
  onTest,
  onEdit,
  onDelete,
}: {
  source: ApiContractSource;
  checking: boolean;
  onTest: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="grid grid-cols-[150px_minmax(0,1fr)_170px_110px_80px_150px] items-center gap-3 border-b border-line-soft px-3.5 py-2.5 text-[11px] last:border-b-0 hover:bg-[#1b222b]">
      <div className="flex min-w-0 flex-col">
        <span className="truncate font-mono font-semibold text-[#cdd9e5]">{source.project}</span>
        <span className="truncate text-[10px] text-faint">
          {source.by ? `by ${source.by}` : "database"}
        </span>
      </div>
      <a
        href={source.url}
        target="_blank"
        rel="noreferrer"
        title={source.url}
        className="truncate font-mono text-[11px] text-[#79c0ff] hover:underline"
      >
        {source.url}
      </a>
      <SourceStatus source={source} />
      <span className="text-[10.5px] text-faint">{ago(source.last_checked_at)}</span>
      <span className="text-right font-mono text-muted">
        {source.last_endpoints ? source.last_endpoints : "—"}
      </span>
      <div className="flex items-center justify-end gap-1">
        <Button
          size="sm"
          variant="ghost"
          onClick={onTest}
          disabled={checking}
          title="Fetch and parse this URL now"
        >
          <RefreshCw className={cn("h-3 w-3", checking && "animate-spin")} />
          Test
        </Button>
        <Button size="sm" variant="ghost" onClick={onEdit} title="Use another URL for this project">
          <Pencil className="h-3 w-3" />
          Edit
        </Button>
        <Button
          size="icon"
          variant="ghost"
          onClick={onDelete}
          title="Remove this source"
          aria-label={`Remove ${source.project}`}
        >
          <Trash2 className="h-3 w-3" />
        </Button>
      </div>
    </div>
  );
}

function SourceStatus({ source }: { source: ApiContractSource }) {
  if (source.last_status === "valid") {
    return (
      <span className="flex flex-wrap items-center gap-1">
        <Badge tone="flaky" className="gap-1.5" title={source.last_error || undefined}>
          <span className="h-1.5 w-1.5 rounded-full bg-ok" />
          valid
        </Badge>
        <span className="truncate text-[10px] text-faint" title={source.last_version}>
          {source.last_version}
        </span>
      </span>
    );
  }
  if (source.last_status === "invalid") {
    return (
      <Badge tone="backend" className="gap-1.5" title={source.last_error || undefined}>
        <span className="h-1.5 w-1.5 rounded-full bg-danger" />
        invalid
      </Badge>
    );
  }
  return <Badge tone="neutral">not checked</Badge>;
}

function SourceForm({
  initial,
  onClose,
  onSaved,
}: {
  initial: FormState;
  onClose: () => void;
  onSaved: (project: string) => void;
}) {
  const [project, setProject] = useState(initial.project);
  const [url, setURL] = useState(initial.url);
  const [check, setCheck] = useState<ApiSourceCheck | null>(null);
  const [error, setError] = useState("");

  const validate = useMutation({
    mutationFn: api.validateSource,
    onMutate: () => {
      setCheck(null);
      setError("");
    },
    onSuccess: setCheck,
    onError: (err) => setError(messageOf(err)),
  });

  const save = useMutation({
    mutationFn: () => api.saveSource(project.trim(), url.trim()),
    onSuccess: () => onSaved(project.trim()),
    onError: (err) => setError(messageOf(err)),
  });

  const tested = check?.ok === true && check.url === url.trim();
  const canSave = project.trim() !== "" && tested && !save.isPending;

  return (
    <form
      className="border-t border-line bg-[#0f141a] px-3.5 py-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (canSave) save.mutate();
      }}
    >
      <div className="mb-2 text-[12px] font-semibold">
        {initial.editing ? `Edit ${initial.project}` : "Add contract source"}
      </div>
      <div className="grid items-center gap-x-3 gap-y-2 sm:grid-cols-[150px_minmax(0,1fr)]">
        <label className="text-[10.5px] text-faint" htmlFor="config-project">
          Project
        </label>
        <Input
          id="config-project"
          value={project}
          onChange={(event) => setProject(event.target.value)}
          placeholder="toolshop"
          className="h-8 max-w-xs font-mono text-[11px]"
          disabled={initial.editing}
          autoComplete="off"
        />
        <label className="text-[10.5px] text-faint" htmlFor="config-url">
          Spec URL
        </label>
        <Input
          id="config-url"
          value={url}
          onChange={(event) => {
            setURL(event.target.value);
            setCheck(null);
            setError("");
          }}
          placeholder="https://api.practicesoftwaretesting.com/docs"
          className="h-8 font-mono text-[11px]"
          autoComplete="off"
          spellCheck={false}
        />
      </div>

      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="default"
          onClick={() => validate.mutate(url.trim())}
          disabled={url.trim() === "" || validate.isPending}
        >
          <RefreshCw className={cn("h-3.5 w-3.5", validate.isPending && "animate-spin")} />
          {validate.isPending ? "Testing…" : "Test URL"}
        </Button>
        <Button type="submit" variant="secondary" disabled={!canSave}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        <Button type="button" variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        {!tested && !validate.isPending && (
          <span className="text-[10.5px] text-faint">Test the URL before saving.</span>
        )}
      </div>

      {error && <CheckPanel tone="error" title="Request failed" detail={error} />}
      {check &&
        (check.ok ? (
          <CheckPanel
            tone="ok"
            title={`Valid ${check.openapi ?? "OpenAPI"}${check.title ? ` · ${check.title}` : ""}`}
            detail={`${check.endpoints} endpoints · ${check.duration_ms} ms · ${check.content_type ?? "JSON"}`}
          />
        ) : (
          <CheckPanel
            tone="error"
            title={check.error ?? "Not a usable OpenAPI document"}
            detail={check.hint}
            candidates={check.candidates}
            onUse={(candidate) => {
              setURL(candidate);
              setCheck(null);
            }}
          />
        ))}
    </form>
  );
}

function CheckPanel({
  tone,
  title,
  detail,
  candidates,
  onUse,
}: {
  tone: "ok" | "error";
  title: string;
  detail?: string;
  candidates?: string[];
  onUse?: (candidate: string) => void;
}) {
  return (
    <div
      className={cn(
        "mt-2.5 rounded-md border px-2.5 py-2 text-[10.5px] leading-relaxed",
        tone === "ok"
          ? "border-ok/40 bg-ok/10 text-[#9be9a8]"
          : "border-danger/40 bg-danger/10 text-[#ffb3ad]",
      )}
    >
      <div className="font-semibold">
        {tone === "ok" ? (
          <CheckCircle2 className="mr-1 inline h-3 w-3" />
        ) : (
          <AlertTriangle className="mr-1 inline h-3 w-3" />
        )}
        {title}
      </div>
      {detail && <div className="mt-0.5 text-[10.5px] opacity-90">{detail}</div>}
      {candidates && candidates.length > 0 && (
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          <span className="opacity-80">Try:</span>
          {candidates.map((candidate) => (
            <button
              key={candidate}
              type="button"
              onClick={() => onUse?.(candidate)}
              className="max-w-full truncate rounded border border-line-soft bg-canvas px-1.5 py-0.5 font-mono text-[10px] text-[#cdd9e5] hover:border-brand/60"
              title={candidate}
            >
              {candidate}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function NotificationsCard({
  settings,
  onSaved,
}: {
  settings: ApiNotifications;
  onSaved: () => Promise<unknown>;
}) {
  const [enabled, setEnabled] = useState(settings.enabled);
  const [webhook, setWebhook] = useState(settings.webhook_url);
  const [events, setEvents] = useState<string[]>(settings.events);
  const [notice, setNotice] = useState<Notice>(null);

  const save = useMutation({
    mutationFn: () =>
      api.saveNotifications({ enabled, webhook_url: webhook.trim(), events }),
    onSuccess: async () => {
      await onSaved();
      setNotice({ tone: "ok", text: "Notification settings saved." });
    },
    onError: (err) => setNotice({ tone: "error", text: messageOf(err) }),
  });

  const test = useMutation({
    mutationFn: api.testNotification,
    onSuccess: (result) => setNotice({ tone: "ok", text: result.message }),
    onError: (err) => setNotice({ tone: "error", text: messageOf(err) }),
  });

  function toggleEvent(event: string) {
    setEvents((current) =>
      current.includes(event) ? current.filter((item) => item !== event) : [...current, event],
    );
  }

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <Bell className="h-3.5 w-3.5 text-brand" />
        <span className="text-[12.5px] font-semibold">Notifications</span>
        <Badge tone="brand">discord</Badge>
        <span className="ml-auto text-[10.5px] text-faint">
          sends new failures and regressions to a Discord webhook
        </span>
      </div>
      <div className="px-3.5 py-3">
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            aria-label="Enable notifications"
            onClick={() => setEnabled((value) => !value)}
            className={cn(
              "relative h-[18px] w-[32px] shrink-0 rounded-full transition-colors",
              enabled ? "bg-brand" : "bg-[#30363d]",
            )}
          >
            <span
              className={cn(
                "absolute top-[2px] h-[14px] w-[14px] rounded-full bg-white transition-all",
                enabled ? "left-[16px]" : "left-[2px]",
              )}
            />
          </button>
          <span className="text-[11.5px] text-muted">Enabled</span>
          <Badge tone="neutral" className="ml-auto normal-case tracking-normal">
            best-effort delivery
          </Badge>
        </div>

        <div className="mt-3 grid items-center gap-x-3 gap-y-2 sm:grid-cols-[110px_minmax(0,1fr)]">
          <label className="text-[10.5px] text-faint" htmlFor="config-webhook">
            Discord webhook
          </label>
          <div className="flex items-center gap-2">
            <Input
              id="config-webhook"
              value={webhook}
              onChange={(event) => setWebhook(event.target.value)}
              placeholder="https://discord.com/api/webhooks/…"
              className="h-8 font-mono text-[11px]"
              autoComplete="off"
              spellCheck={false}
            />
            {settings.webhook_set && <Badge tone="neutral">stored</Badge>}
          </div>
        </div>
        <div className="mt-1 text-[10px] text-faint sm:ml-[122px]">
          Discord → Server Settings → Integrations → Webhooks → Copy Webhook URL. The stored token is
          masked; leave it unchanged to keep the current webhook.
        </div>

        <div className="mt-3">
          <div className="mb-1.5 text-[10.5px] text-faint">Events</div>
          <div className="flex flex-wrap gap-2">
            {settings.available_events.map((event) => {
              const soon = event === "quarantine_expiring";
              return (
                <label
                  key={event}
                  title={soon ? "Not delivered yet; coming later." : undefined}
                  className={cn(
                    "inline-flex items-center gap-1.5 rounded border px-2 py-1 text-[10.5px]",
                    soon ? "cursor-not-allowed opacity-60" : "cursor-pointer",
                    events.includes(event)
                      ? "border-brand/50 bg-brand/10 text-ink"
                      : "border-line bg-[#0f141a] text-muted",
                  )}
                >
                  <input
                    type="checkbox"
                    className="h-3 w-3 accent-[#58a6ff]"
                    checked={events.includes(event)}
                    onChange={() => toggleEvent(event)}
                    disabled={soon}
                  />
                  {eventLabels[event] ?? event}
                </label>
              );
            })}
          </div>
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button onClick={() => save.mutate()} disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
          <Button
            variant="outline"
            onClick={() => test.mutate()}
            disabled={test.isPending}
            title="Send a real Discord test message"
          >
            <Send className="h-3 w-3" />
            Send test
          </Button>
          <span className="text-[10.5px] text-faint">
            Delivery is best-effort; a failed webhook is logged and does not fail the run.
          </span>
        </div>

        {notice && (
          <div
            className={cn(
              "mt-2 rounded-md border px-2.5 py-1.5 text-[10.5px]",
              notice.tone === "ok"
                ? "border-ok/40 bg-ok/10 text-[#9be9a8]"
                : "border-danger/40 bg-danger/10 text-[#ffb3ad]",
            )}
          >
            {notice.text}
          </div>
        )}
      </div>
    </Card>
  );
}

function NoticeBar({ notice, onClose }: { notice: NonNullable<Notice>; onClose: () => void }) {
  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-[10px] border px-3 py-2 text-[11px]",
        notice.tone === "ok"
          ? "border-ok/40 bg-ok/10 text-[#9be9a8]"
          : "border-danger/40 bg-danger/10 text-[#ffb3ad]",
      )}
    >
      {notice.tone === "ok" ? (
        <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      ) : (
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      )}
      <span className="min-w-0 flex-1">{notice.text}</span>
      <button type="button" onClick={onClose} className="shrink-0 text-[10.5px] opacity-70 hover:opacity-100">
        Dismiss
      </button>
    </div>
  );
}

function State({ children }: { children: ReactNode }) {
  return <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>;
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "The request failed.";
}

function ago(iso?: string | null): string {
  if (!iso) return "—";
  const time = new Date(iso).getTime();
  if (Number.isNaN(time)) return "—";
  const minutes = Math.floor((Date.now() - time) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}
