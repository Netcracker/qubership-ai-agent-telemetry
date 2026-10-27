// Managed by ai-agent-telemetry. Do not edit.
import { spawn } from "node:child_process";
import { setTimeout, clearTimeout } from "node:timers";

function sendEnvelope(envelope) {
  const child = spawn("ai-agent-telemetry", ["ingest", "--agent=opencode"], {
    stdio: ["pipe", "ignore", "ignore"],
    windowsHide: true,
  });
  child.on("error", () => {});
  child.stdin.on("error", () => {});
  child.stdin.end(JSON.stringify(envelope));
  child.unref();
}

export function createPlugin(send = sendEnvelope) {
  return {
    id: "ai-agent-telemetry",
    async setup(ctx) {
      const directory = ctx.location.directory;
      const controller = new globalThis.AbortController();
      const registrations = [];
      const pending = new Map();
      const activeCode = new Set();
      const settled = new Set();
      const settle = (callKey) => {
        settled.add(callKey);
        if (settled.size > 2048) settled.delete(settled.values().next().value);
      };
      const key = (event) =>
        JSON.stringify([
          event.sessionID,
          event.messageID ?? event.assistantMessageID,
          event.id,
        ]);
      const mcpEvent = (sessionID, mcp, status) =>
        emit({
          type: "mcp",
          session_id: sessionID,
          ...mcp,
          outcome: status === "completed" ? "succeeded" : "failed",
        });
      const emit = (fields) => {
        if (controller.signal.aborted) return;
        try {
          Promise.resolve(send({ ...fields, directory })).catch(() => {});
        } catch {
          /* Telemetry failures must not interrupt agent operations. */
        }
      };
      const skill = (sessionID, id) => {
        if (typeof id === "string")
          emit({ type: "skill", session_id: sessionID, skill_id: id });
      };
      const identity = async (id) => {
        const [tools, servers] = await Promise.all([
          ctx.tool.list(),
          ctx.mcp.list(),
        ]);
        const tool = tools.find((tool) => tool.id === id);
        if (!tool?.options?.namespace) return;
        const candidates = servers.data.filter(
          (server) =>
            server.name.replace(/[^a-zA-Z0-9_-]/g, "_") ===
            tool.options.namespace,
        );
        if (candidates.length === 1)
          return { server: candidates[0].name, tool: tool.name };
      };
      registrations.push(
        await ctx.tool.hook("execute.before", async (event) => {
          try {
            const callKey = key(event);
            if (!activeCode.has(callKey)) settled.delete(callKey);
            if (event.tool === "execute") {
              activeCode.add(callKey);
              return;
            }
            const mcp = await identity(event.tool);
            if (mcp) {
              const calls = pending.get(callKey) ?? [];
              calls.push({
                name: event.tool,
                mcp,
                nested: activeCode.has(callKey),
              });
              pending.set(callKey, calls);
            }
          } catch {
            /* Telemetry failures must not interrupt agent operations. */
          }
        }),
      );
      registrations.push(
        await ctx.tool.hook("execute.after", async (event) => {
          try {
            const callKey = key(event);
            if (settled.has(callKey)) return;
            const calls = pending.get(callKey) ?? [];
            if (event.tool === "execute") {
              for (const call of calls)
                mcpEvent(event.sessionID, call.mcp, "error");
              pending.delete(callKey);
              activeCode.delete(callKey);
              settle(callKey);
              return;
            }
            const index = calls.findIndex((call) => call.name === event.tool);
            const tracked = index < 0 ? undefined : calls.splice(index, 1)[0];
            if (calls.length === 0) pending.delete(callKey);
            if (event.tool === "skill") {
              if (event.status === "completed")
                skill(event.sessionID, event.input?.id);
              return;
            }
            const mcp = tracked?.mcp ?? (await identity(event.tool));
            if (mcp && ["completed", "error"].includes(event.status)) {
              mcpEvent(event.sessionID, mcp, event.status);
            }
          } catch {
            /* Telemetry failures must not interrupt agent operations. */
          }
        }),
      );
      const observe = async () => {
        while (!controller.signal.aborted) {
          try {
            for await (const event of ctx.event.subscribe({
              signal: controller.signal,
            })) {
              if (controller.signal.aborted) break;
              if (event.location?.directory !== directory) continue;
              try {
                const data = event.data;
                if (event.type === "session.tool.failed") {
                  const callKey = key(data);
                  const calls = pending.get(callKey) ?? [];
                  for (const call of calls)
                    mcpEvent(data.sessionID, call.mcp, "error");
                  pending.delete(callKey);
                  activeCode.delete(callKey);
                  if (calls.length > 0) settle(callKey);
                }
                if (event.type === "session.skill.activated")
                  skill(data.sessionID, data.id);
                if (event.type === "session.inbox.delivered") {
                  const messages = await ctx.session.context({
                    sessionID: data.sessionID,
                  });
                  const message = messages.find(
                    (message) =>
                      message.id === data.inboxID && message.type === "user",
                  );
                  for (const attachment of message?.skills ?? [])
                    skill(data.sessionID, attachment.id);
                }
              } catch {
                /* Telemetry failures must not interrupt agent operations. */
              }
            }
          } catch {
            /* Telemetry failures must not interrupt agent operations. */
          }
          pending.clear();
          activeCode.clear();
          if (!controller.signal.aborted)
            await new Promise((resolve) => {
              const abort = () => {
                clearTimeout(timer);
                resolve();
              };
              const timer = setTimeout(() => {
                controller.signal.removeEventListener("abort", abort);
                resolve();
              }, 250);
              controller.signal.addEventListener("abort", abort, {
                once: true,
              });
            });
        }
      };
      void observe();
      return async () => {
        controller.abort();
        pending.clear();
        activeCode.clear();
        settled.clear();
        for (const registration of registrations) await registration.dispose();
      };
    },
  };
}

export default createPlugin();
