import { useDeferredValue, useEffect, useState } from "react";
import {
  createBackup,
  dryRunConfig,
  fetchBackupDetail,
  fetchDashboardData,
  forcePollAll,
  forcePollSource,
  openStream,
  replyToMessage,
  resolveApproval,
  restoreBackup,
  saveConfig,
  savePack,
  stopRun,
  validateConfig,
} from "./api";
import { AgentWorkspace } from "./components/AgentWorkspace";
import { ApprovalBanner } from "./components/ApprovalBanner";
import { OverviewPage } from "./components/OverviewPage";
import { PacksWorkspace } from "./components/PacksWorkspace";
import { SettingsWorkspace, type GeneralSettingsDraft } from "./components/SettingsWorkspace";
import { Sidebar } from "./components/Sidebar";
import { WorkflowWorkspace } from "./components/WorkflowWorkspace";
import { WorkspaceHeader } from "./components/WorkspaceHeader";
import {
  csvLines,
  emptyPackDraft,
  emptySourceDraft,
  matchesSearch,
  parseRoute,
  routePath,
  sourceHealth,
  type PackDraft,
  type SourceDraft,
} from "./lib/helpers";
import type { ConfigBackupDetailResponse } from "./types";

type SettingsTab = "general" | "yaml" | "backups";
type ThemeMode = "dark" | "light";
type QuickFilterMode = "all" | "attention" | "awaiting-approval";
type DashboardState = Awaited<ReturnType<typeof fetchDashboardData>>;

const themeKey = "maestro-web-theme";

function App() {
  const [dashboard, setDashboard] = useState<DashboardState | null>(null);
  const initialRoute = typeof window === "undefined" ? { view: "overview" as const, settingsTab: "general" as const, agentName: "", workflowName: "" } : parseRoute(window.location.pathname);
  const [view, setView] = useState<"overview" | "agent" | "workflow" | "settings" | "packs">(initialRoute.view);
  const [settingsTab, setSettingsTab] = useState<SettingsTab>(initialRoute.settingsTab);
  const [selectedAgentName, setSelectedAgentName] = useState(initialRoute.agentName);
  const [selectedSourceName, setSelectedSourceName] = useState(initialRoute.workflowName);
  const [selectedAgentRunId, setSelectedAgentRunId] = useState("");
  const [selectedWorkflowRunId, setSelectedWorkflowRunId] = useState("");
  const [selectedBackupName, setSelectedBackupName] = useState("");
  const [selectedBackup, setSelectedBackup] = useState<ConfigBackupDetailResponse | null>(null);
  const [configEditor, setConfigEditor] = useState("");
  const [configResult, setConfigResult] = useState("");
  const [configDiff, setConfigDiff] = useState("");
  const [lastPingAt, setLastPingAt] = useState<string>("");
  const [theme, setTheme] = useState<ThemeMode>(() => {
    if (typeof window === "undefined") return "dark";
    return localStorage.getItem(themeKey) === "light" ? "light" : "dark";
  });
  const [search, setSearch] = useState("");
  const [quickFilter, setQuickFilter] = useState<QuickFilterMode>("all");
  const [sourceGroup, setSourceGroup] = useState("");
  const [sourceDraft, setSourceDraft] = useState<SourceDraft>(emptySourceDraft);
  const [packDraft, setPackDraft] = useState<PackDraft>(emptyPackDraft);
  const [settingsDraft, setSettingsDraft] = useState<GeneralSettingsDraft>({
    maxConcurrentGlobal: "",
    defaultPollInterval: "",
    stallTimeout: "",
    logMaxFiles: "",
  });
  const [packResult, setPackResult] = useState("");
  const [showWorkflowEditor, setShowWorkflowEditor] = useState(false);
  const deferredSearch = useDeferredValue(search.trim().toLowerCase());

  async function refresh() {
    const next = await fetchDashboardData();
    setDashboard(next);
    setConfigEditor((current) => current || next.rawConfig.yaml || "");
    setSelectedAgentName((current) => current || next.status.config.agents[0]?.name || "");
    setSelectedSourceName((current) => current || next.status.config.sources[0]?.name || "");
    setSettingsDraft({
      maxConcurrentGlobal: String(next.status.config.max_concurrent_global || ""),
      defaultPollInterval: next.status.config.default_poll_interval || "",
      stallTimeout: next.status.config.stall_timeout || "",
      logMaxFiles: String(next.status.config.log_max_files || ""),
    });
    setLastPingAt(new Date().toISOString());
  }

  useEffect(() => {
    let cancelled = false;
    fetchDashboardData().then((next) => {
      if (cancelled) return;
      const route = parseRoute(window.location.pathname);
      setDashboard(next);
      setConfigEditor(next.rawConfig.yaml || "");
      setView(route.view);
      setSettingsTab(route.settingsTab);
      setSelectedAgentName(route.agentName || next.status.config.agents[0]?.name || "");
      setSelectedSourceName(route.workflowName || next.status.config.sources[0]?.name || "");
      setSettingsDraft({
        maxConcurrentGlobal: String(next.status.config.max_concurrent_global || ""),
        defaultPollInterval: next.status.config.default_poll_interval || "",
        stallTimeout: next.status.config.stall_timeout || "",
        logMaxFiles: String(next.status.config.log_max_files || ""),
      });
      setLastPingAt(new Date().toISOString());
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    function handlePopState() {
      const route = parseRoute(window.location.pathname);
      setView(route.view);
      setSettingsTab(route.settingsTab);
      if (route.agentName) setSelectedAgentName(route.agentName);
      if (route.workflowName) setSelectedSourceName(route.workflowName);
    }
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem(themeKey, theme);
  }, [theme]);

  useEffect(() => {
    const stream = openStream(() => {
      void refresh();
    });
    return () => stream.close();
  }, []);

  useEffect(() => {
    const id = window.setInterval(() => {
      void refresh();
    }, 10000);
    return () => window.clearInterval(id);
  }, []);

  const config = dashboard?.status.config;
  const sources = dashboard?.sources.items ?? [];
  const runs = dashboard?.runs.items ?? [];
  const outputs = dashboard?.runs.outputs ?? [];
  const approvals = dashboard?.approvals.items ?? [];
  const messages = dashboard?.messages.items ?? [];
  const retries = dashboard?.retries.items ?? [];
  const events = dashboard?.events.items ?? [];
  const backups = dashboard?.backups.items ?? [];
  const agents = config?.agents ?? [];
  const approvalHistory = dashboard?.status.snapshot.approval_history ?? [];
  const messageHistory = dashboard?.status.snapshot.message_history ?? [];

  const mergedSources = config?.sources.map((source) => {
    const runtime = sources.find((item) => item.name === source.name);
    return {
      config: source,
      runtime,
      health: sourceHealth(runtime),
    };
  }) ?? [];

  const sourceGroups = Array.from(
    new Set(
      mergedSources
        .map((item) => item.config.display_group || item.runtime?.display_group || item.config.tracker)
        .filter(Boolean),
    ),
  ).sort((left, right) => left.localeCompare(right));

  const filteredSources = mergedSources
    .filter((item) => {
      const group = item.config.display_group || item.runtime?.display_group || item.config.tracker;
      if (sourceGroup && group !== sourceGroup) return false;
      if (!deferredSearch) return true;
      return matchesSearch(deferredSearch, [
        item.config.name,
        group,
        item.config.tracker,
        item.config.agent_type,
        item.config.project,
        item.config.group,
        item.config.repo,
        ...(item.config.filter_labels || []),
        ...(item.config.epic_filter_labels || []),
        ...(item.config.issue_filter_labels || []),
      ]);
    })
    .filter((item) => {
      if (quickFilter === "attention") {
        return Boolean(item.runtime?.retry_count || item.runtime?.pending_approvals);
      }
      if (quickFilter === "awaiting-approval") {
        return Boolean(item.runtime?.pending_approvals);
      }
      return true;
    });

  const filteredSourceNames = filteredSources.map((item) => item.config.name);

  const visibleApprovals = approvals.filter((approval) => {
    const run = runs.find((item) => item.id === approval.run_id);
    if (run && !filteredSourceNames.includes(run.source_name)) return false;
    if (!deferredSearch) return true;
    return matchesSearch(deferredSearch, [
      approval.agent_name,
      approval.issue_identifier,
      approval.tool_name,
      approval.tool_input,
    ]);
  });

  const visibleMessages = messages.filter((message) => {
    const run = runs.find((item) => item.id === message.run_id);
    if (run && !filteredSourceNames.includes(run.source_name)) return false;
    if (!deferredSearch) return true;
    return matchesSearch(deferredSearch, [
      message.agent_name,
      message.issue_identifier,
      message.summary,
      message.body,
      message.kind,
    ]);
  });

  const visibleRetries = retries.filter((retry) => {
    if (!filteredSourceNames.includes(retry.source_name)) return false;
    if (quickFilter === "awaiting-approval") return false;
    if (!deferredSearch) return true;
    return matchesSearch(deferredSearch, [retry.issue_identifier, retry.source_name, retry.error]);
  });

  const visibleEvents = events.filter((event) => {
    if (!deferredSearch) return true;
    return matchesSearch(deferredSearch, [event.message, event.issue, event.source]);
  });

  const selectedAgent = agents.find((agent) => agent.name === selectedAgentName) ?? agents[0];
  const selectedAgentRuns = runs.filter((run) => run.agent_name === selectedAgent?.name);
  const currentRun = selectedAgentRuns.find((run) => run.id === selectedAgentRunId) ?? selectedAgentRuns[0];
  const currentOutput = currentRun ? outputs.find((output) => output.run_id === currentRun.id) : undefined;
  const selectedSource = mergedSources.find((item) => item.config.name === selectedSourceName) ?? mergedSources[0];
  const selectedSourceRuns = runs.filter((run) => run.source_name === selectedSource?.config.name);
  const selectedWorkflowRun = selectedSourceRuns.find((run) => run.id === selectedWorkflowRunId) ?? selectedSourceRuns[0];
  const selectedWorkflowOutput = selectedWorkflowRun ? outputs.find((output) => output.run_id === selectedWorkflowRun.id) : undefined;
  const selectedSourceRetries = retries.filter((retry) => retry.source_name === selectedSource?.config.name);
  const selectedSourceApprovals = approvals.filter((approval) => {
    const run = runs.find((item) => item.id === approval.run_id);
    return run?.source_name === selectedSource?.config.name;
  });
  const selectedSourceMessages = messages.filter((message) => {
    return message.source_name === selectedSource?.config.name;
  });
  const selectedSourceMessageHistory = messageHistory.filter((entry) => entry.source_name === selectedSource?.config.name);
  const selectedSourceEvents = events.filter((event) => event.source === selectedSource?.config.name);
  const selectedAgentApprovals = approvals.filter((approval) => approval.agent_name === selectedAgent?.name);
  const selectedAgentEvents = events.filter((event) => event.run_id === currentRun?.id || event.source === currentRun?.source_name);

  useEffect(() => {
    if (selectedAgentRuns.length === 0) {
      if (selectedAgentRunId) setSelectedAgentRunId("");
      return;
    }
    if (!selectedAgentRuns.some((run) => run.id === selectedAgentRunId)) {
      setSelectedAgentRunId(selectedAgentRuns[0].id);
    }
  }, [selectedAgentRunId, selectedAgentRuns]);

  useEffect(() => {
    if (selectedSourceRuns.length === 0) {
      if (selectedWorkflowRunId) setSelectedWorkflowRunId("");
      return;
    }
    if (!selectedSourceRuns.some((run) => run.id === selectedWorkflowRunId)) {
      setSelectedWorkflowRunId(selectedSourceRuns[0].id);
    }
  }, [selectedSourceRuns, selectedWorkflowRunId]);

  async function handleApproval(requestId: string, action: "approve" | "reject") {
    try {
      await resolveApproval(requestId, action);
    } catch (err) {
      console.error("Failed to resolve approval:", err);
      alert(`Failed to ${action} approval: ${err instanceof Error ? err.message : String(err)}`);
    }
    await refresh();
  }

  async function handleMessageReply(requestId: string, reply: string) {
    try {
      await replyToMessage(requestId, reply);
    } catch (err) {
      console.error("Failed to reply to message:", err);
      alert(`Failed to send reply: ${err instanceof Error ? err.message : String(err)}`);
    }
    await refresh();
  }

  async function handleValidate() {
    const result = await validateConfig(configEditor);
    setConfigResult(result.ok ? "Config is valid." : result.validation_error || "Validation failed.");
    setConfigDiff(result.diff || "");
  }

  async function handleDryRun() {
    const result = await dryRunConfig(configEditor);
    setConfigResult(result.ok ? "Dry run succeeded. Restart required after save." : result.validation_error || "Dry run failed.");
    setConfigDiff(result.diff || "");
  }

  async function handleSave() {
    const result = await saveConfig(configEditor);
    setConfigResult(result.ok ? "Config saved. Restart required to apply changes." : result.validation_error || "Save failed.");
    setConfigDiff(result.diff || "");
    await refresh();
  }

  async function handleBackupSelect(name: string) {
    const backup = backups.find((item) => item.name === name);
    if (!backup) return;
    setSelectedBackupName(backup.name);
    setSelectedBackup(await fetchBackupDetail(backup.name));
  }

  async function handleCreateBackup() {
    await createBackup();
    await refresh();
  }

  async function handleRestoreBackup() {
    if (!selectedBackupName) return;
    const diffPreview = (selectedBackup?.diff || "").split("\n").slice(0, 18).join("\n");
    const confirmed = window.confirm(
      `Restore backup ${selectedBackupName}?\n\nThis replaces the current config on disk and requires a restart.\n\n${diffPreview || "No diff preview available."}`,
    );
    if (!confirmed) return;
    await restoreBackup(selectedBackupName);
    await refresh();
  }

  function loadSelectedSourceIntoDraft() {
    if (!selectedSource) return;
    const source = selectedSource.config;
    setSourceDraft({
      tracker: source.tracker as SourceDraft["tracker"],
      name: source.name || "",
      agentType: source.agent_type || "",
      project: source.project || "",
      group: source.group || "",
      repo: source.repo || "",
      labels: (source.filter_labels || []).join(", "),
      assignee: source.assignee || "",
      epicIids: (source.epic_filter_iids || []).join(", "),
      issueLabels: (source.issue_filter_labels || []).join(", "),
    });
    navigate("workflow", { workflowName: source.name });
    setShowWorkflowEditor(true);
    setTimeout(() => document.getElementById("source-draft")?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
  }

  function loadSelectedAgentIntoDraft() {
    if (!selectedAgent) return;
    setPackDraft({
      originalName: selectedAgent.agent_pack || selectedAgent.name,
      name: selectedAgent.agent_pack || selectedAgent.name,
      description: selectedAgent.description || "",
      instanceName: selectedAgent.instance_name || selectedAgent.name,
      harness: selectedAgent.harness || "claude-code",
      workspace: selectedAgent.workspace || "git-clone",
      approvalPolicy: selectedAgent.approval_policy || "manual",
      maxConcurrent: String(selectedAgent.max_concurrent || 1),
      tools: (selectedAgent.tools || []).join(", "),
      skills: (selectedAgent.skills || []).join(", "),
      envKeys: (selectedAgent.env_keys || []).join(", "),
      promptBody: selectedAgent.prompt_body || "",
      contextBody: (selectedAgent.context_bodies || []).map((item) => item.content || "").join("\n\n"),
    });
    setTimeout(() => document.getElementById("pack-editor")?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
  }

  async function applySourceDraftToEditor() {
    const draftBlock = renderSourceDraftBlock();
    const nextEditor = upsertSourceBlock(configEditor, draftBlock, selectedSource?.config.name, sourceDraft.name);
    const preview = await dryRunConfig(nextEditor);
    setConfigResult(preview.ok ? "Workflow draft validated. Review the diff, then save the YAML." : preview.validation_error || "Workflow draft failed validation.");
    setConfigDiff(preview.diff || "");
    if (!preview.ok) {
      navigate("settings", { settingsTab: "yaml" });
      return;
    }

    const changeLabel = selectedSource?.config.name === sourceDraft.name || config?.sources.some((source) => source.name === sourceDraft.name)
      ? `Update workflow ${sourceDraft.name}`
      : `Add workflow ${sourceDraft.name || "new workflow"}`;
    const diffPreview = (preview.diff || "").split("\n").slice(0, 18).join("\n");
    const confirmed = window.confirm(`${changeLabel}?\n\nThis updates the YAML editor only. You will still need to save the YAML to persist it.\n\n${diffPreview || "No diff preview available."}`);
    if (!confirmed) return;

    setConfigEditor(nextEditor);
    navigate("settings", { settingsTab: "yaml" });
  }

  async function applySettingsDraftToEditor() {
    const nextEditor = (() => {
      let next = configEditor;
      next = next.replace(/max_concurrent_global:\s*\d+/m, `max_concurrent_global: ${settingsDraft.maxConcurrentGlobal || 10}`);
      next = next.replace(/(defaults:\n(?:.*\n)*?\s+poll_interval:\n\s+duration:\s*)(.*)/m, `$1${settingsDraft.defaultPollInterval || "30s"}`);
      next = next.replace(/(defaults:\n(?:.*\n)*?\s+stall_timeout:\n\s+duration:\s*)(.*)/m, `$1${settingsDraft.stallTimeout || "10m"}`);
      next = next.replace(/(logging:\n(?:.*\n)*?\s+max_files:\s*)(\d+)/m, `$1${settingsDraft.logMaxFiles || 10}`);
      return next;
    })();
    const preview = await dryRunConfig(nextEditor);
    setConfigResult(preview.ok ? "Settings draft validated. Review the diff, then save the YAML." : preview.validation_error || "Settings draft failed validation.");
    setConfigDiff(preview.diff || "");
    if (!preview.ok) {
      navigate("settings", { settingsTab: "yaml" });
      return;
    }
    const confirmed = window.confirm(
      `Apply updated runtime settings to the YAML editor?\n\nThis updates the YAML editor only. You will still need to save the YAML to persist it.\n\n${(preview.diff || "").split("\n").slice(0, 18).join("\n") || "No diff preview available."}`,
    );
    if (!confirmed) return;
    setConfigEditor(nextEditor);
    navigate("settings", { settingsTab: "yaml" });
  }

  async function handleSavePack() {
    if (!packDraft.name.trim()) {
      setPackResult("Pack name is required.");
      return;
    }
    const payload = {
      original_name: packDraft.originalName || undefined,
      name: packDraft.name.trim(),
      description: packDraft.description.trim(),
      instance_name: (packDraft.instanceName || packDraft.name).trim(),
      harness: packDraft.harness,
      workspace: packDraft.workspace,
      approval_policy: packDraft.approvalPolicy,
      max_concurrent: Number.parseInt(packDraft.maxConcurrent || "1", 10) || 1,
      tools: csvLines(packDraft.tools),
      skills: csvLines(packDraft.skills),
      env_keys: csvLines(packDraft.envKeys),
      prompt_body: packDraft.promptBody,
      context_body: packDraft.contextBody,
    };
    const actionLabel = packDraft.originalName ? `Save changes to pack ${payload.name}` : `Create new pack ${payload.name}`;
    const confirmed = window.confirm(
      `${actionLabel}?\n\nThis writes the pack files to disk and refreshes the config summary. A restart is still required before runs use the updated pack.`,
    );
    if (!confirmed) return;
    const result = await savePack(payload);
    setPackResult(result.ok ? "Pack saved. Restart required to apply changes." : result.validation_error || "Pack save failed.");
    await refresh();
  }

  async function handleStopWorkflow() {
    if (!selectedWorkflowRun) return;
    const confirmed = window.confirm(
      `Stop workflow ${selectedSource?.config.name}?\n\nThis will stop the active run ${selectedWorkflowRun.issue.identifier || selectedWorkflowRun.id} and mark it as stopped by the operator.`,
    );
    if (!confirmed) return;
    await stopRun(selectedWorkflowRun.id);
    await refresh();
  }

  async function handleForcePollAll() {
    await forcePollAll();
    await refresh();
  }

  async function handleForcePollWorkflow() {
    if (!selectedSource?.config.name) return;
    await forcePollSource(selectedSource.config.name);
    await refresh();
  }

  function upsertSourceBlock(current: string, draftBlock: string, originalName?: string, nextName?: string) {
    const sourceName = (originalName || nextName || "").trim();
    const sectionMatch = current.match(/(^sources:\n)([\s\S]*?)(^\w[\w_-]*:|$)/m);
    if (!sectionMatch) {
      return `${current.trimEnd()}\n\nsources:\n${draftBlock}\n`;
    }

    const sectionIndex = sectionMatch.index || 0;
    const suffixAnchor = sectionIndex + sectionMatch[0].length - sectionMatch[3].length;
    const prefix = current.slice(0, sectionIndex) + sectionMatch[1];
    const body = sectionMatch[2];
    const suffix = current.slice(suffixAnchor);
    const lines = body.split("\n");
    const itemStarts: Array<{ start: number; end: number; name: string }> = [];

    for (let i = 0; i < lines.length; i += 1) {
      const match = lines[i].match(/^\s{2}- name:\s*(.+)\s*$/);
      if (!match) continue;
      const start = i;
      let end = lines.length;
      for (let j = i + 1; j < lines.length; j += 1) {
        if (/^\s{2}- name:\s*/.test(lines[j])) {
          end = j;
          break;
        }
      }
      itemStarts.push({ start, end, name: match[1].trim() });
    }

    const draftLines = draftBlock.split("\n");
    const match = itemStarts.find((item) => item.name === sourceName || item.name === (nextName || "").trim());
    if (match) {
      lines.splice(match.start, match.end - match.start, ...draftLines);
    } else {
      if (lines.length && lines[lines.length - 1] === "") lines.pop();
      lines.push(...draftLines);
    }
    const nextBody = `${lines.join("\n").replace(/\n{3,}/g, "\n\n").replace(/^\n+/, "").trimEnd()}\n`;
    return `${prefix}${nextBody}${suffix}`;
  }

  function renderSourceDraftBlock() {
    const lines = [
      "  - name: " + (sourceDraft.name || "new-source"),
      "    tracker: " + sourceDraft.tracker,
      "    agent_type: " + (sourceDraft.agentType || "code-pr"),
    ];
    if (sourceDraft.project || sourceDraft.group) {
      lines.push("    connection:");
      if (sourceDraft.project) lines.push("      project: " + sourceDraft.project);
      if (sourceDraft.group && sourceDraft.tracker === "gitlab-epic") lines.push("      group: " + sourceDraft.group);
    }
    if (sourceDraft.repo) lines.push("    repo: " + sourceDraft.repo);
    if (sourceDraft.labels || sourceDraft.assignee) {
      lines.push("    filter:");
      if (sourceDraft.labels) {
        lines.push("      labels:");
        csvLines(sourceDraft.labels).forEach((label) => lines.push(`        - ${label}`));
      }
      if (sourceDraft.assignee) lines.push("      assignee: " + sourceDraft.assignee);
    }
    if (sourceDraft.tracker === "gitlab-epic") {
      const iids = csvLines(sourceDraft.epicIids);
      const issueLabels = csvLines(sourceDraft.issueLabels);
      if (iids.length) {
        lines.push("    epic_filter:", "      iids:");
        iids.forEach((iid) => lines.push(`        - ${iid}`));
      }
      if (issueLabels.length) {
        lines.push("    issue_filter:", "      labels:");
        issueLabels.forEach((label) => lines.push(`        - ${label}`));
      }
    }
    return lines.join("\n");
  }

  function navigate(nextView: "overview" | "agent" | "workflow" | "settings" | "packs", options?: { settingsTab?: SettingsTab; agentName?: string; workflowName?: string; replace?: boolean }) {
    const nextTab = options?.settingsTab ?? (nextView === "settings" ? settingsTab : "general");
    const nextAgentName = options?.agentName ?? selectedAgent?.name ?? agents[0]?.name ?? "";
    const nextWorkflowName = options?.workflowName ?? selectedSource?.config.name ?? config?.sources[0]?.name ?? "";
    const nextPath = routePath(nextView, nextTab, nextAgentName, nextWorkflowName);
    const method = options?.replace ? "replaceState" : "pushState";
    window.history[method]({}, "", nextPath);
    setView(nextView);
    setSettingsTab(nextTab);
    if (nextView === "agent" && nextAgentName) {
      setSelectedAgentName(nextAgentName);
    }
    if (nextView === "workflow" && nextWorkflowName) {
      setSelectedSourceName(nextWorkflowName);
    }
  }

  return (
    <div className="appShell">
      <div className="appGrid">
        <Sidebar
          view={view}
          sources={config?.sources ?? []}
          selectedWorkflow={selectedSource?.config}
          search={search}
          onSearchChange={setSearch}
          onSelectView={(nextView) => navigate(nextView)}
          onOpenSource={(name) => {
            setSelectedSourceName(name);
            setShowWorkflowEditor(false);
            navigate("workflow", { workflowName: name });
          }}
          onOpenSettings={() => {
            navigate("settings", { settingsTab: "general" });
          }}
          onOpenPacks={() => navigate("packs")}
        />

        <section className="workspace">
          <WorkspaceHeader
            view={view}
            selectedAgent={selectedAgent}
            workflowName={selectedSource?.config.name}
            generatedAt={lastPingAt}
            theme={theme}
            onOpenHome={() => navigate("overview")}
            onRefresh={() => void refresh()}
            onToggleTheme={() => setTheme((current) => (current === "dark" ? "light" : "dark"))}
          />

          <ApprovalBanner approval={visibleApprovals[0]} approvals={visibleApprovals.length} onResolve={handleApproval} />

          {view === "overview" ? (
        <OverviewPage
          generatedAt={dashboard?.status.generated_at}
          instanceMetrics={dashboard?.status.snapshot.instance_metrics}
          harnessMetrics={dashboard?.status.snapshot.harness_metrics}
          quickFilter={quickFilter}
              onQuickFilterChange={setQuickFilter}
              sourceGroup={sourceGroup}
              onSourceGroupChange={setSourceGroup}
              sourceGroups={sourceGroups}
              approvals={visibleApprovals}
              messages={visibleMessages}
              retries={visibleRetries}
              sources={filteredSources.map((source) => ({
                  name: source.config.name,
                  displayGroup: source.config.display_group || source.runtime?.display_group || source.config.tracker,
                  tracker: source.config.tracker,
                  agentType: source.config.agent_type,
                  health: source.health,
                  visibleCount: source.runtime?.last_poll_count || 0,
                  metrics: source.runtime?.metrics,
                }))}
              events={visibleEvents}
              approvalHistory={approvalHistory
                .filter((entry) => {
                  if (!deferredSearch) return true;
                  return matchesSearch(deferredSearch, [
                    entry.issue_identifier,
                    entry.agent_name,
                    entry.tool_name,
                    entry.outcome,
                    entry.decision,
                  ]);
                })
                .slice(0, 6)}
              messageHistory={messageHistory
                .filter((entry) => {
                  if (!deferredSearch) return true;
                  return matchesSearch(deferredSearch, [
                    entry.issue_identifier,
                    entry.agent_name,
                    entry.summary,
                    entry.reply,
                    entry.kind,
                    entry.outcome,
                  ]);
                })
                .slice(0, 6)}
              onOpenSource={(name) => {
                setSelectedSourceName(name);
                navigate("workflow", { workflowName: name });
              }}
              onForcePollAll={() => void handleForcePollAll()}
            />
          ) : null}

          {view === "agent" && selectedAgent ? (
            <AgentWorkspace
              agent={selectedAgent}
              runs={selectedAgentRuns}
              selectedRunId={currentRun?.id}
              currentRun={currentRun}
              currentOutput={currentOutput}
              approvals={selectedAgentApprovals}
              events={selectedAgentEvents}
              sources={config?.sources.filter((source) => source.agent_type === selectedAgent.name) ?? []}
              onSelectRun={setSelectedAgentRunId}
              onResolveApproval={handleApproval}
            />
          ) : null}

          {view === "workflow" ? (
            <WorkflowWorkspace
              workflow={selectedSource?.config}
              runtime={selectedSource?.runtime}
              selectedRunId={selectedWorkflowRun?.id}
              currentRun={selectedWorkflowRun}
              currentOutput={selectedWorkflowOutput}
              runs={selectedSourceRuns}
              retries={selectedSourceRetries}
              approvals={selectedSourceApprovals}
              messages={selectedSourceMessages}
              messageHistory={selectedSourceMessageHistory}
              events={selectedSourceEvents}
              sourceDraft={sourceDraft}
              showEditor={showWorkflowEditor}
              onSourceDraftChange={setSourceDraft}
              onAppendSourceDraft={() => void applySourceDraftToEditor()}
              onOpenYaml={() => navigate("settings", { settingsTab: "yaml" })}
              onLoadSelectedWorkflow={loadSelectedSourceIntoDraft}
              onNewWorkflow={() => {
                setSourceDraft(emptySourceDraft);
                setShowWorkflowEditor(true);
              }}
              onToggleEditor={() => setShowWorkflowEditor((value) => !value)}
              onStopWorkflow={() => void handleStopWorkflow()}
              onForcePollWorkflow={() => void handleForcePollWorkflow()}
              onSelectRun={setSelectedWorkflowRunId}
              onOpenAgent={(name) => {
                setSelectedAgentName(name);
                navigate("agent", { agentName: name });
              }}
              onResolveMessage={(requestId, reply) => handleMessageReply(requestId, reply)}
            />
          ) : null}

          {view === "settings" && config ? (
            <SettingsWorkspace
              config={config}
              settingsTab={settingsTab}
              onChangeSettingsTab={(tab) => navigate("settings", { settingsTab: tab })}
              rawConfig={dashboard?.rawConfig}
              configEditor={configEditor}
              onConfigEditorChange={setConfigEditor}
              onValidate={() => void handleValidate()}
              onDryRun={() => void handleDryRun()}
              onSave={() => void handleSave()}
              configResult={configResult}
              configDiff={configDiff}
              backups={backups}
              selectedBackupName={selectedBackupName}
              selectedBackup={selectedBackup}
              onSelectBackup={(backup) => void handleBackupSelect(backup.name)}
              onCreateBackup={() => void handleCreateBackup()}
              onRestoreBackup={() => void handleRestoreBackup()}
              settingsDraft={settingsDraft}
              onSettingsDraftChange={setSettingsDraft}
              onApplySettingsDraft={() => void applySettingsDraftToEditor()}
              onOpenYaml={() => navigate("settings", { settingsTab: "yaml" })}
            />
          ) : null}

          {view === "packs" ? (
            <PacksWorkspace
              agents={agents}
              selectedAgent={selectedAgent}
              onSelectAgent={setSelectedAgentName}
              packDraft={packDraft}
              onPackDraftChange={setPackDraft}
              onOpenAgent={(name) => navigate("agent", { agentName: name })}
              onResetDraft={() => setPackDraft(emptyPackDraft)}
              onLoadSelectedAgent={loadSelectedAgentIntoDraft}
              onSavePack={() => void handleSavePack()}
              packResult={packResult}
            />
          ) : null}
        </section>
      </div>
    </div>
  );
}

export default App;
