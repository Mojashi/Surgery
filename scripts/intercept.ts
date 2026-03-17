/**
 * API Intercept Proxy for Claude Code
 *
 * Usage:
 *   bun run scripts/intercept.ts [port]
 *
 * Then in another terminal:
 *   ANTHROPIC_BASE_URL=http://localhost:18888 claude -r <session-id> -p "hello"
 *
 * Saves each API request to /tmp/intercept-<port>-<n>.json
 * Prints summary to stderr.
 */

const port = parseInt(Bun.argv[2] || "18888");
let counter = 0;

const server = Bun.serve({
  port,
  async fetch(req) {
    const url = new URL(req.url);
    const target = "https://api.anthropic.com" + url.pathname + url.search;
    const body = await req.text();

    if (url.pathname.includes("/messages") && req.method === "POST") {
      counter++;
      const data = JSON.parse(body);
      const msgs: any[] = data.messages || [];

      // Save full request
      const fname = `/tmp/intercept-${port}-${counter}.json`;
      await Bun.write(fname, body);

      // Print summary
      console.error(`\n#${counter} → ${fname}`);
      console.error(`  ${msgs.length} messages, ${(body.length / 1024).toFixed(1)}KB`);

      // Count blocks
      const counts: Record<string, number> = {};
      const sizes: Record<string, number> = {};
      for (const m of msgs) {
        const content = m.content;
        if (Array.isArray(content)) {
          for (const b of content) {
            const t = b.type || "?";
            counts[t] = (counts[t] || 0) + 1;
            if (t === "text") sizes[t] = (sizes[t] || 0) + (b.text?.length || 0);
            else if (t === "tool_result") {
              const c = b.content;
              sizes[t] = (sizes[t] || 0) + (typeof c === "string" ? c.length : JSON.stringify(c).length);
            } else if (t === "tool_use") {
              sizes[t] = (sizes[t] || 0) + JSON.stringify(b.input || {}).length;
            } else if (t === "thinking") {
              sizes[t] = (sizes[t] || 0) + (b.thinking?.length || 0);
            }
          }
        } else if (typeof content === "string") {
          counts["string"] = (counts["string"] || 0) + 1;
          sizes["string"] = (sizes["string"] || 0) + content.length;
        }
      }

      const lines = Object.keys(counts).sort().map(t => {
        const s = sizes[t] ? ` (${(sizes[t]! / 1024).toFixed(1)}KB)` : "";
        return `${t}: ${counts[t]}${s}`;
      });
      console.error(`  ${lines.join(", ")}`);
    }

    // Forward
    const headers = new Headers(req.headers);
    headers.delete("host");
    headers.set("accept-encoding", "identity");

    const resp = await fetch(target, {
      method: req.method,
      headers,
      body: req.method !== "GET" ? body : undefined,
    });

    const respHeaders = new Headers(resp.headers);
    respHeaders.delete("content-encoding");

    if (resp.status >= 400) {
      const respBody = await resp.text();
      const errMsg = JSON.parse(respBody)?.error?.message || respBody;
      console.error(`  ⚠ ${resp.status}: ${errMsg.slice(0, 200)}`);
      return new Response(respBody, { status: resp.status, headers: respHeaders });
    }

    return new Response(resp.body, { status: resp.status, headers: respHeaders });
  },
});

console.error(`Intercept proxy on http://localhost:${port}`);
console.error(`Requests saved to /tmp/intercept-${port}-*.json`);
console.error(`Usage: ANTHROPIC_BASE_URL=http://localhost:${port} claude -r <id> -p "..."`);
