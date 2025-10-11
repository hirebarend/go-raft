import { spawn } from "node:child_process";

const NODES = [
  { data: "data/data-1", port: 8081 },
  { data: "data/data-2", port: 8082 },
  { data: "data/data-3", port: 8083 },
  { data: "data/data-4", port: 8084 },
  { data: "data/data-5", port: 8085 },
];

function spawnNode(node) {
  const args = [
    "--data",
    node.data,
    "--port",
    String(node.port),
    "--nodes",
    NODES.map((x) => `127.0.0.1:${x.port}`).join(","),
  ];

  const result = {
    alive: true,
    childProcess: spawn("./raft-go", args, {
      stdio: ["ignore", "inherit", "inherit"],
    }),
    node,
  };

  result.childProcess.on("exit", (code, signal) => {
    result.alive = false;
    console.log(`[127.0.0.1:${node.port}] - exit`);
  });

  result.childProcess.on("error", (err) => {
    result.alive = false;
    console.log(`[127.0.0.1:${node.port}] - error`);
  });

  return result;
}

function isAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function killNode(obj) {
  try {
    process.kill(obj.childProcess.pid, "SIGTERM");
  } catch {}

  for (let i = 0; i < 10; i++) {
    if (!isAlive(obj.childProcess.pid)) {
      break;
    }

    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  if (isAlive(obj.childProcess.pid)) {
    try {
      process.kill(obj.childProcess.pid, "SIGKILL");
    } catch {}
  }

  await new Promise((resolve) => setTimeout(resolve, 100));
}

const arr = [];
let interval = null;

(async () => {
  for (const node of NODES) {
    const x = await spawnNode(node);

    arr.push(x);
  }

  interval = setInterval(async () => {
    const obj = arr[Math.floor(Math.random() * arr.length)];

    console.log(`[${obj.node.port}] - killing`);
    await killNode(obj);

    await new Promise((resolve) => setTimeout(resolve, 5000));

    console.log(`[${obj.node.port}] - spawning`);
    await spawnNode(obj.node);
  }, 30000);
})();

process.on("SIGINT", async () => {
  clearInterval(interval);

  for (const x of arr) {
    await killNode(x);
  }

  await new Promise((resolve) => setTimeout(resolve, 2000));

  process.exit(130);
});

process.on("SIGTERM", async () => {
  clearInterval(interval);

  for (const x of arr) {
    await killNode(x);
  }

  await new Promise((resolve) => setTimeout(resolve, 2000));

  process.exit(143);
});

process.on("uncaughtException", async () => {
  clearInterval(interval);

  for (const x of arr) {
    await killNode(x);
  }

  await new Promise((resolve) => setTimeout(resolve, 2000));

  process.exit(1);
});

process.on("unhandledRejection", async () => {
  clearInterval(interval);

  for (const x of arr) {
    await killNode(x);
  }

  await new Promise((resolve) => setTimeout(resolve, 2000));

  process.exit(1);
});
