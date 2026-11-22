import { createReporter } from "./report.mjs";

// OpenCode v2's Plugin.define is an identity function. A plain plugin object
// avoids installing a second SDK or downloading dependencies inside the guest.
export default {
  id: "agentnook.telemetry",
  setup(ctx) {
    const runId = process.env.AGENTNOOK_REPORT_ID;
    if (!runId || !/^[a-f0-9]{32}$/.test(runId)) return;
    const publish = createReporter();
    const sessions = new Map();
    const controller = new AbortController();
    let last = runId;
    let heartbeat;
    const report = () => {
      const entries = [...sessions.entries()];
      const blocked = entries.find(([, s]) => s.pending.size > 0);
      const busy = entries.find(([, s]) => s.busy);
      publish(blocked?.[0] ?? busy?.[0] ?? last, blocked ? "blocked" : busy ? "working" : "idle");
    };
    const types = ["session.status", "session.deleted", "permission.asked", "permission.replied", "form.created", "form.replied", "form.cancelled"];
    void (async () => {
      for await (const event of ctx.event.subscribe({ types, signal: controller.signal })) {
        if (event.location?.directory && event.location.directory !== ctx.location.directory) continue;
        const data = event.data;
        const id = data?.sessionID ?? data?.form?.sessionID;
        if (typeof id !== "string") continue;
        last = id;
        if (event.type === "session.deleted") { sessions.delete(id); report(); continue; }
        if (!sessions.has(id) && sessions.size >= 256) continue;
        const session = sessions.get(id) ?? { busy: false, pending: new Set() };
        sessions.set(id, session);
        switch (event.type) {
          case "session.status":
            if (!["idle", "busy", "retry"].includes(data.status?.type)) continue;
            session.busy = data.status.type !== "idle";
            if (!session.busy) session.pending.clear();
            break;
          case "permission.asked": session.pending.add(`permission:${data.id}`); break;
          case "permission.replied": session.pending.delete(`permission:${data.requestID}`); break;
          case "form.created": session.pending.add(`form:${data.form.id}`); break;
          case "form.replied":
          case "form.cancelled": session.pending.delete(`form:${data.requestID ?? data.id}`); break;
        }
        report();
      }
    })().catch(() => {}).finally(() => { clearInterval(heartbeat); });
    report();
    heartbeat = setInterval(report, 1000);
    heartbeat.unref?.();
    return () => { controller.abort(); clearInterval(heartbeat); };
  },
};
