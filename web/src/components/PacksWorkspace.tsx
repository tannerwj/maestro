import type { ConfigAgentSummary } from "../types";
import { renderPackDraft, type PackDraft } from "../lib/helpers";
import { Control, EmptyState, Metric, PanelHeader, Pill } from "./ui";

export function PacksWorkspace({
  agents,
  selectedAgent,
  onSelectAgent,
  packDraft,
  onPackDraftChange,
  onOpenAgent,
  onResetDraft,
  onLoadSelectedAgent,
  onSavePack,
  packResult,
}: {
  agents: ConfigAgentSummary[];
  selectedAgent?: ConfigAgentSummary;
  onSelectAgent: (name: string) => void;
  packDraft: PackDraft;
  onPackDraftChange: (draft: PackDraft) => void;
  onOpenAgent: (name: string) => void;
  onResetDraft: () => void;
  onLoadSelectedAgent: () => void;
  onSavePack: () => void;
  packResult: string;
}) {
  return (
    <section className="page">
      <div className="settingsGrid">
        <section className="panel">
          <PanelHeader
            title="Existing packs"
            copy="Select one pack to inspect or revise."
            meta={`${agents.length} total`}
            actions={
              <div className="inlineActions">
                {selectedAgent ? <button className="tinyButton primaryButton" onClick={() => onOpenAgent(selectedAgent.name)}>Open runtime view</button> : null}
              </div>
            }
          />
          <div className="stack">
            {agents.map((agent) => (
              <button
                key={agent.name}
                className={`listCard inventoryCard ${selectedAgent?.name === agent.name ? "selected" : ""}`}
                onClick={() => onSelectAgent(agent.name)}
              >
                <div className="inventoryHeader">
                  <strong>{agent.name}</strong>
                  <span className="inventoryTag">{agent.harness}</span>
                </div>
                <span className="inventoryScope">{agent.agent_pack || "custom pack"}</span>
                <div className="pills">
                  <span className="pill">{agent.approval_policy}</span>
                  <span className="pill">{agent.workspace}</span>
                </div>
              </button>
            ))}
          </div>
        </section>

        <section className="panel spanTwo" id="pack-editor">
          <PanelHeader
            title="Selected pack"
            copy="Prompt files, system instructions, tools, skills, and environment keys for the selected agent."
            meta={selectedAgent?.agent_pack || selectedAgent?.name || "No selection"}
            actions={
              selectedAgent ? (
                <div className="inlineActions">
                  <button className="tinyButton primaryButton" onClick={onLoadSelectedAgent}>Edit selected</button>
                  <button className="tinyButton" onClick={() => onOpenAgent(selectedAgent.name)}>Open runtime view</button>
                </div>
              ) : null
            }
          />
          {selectedAgent ? (
            <div className="promptGrid">
              <article className="detailCard">
                <Metric label="Description" value={selectedAgent.description || "n/a"} />
                <Metric label="Pack" value={selectedAgent.agent_pack || "custom"} />
                <Metric label="Harness" value={selectedAgent.harness} />
                <Metric label="Prompt file" value={selectedAgent.prompt} />
                <Metric label="Pack path" value={selectedAgent.pack_path || "n/a"} />
              </article>
              <article className="detailCard">
                <span className="eyebrow">Tools / MCPs / CLIs</span>
                <div className="pills">{(selectedAgent.tools || []).map((tool) => <Pill key={tool} tone="info">{tool}</Pill>)}</div>
                <span className="eyebrow">Skills</span>
                <div className="pills">{(selectedAgent.skills || []).map((skill) => <Pill key={skill}>{skill}</Pill>)}</div>
                <span className="eyebrow">Environment keys</span>
                <div className="pills">{(selectedAgent.env_keys || []).map((envKey) => <Pill key={envKey}>{envKey}</Pill>)}</div>
              </article>
              <article className="detailCard spanTwo">
                <span className="eyebrow">System prompt</span>
                <pre className="logBlock">{selectedAgent.system_prompt || "n/a"}</pre>
              </article>
              <article className="detailCard spanTwo">
                <span className="eyebrow">Prompt template</span>
                <pre className="logBlock">{selectedAgent.prompt_body || "n/a"}</pre>
              </article>
              {(selectedAgent.context_bodies || []).map((context) => (
                <article key={context.path} className="detailCard spanTwo">
                  <span className="eyebrow">{context.path}</span>
                  <pre className="logBlock">{context.content || "n/a"}</pre>
                </article>
              ))}
            </div>
          ) : (
            <EmptyState copy="Select a pack to inspect it." />
          )}
        </section>

        <section className="panel spanTwo">
          <PanelHeader
            title="Pack editor"
            copy="Edit the selected pack or create a new one. Changes persist to the pack files on disk."
            meta={packDraft.originalName ? `Editing ${packDraft.originalName}` : "New pack"}
            actions={
              <div className="inlineActions">
                <button className="tinyButton" onClick={onResetDraft}>New pack</button>
                <button className="tinyButton primaryButton" onClick={onSavePack}>Save pack</button>
              </div>
            }
          />
          <div className="builderGrid">
            <Control label="Name">
              <input value={packDraft.name} onChange={(event) => onPackDraftChange({ ...packDraft, name: event.target.value })} />
            </Control>
            <Control label="Description">
              <input value={packDraft.description} onChange={(event) => onPackDraftChange({ ...packDraft, description: event.target.value })} />
            </Control>
            <Control label="Instance name">
              <input value={packDraft.instanceName} onChange={(event) => onPackDraftChange({ ...packDraft, instanceName: event.target.value })} />
            </Control>
            <Control label="Harness">
              <select value={packDraft.harness} onChange={(event) => onPackDraftChange({ ...packDraft, harness: event.target.value })}>
                <option value="claude-code">claude-code</option>
                <option value="codex">codex</option>
              </select>
            </Control>
            <Control label="Workspace">
              <select value={packDraft.workspace} onChange={(event) => onPackDraftChange({ ...packDraft, workspace: event.target.value })}>
                <option value="git-clone">git-clone</option>
              </select>
            </Control>
            <Control label="Approval policy">
              <select value={packDraft.approvalPolicy} onChange={(event) => onPackDraftChange({ ...packDraft, approvalPolicy: event.target.value })}>
                <option value="auto">auto</option>
                <option value="manual">manual</option>
              </select>
            </Control>
            <Control label="Max concurrency">
              <input value={packDraft.maxConcurrent} onChange={(event) => onPackDraftChange({ ...packDraft, maxConcurrent: event.target.value })} />
            </Control>
            <Control label="Tools">
              <input value={packDraft.tools} onChange={(event) => onPackDraftChange({ ...packDraft, tools: event.target.value })} placeholder="git, gh, glab" />
            </Control>
            <Control label="Skills">
              <input value={packDraft.skills} onChange={(event) => onPackDraftChange({ ...packDraft, skills: event.target.value })} placeholder="code-pr, triage" />
            </Control>
            <Control label="Env keys">
              <input value={packDraft.envKeys} onChange={(event) => onPackDraftChange({ ...packDraft, envKeys: event.target.value })} placeholder="OPENAI_API_KEY, GITLAB_TOKEN" />
            </Control>
          </div>
          <div className="builderGrid builderGridWide">
            <Control label="Prompt template">
              <textarea className="editor compactEditor" value={packDraft.promptBody} onChange={(event) => onPackDraftChange({ ...packDraft, promptBody: event.target.value })} />
            </Control>
            <Control label="Context body">
              <textarea className="editor compactEditor" value={packDraft.contextBody} onChange={(event) => onPackDraftChange({ ...packDraft, contextBody: event.target.value })} />
            </Control>
          </div>
          <p className="message">{packResult || "Save will validate the current config with the updated pack and keep a restart-required model."}</p>
          <pre className="logBlock">{renderPackDraft(packDraft)}</pre>
        </section>
      </div>
    </section>
  );
}
