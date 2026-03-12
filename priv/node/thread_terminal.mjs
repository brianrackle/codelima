import fs from "fs";
import http from "http";
import path from "path";
import process from "process";
import pty from "@lydell/node-pty";
import { WebSocketServer } from "ws";

function parseArgs(argv) {
  const options = {};

  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];

    if (!key?.startsWith("--") || value == null) {
      throw new Error(`invalid arguments near ${key ?? "<end>"}`);
    }

    options[key.slice(2)] = value;
  }

  return options;
}

const options = parseArgs(process.argv.slice(2));
const required = ["port", "token", "project-dir", "vm-name", "log-path", "bootstrap"];

for (const key of required) {
  if (!options[key]) {
    throw new Error(`missing required option --${key}`);
  }
}

const port = Number.parseInt(options.port, 10);
const token = options.token;
const projectDir = options["project-dir"];
const vmName = options["vm-name"];
const logPath = options["log-path"];
const setupCommands = options["setup-commands"] ?? "";
const bootstrap = options.bootstrap;
const connections = new Set();
let pendingOutput = "";
let lastOutputLine = null;
let lastFailureLine = null;

fs.mkdirSync(path.dirname(logPath), { recursive: true });

function emit(event, payload = {}) {
  process.stdout.write(`${JSON.stringify({ event, ...payload })}\n`);
}

function appendRecord(direction, data) {
  fs.appendFileSync(
    logPath,
    `${JSON.stringify({
      timestamp: new Date().toISOString(),
      direction,
      data
    })}\n`
  );
}

function rememberOutput(data) {
  pendingOutput += data;
  const lines = pendingOutput.split(/\r?\n/);
  pendingOutput = lines.pop() ?? "";

  for (const line of lines) {
    rememberLine(line);
  }

  rememberLine(pendingOutput);
}

function rememberLine(line) {
  const trimmed = line.trim();

  if (!trimmed) {
    return;
  }

  lastOutputLine = trimmed;

  if (!trimmed.startsWith("[bootstrap]")) {
    lastFailureLine = trimmed;
  }
}

const shell = pty.spawn(bootstrap, [projectDir, vmName, setupCommands], {
  name: "xterm-256color",
  cols: 120,
  rows: 36,
  cwd: projectDir,
  env: {
    ...process.env,
    COLORTERM: "truecolor",
    TERM: "xterm-256color"
  }
});

const server = http.createServer((_req, res) => {
  res.writeHead(404);
  res.end("Not Found");
});

const socketServer = new WebSocketServer({ noServer: true });

server.on("upgrade", (req, socket, head) => {
  const url = new URL(req.url, `http://${req.headers.host}`);

  if (url.pathname !== "/" || url.searchParams.get("token") !== token) {
    socket.write("HTTP/1.1 403 Forbidden\r\n\r\n");
    socket.destroy();
    return;
  }

  socketServer.handleUpgrade(req, socket, head, (ws) => {
    socketServer.emit("connection", ws, req);
  });
});

socketServer.on("connection", (ws) => {
  connections.add(ws);

  ws.on("message", (buffer) => {
    const message = buffer.toString("utf8");

    if (message.startsWith("{")) {
      try {
        const payload = JSON.parse(message);

        if (payload.type === "resize") {
          shell.resize(payload.cols, payload.rows);
          return;
        }
      } catch {
        // Fall through and treat it as input.
      }
    }

    appendRecord("input", message);
    shell.write(message);
  });

  ws.on("close", () => {
    connections.delete(ws);
  });

  ws.on("error", () => {
    connections.delete(ws);
  });
});

shell.onData((data) => {
  appendRecord("output", data);
  rememberOutput(data);

  for (const connection of connections) {
    if (connection.readyState === connection.OPEN) {
      connection.send(data);
    }
  }
});

shell.onExit(({ exitCode, signal }) => {
  const exitMessage = `PTY exited with code ${exitCode}${signal ? ` via ${signal}` : ""}.`;

  if ((exitCode ?? 0) === 0 && !signal) {
    emit("status", { message: exitMessage });
  } else {
    emit("error", {
      message: lastFailureLine || lastOutputLine || exitMessage
    });
  }

  for (const connection of connections) {
    if (connection.readyState === connection.OPEN) {
      connection.close();
    }
  }

  server.close(() => process.exit(exitCode ?? 0));
});

function shutdown() {
  try {
    shell.kill();
  } finally {
    server.close(() => process.exit(0));
  }
}

process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);

server.listen(port, "0.0.0.0", () => {
  emit("ready", { port, projectDir, vmName });
});
