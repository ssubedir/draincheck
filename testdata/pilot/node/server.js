"use strict";

const http = require("node:http");

let ready = true;
let shuttingDown = false;

const server = http.createServer((request, response) => {
  const url = new URL(request.url, "http://localhost");
  if (url.pathname === "/ready") {
    response.writeHead(ready ? 200 : 503);
    response.end();
    return;
  }
  if (url.pathname === "/work") {
    const requestedDelay = Number.parseInt(url.searchParams.get("delay_ms") || "2000", 10);
    const delay = Number.isFinite(requestedDelay) ? Math.min(Math.max(requestedDelay, 0), 30000) : 2000;
    setTimeout(() => {
      response.writeHead(200, { "content-type": "text/plain" });
      response.end("completed\n");
    }, delay);
    return;
  }
  response.writeHead(404);
  response.end();
});

server.listen(8080, "0.0.0.0", () => {
  process.stdout.write("Node pilot listening on port 8080\n");
});

function shutdown(signal) {
  if (shuttingDown) {
    return;
  }
  shuttingDown = true;
  ready = false;
  process.stdout.write(`received ${signal}; draining requests\n`);

  setTimeout(() => {
    server.close((error) => {
      if (error) {
        process.stderr.write(`shutdown failed: ${error.message}\n`);
        process.exitCode = 1;
        return;
      }
      process.stdout.write("graceful shutdown complete\n");
    });
  }, 100);

  setTimeout(() => {
    process.stderr.write("shutdown deadline exceeded\n");
    process.exit(1);
  }, 10000).unref();
}

process.on("SIGTERM", () => shutdown("SIGTERM"));
process.on("SIGINT", () => shutdown("SIGINT"));
