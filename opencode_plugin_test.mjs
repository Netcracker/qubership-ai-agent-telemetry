import test from "node:test";
import { setImmediate, setTimeout } from "node:timers";
import assert from "node:assert/strict";
import { createPlugin } from "./opencode_plugin.mjs";

async function fixture({
  servers = ["docs"],
  tools = [
    { id: "docs_search", name: "search", options: { namespace: "docs" } },
  ],
  directory = "/fixture",
  realTransport = false,
} = {}) {
  const sent = [],
    hooks = {},
    queue = [],
    waiters = [];
  let disposed = 0;
  const ctx = {
    location: { directory },
    tool: {
      list: async () => tools,
      hook: async (name, fn) => {
        hooks[name] = fn;
        return {
          dispose: async () => {
            disposed++;
          },
        };
      },
    },
    mcp: { list: async () => ({ data: servers.map((name) => ({ name })) }) },
    session: {
      context: async () => [
        {
          id: "msg_attach",
          type: "user",
          skills: [
            { id: "git-release", name: "PRIVATE", text: "PRIVATE_BODY" },
          ],
        },
      ],
    },
    event: {
      subscribe: ({ signal }) => ({
        [Symbol.asyncIterator]() {
          return this;
        },
        next() {
          if (signal.aborted) return Promise.resolve({ done: true });
          if (queue.length)
            return Promise.resolve({ value: queue.shift(), done: false });
          return new Promise((resolve) => {
            waiters.push(resolve);
            signal.addEventListener("abort", () => resolve({ done: true }), {
              once: true,
            });
          });
        },
      }),
    },
  };
  const stop = await createPlugin(
    realTransport ? undefined : (e) => sent.push(e),
  ).setup(ctx);
  return {
    sent,
    hooks,
    ctx,
    stop,
    get disposed() {
      return disposed;
    },
    disconnect() {
      waiters.shift()?.({ done: true });
    },
    event(e) {
      const waiter = waiters.shift();
      if (waiter) waiter({ value: e, done: false });
      else queue.push(e);
    },
    async flush() {
      await new Promise((resolve) => setImmediate(resolve));
    },
  };
}
const call = {
  tool: "skill",
  sessionID: "ses_test",
  messageID: "msg_test",
  id: "call_1",
  input: { id: "git-release", text: "PRIVATE" },
  status: "completed",
  result: { metadata: { name: "Private Display" }, content: "PRIVATE_BODY" },
};

test("emits successful native Skill.ID without bodies or display names", async () => {
  const f = await fixture();
  await f.hooks["execute.after"](call);
  await f.flush();
  assert.deepEqual(f.sent, [
    {
      type: "skill",
      session_id: "ses_test",
      directory: "/fixture",
      skill_id: "git-release",
    },
  ]);
  await f.stop();
});
test("drops failed skills and unrelated reads", async () => {
  const f = await fixture();
  await f.hooks["execute.after"]({ ...call, status: "error" });
  await f.hooks["execute.after"]({ ...call, tool: "read" });
  await f.flush();
  assert.deepEqual(f.sent, []);
  await f.stop();
});
test("counts three nested completions sharing an outer ID without durations", async () => {
  const f = await fixture();
  for (const status of ["completed", "completed", "error"])
    await f.hooks["execute.after"]({ ...call, tool: "docs_search", status });
  await f.flush();
  assert.deepEqual(
    f.sent.map((e) => [e.server, e.tool, e.outcome, e.duration_ms]),
    [
      ["docs", "search", "succeeded", undefined],
      ["docs", "search", "succeeded", undefined],
      ["docs", "search", "failed", undefined],
    ],
  );
  await f.stop();
});
test("refreshes registry identities and skips colliding server namespaces", async () => {
  const f = await fixture({
    servers: ["docs.a", "docs_a"],
    tools: [
      {
        id: "docs_a_search",
        name: "search.original",
        options: { namespace: "docs_a" },
      },
    ],
  });
  await f.hooks["execute.after"]({ ...call, tool: "docs_a_search" });
  assert.deepEqual(f.sent, []);
  f.ctx.mcp.list = async () => ({ data: [{ name: "docs.a" }] });
  await f.hooks["execute.after"]({ ...call, tool: "docs_a_search" });
  await f.flush();
  assert.deepEqual(
    f.sent.map((e) => [e.server, e.tool]),
    [["docs.a", "search.original"]],
  );
  await f.stop();
});
test("accepts registry classification even for an overridden tool", async () => {
  const f = await fixture();
  await f.hooks["execute.after"]({
    ...call,
    tool: "docs_search",
    result: { content: "NOT_MCP" },
  });
  await f.flush();
  assert.equal(f.sent.length, 1);
  await f.stop();
});
test("resolves delivered attachment without observing enqueue and filters other locations", async () => {
  const f = await fixture();
  f.event({
    type: "session.inbox.delivered",
    location: { directory: "/other" },
    data: { sessionID: "ses_test", inboxID: "msg_attach" },
  });
  f.event({
    type: "session.inbox.delivered",
    location: { directory: "/fixture" },
    data: { sessionID: "ses_child", inboxID: "msg_attach" },
  });
  await f.flush();
  assert.deepEqual(f.sent, [
    {
      type: "skill",
      session_id: "ses_child",
      directory: "/fixture",
      skill_id: "git-release",
    },
  ]);
  await f.stop();
  assert.equal(f.disposed, 2);
});
test("does not count enqueue, cancellation, or synthetic text as activation", async () => {
  const f = await fixture();
  for (const type of ["session.inbox.enqueued", "session.inbox.cancelled"])
    f.event({
      type,
      location: { directory: "/fixture" },
      data: {
        sessionID: "ses_test",
        inboxID: "msg_attach",
        item: { payload: { skills: [{ id: "x" }] } },
      },
    });
  await f.flush();
  assert.deepEqual(f.sent, []);
  await f.stop();
});
test("observes standalone native skill activation", async () => {
  const f = await fixture();
  f.event({
    type: "session.skill.activated",
    location: { directory: "/fixture" },
    data: { sessionID: "ses_test", id: "git-release", text: "PRIVATE" },
  });
  await f.flush();
  assert.deepEqual(f.sent, [
    {
      type: "skill",
      session_id: "ses_test",
      directory: "/fixture",
      skill_id: "git-release",
    },
  ]);
  await f.stop();
});
test("isolates transport and registry errors from agent execution", async () => {
  const f = await fixture();
  f.ctx.tool.list = async () => {
    throw Error("fixture");
  };
  await assert.doesNotReject(
    f.hooks["execute.after"]({ ...call, tool: "docs_search" }),
  );
  await f.stop();
});

test("counts a denied direct MCP attempt even when execute.after is absent", async () => {
  const f = await fixture();
  assert.equal(typeof f.hooks["execute.before"], "function");
  await f.hooks["execute.before"]({ ...call, tool: "docs_search" });
  f.event({
    type: "session.tool.failed",
    location: { directory: "/fixture" },
    data: {
      sessionID: "ses_test",
      assistantMessageID: "msg_test",
      id: "call_1",
    },
  });
  await f.flush();
  assert.deepEqual(
    f.sent.map((e) => [e.tool, e.outcome]),
    [["search", "failed"]],
  );
  await f.stop();
});
test("keeps denied nested attempts while avoiding duplicate terminal events", async () => {
  const f = await fixture();
  assert.equal(typeof f.hooks["execute.before"], "function");
  await f.hooks["execute.before"]({ ...call, tool: "execute" });
  for (let i = 0; i < 3; i++)
    await f.hooks["execute.before"]({ ...call, tool: "docs_search" });
  for (const status of ["completed", "error"])
    await f.hooks["execute.after"]({ ...call, tool: "docs_search", status });
  await f.hooks["execute.after"]({
    ...call,
    tool: "execute",
    result: {
      metadata: {
        toolCalls: [
          { tool: "docs.search", status: "completed" },
          { tool: "docs.search", status: "error" },
          { tool: "docs.search", status: "error" },
        ],
      },
    },
  });
  await f.flush();
  assert.deepEqual(
    f.sent.map((e) => e.outcome),
    ["succeeded", "failed", "failed"],
  );
  await f.stop();
});

test("flushes unresolved nested attempts when the outer call fails without after hooks", async () => {
  const f = await fixture();
  await f.hooks["execute.before"]({ ...call, tool: "execute" });
  await f.hooks["execute.before"]({ ...call, tool: "docs_search" });
  f.event({
    type: "session.tool.failed",
    location: { directory: "/fixture" },
    data: {
      sessionID: "ses_test",
      assistantMessageID: "msg_test",
      id: "call_1",
    },
  });
  await f.flush();
  assert.deepEqual(
    f.sent.map((e) => [e.tool, e.outcome]),
    [["search", "failed"]],
  );
  await f.stop();
});

test(
  "returns while the ingest process is stalled and sends only the envelope",
  { skip: globalThis.process.platform === "win32" },
  async () => {
    const fs = await import("node:fs/promises");
    const os = await import("node:os");
    const path = await import("node:path");
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "opencode-transport-"));
    const cli = path.join(dir, "ai-agent-telemetry");
    const inputFile = path.join(dir, "input.json");
    const release = path.join(dir, "release");
    await fs.writeFile(
      cli,
      "#!/usr/bin/env node\n" +
        `const fs=require('node:fs');let input='';process.stdin.on('data',b=>input+=b);process.stdin.on('end',()=>{fs.writeFileSync(${JSON.stringify(inputFile)},JSON.stringify({args:process.argv.slice(2),envelope:JSON.parse(input)}));const t=setInterval(()=>{if(fs.existsSync(${JSON.stringify(release)})){clearInterval(t);}},10);});\n`,
      { mode: 0o700 },
    );
    const originalPath = globalThis.process.env.PATH;
    globalThis.process.env.PATH = dir + path.delimiter + originalPath;
    const f = await fixture({ realTransport: true });
    try {
      await f.hooks["execute.after"](call);
      let received;
      for (let i = 0; i < 200; i++) {
        try {
          received = JSON.parse(await fs.readFile(inputFile, "utf8"));
          break;
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 10));
        }
      }
      assert.deepEqual(received, {
        args: ["ingest", "--agent=opencode"],
        envelope: {
          type: "skill",
          session_id: "ses_test",
          skill_id: "git-release",
          directory: "/fixture",
        },
      });
      await assert.rejects(fs.stat(release), { code: "ENOENT" });
    } finally {
      await fs.writeFile(release, "done");
      globalThis.process.env.PATH = originalPath;
      await f.stop();
    }
  },
);

test("does not emit a late nested completion already accounted for by outer failure", async () => {
  const f = await fixture();
  await f.hooks["execute.before"]({ ...call, tool: "execute" });
  await f.hooks["execute.before"]({ ...call, tool: "docs_search" });
  f.event({
    type: "session.tool.failed",
    location: { directory: "/fixture" },
    data: {
      sessionID: "ses_test",
      assistantMessageID: "msg_test",
      id: "call_1",
    },
  });
  await f.flush();
  await f.hooks["execute.after"]({
    ...call,
    tool: "docs_search",
    status: "error",
  });
  await f.flush();
  assert.deepEqual(
    f.sent.map((e) => e.outcome),
    ["failed"],
  );
  await f.stop();
});

test("resumes new events after disconnect without reconstructing missed attempts", async () => {
  const f = await fixture();
  const subscribe = f.ctx.event.subscribe;
  let reconnected = false;
  f.ctx.event.subscribe = (options) => {
    reconnected = true;
    return subscribe(options);
  };
  await f.hooks["execute.before"]({ ...call, tool: "docs_search" });
  f.disconnect();
  try {
    for (let i = 0; i < 100 && !reconnected; i++)
      await new Promise((resolve) => setTimeout(resolve, 10));
    assert.equal(reconnected, true);
    f.event({
      type: "session.tool.failed",
      location: { directory: "/fixture" },
      data: {
        sessionID: "ses_test",
        assistantMessageID: "msg_test",
        id: "call_1",
      },
    });
    f.event({
      type: "session.skill.activated",
      location: { directory: "/fixture" },
      data: { sessionID: "ses_new", id: "git-release" },
    });
    await f.flush();
    assert.deepEqual(f.sent, [
      {
        type: "skill",
        session_id: "ses_new",
        skill_id: "git-release",
        directory: "/fixture",
      },
    ]);
  } finally {
    await f.stop();
  }
});
